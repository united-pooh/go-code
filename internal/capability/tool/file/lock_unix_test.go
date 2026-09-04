//go:build darwin || linux

package file

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"paw/internal/platform/pawpath"
)

func mutationLockPath(t *testing.T, root string) string {
	t.Helper()
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	projectDir, err := pawpath.ProjectDir(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(projectDir, "locks", "mutation.lock")
}

func TestMutationLockUsesGlobalProjectStorage(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(fmt.Sprintf("override=%t", override), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("PAW_CONFIG_HOME", "")
			storageHome := filepath.Join(home, ".paw")
			if override {
				storageHome = t.TempDir()
				t.Setenv("PAW_CONFIG_HOME", storageHome)
			}
			root := t.TempDir()
			realRoot, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			projectDir, err := pawpath.ProjectDirInHome(storageHome, realRoot)
			if err != nil {
				t.Fatal(err)
			}
			called := false
			callbackErr := errors.New("mutation failed")
			err = withMutationLock(root, func() error {
				called = true
				for path, mode := range map[string]os.FileMode{
					filepath.Join(projectDir, "locks"):                  0o700,
					filepath.Join(projectDir, "locks", "mutation.lock"): 0o600,
				} {
					info, err := os.Stat(path)
					if err != nil {
						t.Errorf("stat lock storage %q: %v", path, err)
					} else if info.Mode().Perm() != mode {
						t.Errorf("mode of %q = %#o, want %#o", path, info.Mode().Perm(), mode)
					}
				}
				return callbackErr
			})
			if !called || !errors.Is(err, callbackErr) {
				t.Fatalf("callback called=%t, error=%v", called, err)
			}
			if _, err := os.Stat(filepath.Join(root, ".paw")); !os.IsNotExist(err) {
				t.Fatalf("mutation lock created workspace .paw: %v", err)
			}
		})
	}
}

func TestMutationLockSymlinkSharesCanonicalLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "workspace-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	canonicalProject, err := pawpath.ProjectDir(root)
	if err != nil {
		t.Fatal(err)
	}
	aliasProject, err := pawpath.ProjectDir(alias)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalProject == aliasProject {
		t.Fatal("session project identity must remain lexical")
	}
	if err := withMutationLock(root, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := withMutationLock(alias, func() error {
		probe, err := os.OpenFile(mutationLockPath(t, root), os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		defer probe.Close()
		err = unix.Flock(int(probe.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			_ = unix.Flock(int(probe.Fd()), unix.LOCK_UN)
			t.Error("symlink workspace did not hold the canonical workspace lock")
		} else if !errors.Is(err, unix.EWOULDBLOCK) {
			t.Errorf("unexpected flock error: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(aliasProject, "locks")); !os.IsNotExist(err) {
		t.Fatalf("symlink identity created a second lock directory: %v", err)
	}
}

func TestMutationLockEmptyRootSkipsLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	home := t.TempDir()
	t.Setenv("PAW_CONFIG_HOME", home)
	called := false
	if err := withMutationLock("", func() error { called = true; return nil }); err != nil || !called {
		t.Fatalf("callback called=%t, error=%v", called, err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty root created storage: %v, error=%v", entries, err)
	}
}

// TestMutationLockExclusiveAcrossOpeners 验证 flock 在该工作区锁文件上是跨打开
// 描述符独占的（等价跨 worker 进程）：持锁期间第二个 opener 非阻塞获取失败，
// 释放后可成功获取。
func TestMutationLockExclusiveAcrossOpeners(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	root := t.TempDir()

	held := make(chan struct{})
	released := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = withMutationLock(root, func() error {
			close(held) // 此时锁已被独占
			<-released
			return nil
		})
		close(done)
	}()

	<-held
	probe, err := os.OpenFile(mutationLockPath(t, root), os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open mutation lock: %v", err)
	}
	defer probe.Close()
	if err := unix.Flock(int(probe.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
		t.Fatal("flock should fail while mutation lock is held")
	}

	close(released) // 释放锁
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("withMutationLock did not release promptly")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := unix.Flock(int(probe.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return // 释放后可获取
		}
		if time.Now().After(deadline) {
			t.Fatal("flock still blocked after release")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
