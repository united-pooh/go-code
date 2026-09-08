package file

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"paw/internal/platform/pawpath"
)

type globalHomeFixture struct {
	home, storage, project, skills, other string
}

func newGlobalHomeFixture(t *testing.T, mode string) globalHomeFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PAW_CONFIG_HOME", "")
	storage := filepath.Join(home, ".paw")
	if mode != "default" {
		storage = filepath.Join(home, "state", "paw")
		t.Setenv("PAW_CONFIG_HOME", storage)
	}
	if mode == "symlink" {
		realStorage := filepath.Join(home, "real-storage")
		if err := os.MkdirAll(realStorage, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(storage), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(realStorage, storage); err != nil {
			t.Fatal(err)
		}
	}
	project, err := pawpath.ProjectDir(home)
	if err != nil {
		t.Fatal(err)
	}
	other, err := pawpath.ProjectDir(filepath.Join(home, "other-workspace"))
	if err != nil {
		t.Fatal(err)
	}
	f := globalHomeFixture{home: home, storage: storage, project: project, skills: filepath.Join(storage, "skills"), other: other}
	for _, path := range []string{
		filepath.Join(home, "documents", "ordinary.txt"),
		filepath.Join(home, "ordinary.txt"),
		filepath.Join(home, "workspace", ".paw", "legacy.txt"),
		filepath.Join(storage+"-neighbor", "ordinary.txt"),
		filepath.Join(project, "tasks", "current.txt"),
		filepath.Join(f.skills, "demo", "SKILL.md"),
		filepath.Join(other, "foreign.txt"),
		filepath.Join(storage, "config.jsonc"),
		filepath.Join(storage, "memory", "private.txt"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("needle original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(other, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../foreign.txt", filepath.Join(other, "nested", "relative-link.txt")); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{
		filepath.Join(home, "directory-alias"):     filepath.Join(other, "nested"),
		filepath.Join(home, "storage-alias"):       storage,
		filepath.Join(home, "foreign-alias.txt"):   filepath.Join(other, "foreign.txt"),
		filepath.Join(project, "foreign-link"):     other,
		filepath.Join(project, "foreign-file.txt"): filepath.Join(other, "foreign.txt"),
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func globalHomeArgs(t *testing.T, path string) json.RawMessage {
	t.Helper()
	args, err := json.Marshal(map[string]any{"path": path, "file_path": path, "pattern": "needle", "literal": true, "content": "changed", "old_string": "original", "new_string": "changed"})
	if err != nil {
		t.Fatal(err)
	}
	return args
}

func TestGlobalHomeReadPolicy(t *testing.T) {
	for _, mode := range []string{"default", "override", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			f := newGlobalHomeFixture(t, mode)
			roots := []string{f.project, f.skills}
			reader := &ReadTool{Root: f.home, ReadRoots: roots}
			for _, tc := range []struct {
				path    string
				allowed bool
			}{
				{filepath.Join(f.home, "documents", "ordinary.txt"), true},
				{filepath.Join(f.home, "workspace", ".paw", "legacy.txt"), true},
				{filepath.Join(f.storage+"-neighbor", "ordinary.txt"), true},
				{filepath.Join(f.project, "tasks", "current.txt"), true},
				{filepath.Join(f.skills, "demo", "SKILL.md"), true},
				{filepath.Join(f.other, "foreign.txt"), false},
				{filepath.Join(f.storage, "config.jsonc"), false},
				{filepath.Join(f.storage, "memory", "private.txt"), false},
				{filepath.Join(f.home, "storage-alias", "config.jsonc"), false},
				{filepath.Join(f.home, "foreign-alias.txt"), false},
				{filepath.Join(f.home, "directory-alias", "relative-link.txt"), false},
				{filepath.Join(f.project, "foreign-link", "foreign.txt"), false},
				{filepath.Join(f.project, "foreign-file.txt"), false},
			} {
				args := globalHomeArgs(t, tc.path)
				out, err := reader.Run(context.Background(), args)
				if (err == nil) != tc.allowed {
					t.Errorf("Read(%s) = %q, %v; allowed=%t", tc.path, out, err, tc.allowed)
				}
				_, outside, err := reader.PermissionReadTarget(args)
				if err != nil || outside == tc.allowed {
					t.Errorf("PermissionReadTarget(%s) outside=%t, err=%v; allowed=%t", tc.path, outside, err, tc.allowed)
				}
			}
			for _, roots := range [][]string{nil, {f.home}, {f.storage}} {
				reader := &ReadTool{Root: f.home, ReadRoots: roots}
				if _, err := reader.Run(context.Background(), globalHomeArgs(t, filepath.Join(f.other, "foreign.txt"))); err == nil {
					t.Errorf("broad ReadRoots %v exposed global storage", roots)
				}
			}
			args := globalHomeArgs(t, filepath.Join(f.other, "foreign.txt"))
			canonical, outside, err := reader.PermissionReadTarget(args)
			if err != nil || !outside {
				t.Fatalf("protected Read must require explicit approval: outside=%t, err=%v", outside, err)
			}
			if _, err := reader.RunApprovedRead(context.Background(), args, canonical); err != nil {
				t.Fatalf("explicit allow-once Read: %v", err)
			}
			if _, err := reader.Run(context.Background(), args); err == nil {
				t.Error("allow-once Read changed default access")
			}
			reader.SetAllowOutsideRoot(true)
			if _, err := reader.Run(context.Background(), globalHomeArgs(t, filepath.Join(f.other, "foreign.txt"))); err != nil {
				t.Fatalf("explicit yolo Read: %v", err)
			}
		})
	}
}

func TestGlobalHomeListingAndSearchPolicy(t *testing.T) {
	for _, mode := range []string{"default", "override", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			f := newGlobalHomeFixture(t, mode)
			roots := []string{f.project, f.skills}
			ls := &LSTool{Root: f.home, ReadRoots: roots}
			grep := &GrepTool{Root: f.home, ReadRoots: roots}
			glob := &GlobTool{Root: f.home, ReadRoots: roots}
			for name, run := range map[string]func(context.Context, json.RawMessage) (string, error){"LS": ls.Run, "Grep": grep.Run, "Glob": glob.Run} {
				for _, path := range []string{f.storage, f.other, filepath.Join(f.home, "storage-alias"), filepath.Join(f.project, "foreign-link")} {
					if out, err := run(context.Background(), globalHomeArgs(t, path)); err == nil {
						t.Errorf("%s(%s) exposed blocked directory: %q", name, path, out)
					}
				}
				for _, path := range []string{f.project, f.skills, filepath.Join(f.home, "documents")} {
					if _, err := run(context.Background(), globalHomeArgs(t, path)); err != nil {
						t.Errorf("%s(%s) blocked allowed directory: %v", name, path, err)
					}
				}
			}
			out, err := ls.Run(context.Background(), globalHomeArgs(t, f.home))
			if err != nil || !strings.Contains(out, "ordinary.txt") || strings.Contains(out, "storage-alias") || strings.Contains(out, "foreign-alias") || strings.Contains(out, "real-storage/") {
				t.Errorf("LS home = %q, %v", out, err)
			}
			out, err = ls.Run(context.Background(), globalHomeArgs(t, f.project))
			if err != nil || !strings.Contains(out, "tasks/") || strings.Contains(out, "foreign-") {
				t.Errorf("LS project = %q, %v", out, err)
			}
			for name, run := range map[string]func(context.Context, json.RawMessage) (string, error){"Grep": grep.Run, "Glob": glob.Run} {
				args := globalHomeArgs(t, f.home)
				if name == "Glob" {
					args, _ = json.Marshal(map[string]any{"path": f.home, "pattern": "**"})
				}
				out, err := run(context.Background(), args)
				if err != nil {
					t.Errorf("%s home: %v", name, err)
					continue
				}
				for _, want := range []string{"ordinary.txt", "current.txt", "SKILL.md", "legacy.txt"} {
					if !strings.Contains(out, want) {
						t.Errorf("%s home missing %s: %q", name, want, out)
					}
				}
				for _, blocked := range []string{"foreign", "config.jsonc", "private.txt"} {
					if strings.Contains(out, blocked) {
						t.Errorf("%s home exposed %s: %q", name, blocked, out)
					}
				}
			}
		})
	}
}

func TestGlobalHomeListingPreservesOrdinaryDanglingSymlinks(t *testing.T) {
	f := newGlobalHomeFixture(t, "default")
	workspace := filepath.Join(f.home, "documents")
	for name, target := range map[string]string{
		"ordinary-dangling":  filepath.Join(workspace, "missing.txt"),
		"protected-dangling": filepath.Join(f.storage, "missing.txt"),
	} {
		if err := os.Symlink(target, filepath.Join(workspace, name)); err != nil {
			t.Fatal(err)
		}
	}
	ls := &LSTool{Root: workspace}
	glob := &GlobTool{Root: workspace}
	for name, run := range map[string]func(context.Context, json.RawMessage) (string, error){"LS": ls.Run, "Glob": glob.Run} {
		out, err := run(context.Background(), json.RawMessage(`{"pattern":"**"}`))
		if err != nil || !strings.Contains(out, "ordinary-dangling") || strings.Contains(out, "protected-dangling") {
			t.Errorf("%s changed dangling symlink semantics: %q, %v", name, out, err)
		}
	}
}

func TestGlobalHomeMutationPolicy(t *testing.T) {
	for _, mode := range []string{"default", "override", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			f := newGlobalHomeFixture(t, mode)
			state := NewReadStateStore()
			reader := &ReadTool{Root: f.home, ReadRoots: []string{f.project, f.skills}, ReadState: state, AllowOutsideRoot: true}
			writer := &WriteTool{Root: f.home, ReadState: state}
			editor := &EditTool{Root: f.home, ReadState: state}
			for _, path := range []string{
				filepath.Join(f.storage, "config.jsonc"),
				filepath.Join(f.project, "tasks", "current.txt"),
				filepath.Join(f.skills, "demo", "SKILL.md"),
				filepath.Join(f.other, "foreign.txt"),
				filepath.Join(f.home, "storage-alias", "config.jsonc"),
			} {
				args := globalHomeArgs(t, path)
				if _, err := reader.Run(context.Background(), args); err != nil {
					t.Fatal(err)
				}
				if _, err := writer.FileMutationTarget(args); err == nil {
					t.Errorf("Write capability allowed %s", path)
				}
				if _, err := editor.FileMutationTarget(args); err == nil {
					t.Errorf("Edit capability allowed %s", path)
				}
				if _, err := writer.Run(context.Background(), args); err == nil {
					t.Errorf("Write allowed global file after yolo Read: %s", path)
				}
				if _, err := editor.Run(context.Background(), args); err == nil {
					t.Errorf("Edit allowed global file after yolo Read: %s", path)
				}
				content, err := os.ReadFile(path)
				if err != nil || string(content) != "needle original" {
					t.Errorf("protected file changed: %s, %q, %v", path, content, err)
				}
			}
			for _, root := range []string{f.storage, filepath.Join(f.home, "storage-alias")} {
				path := filepath.Join(root, "missing", "nested", "new.txt")
				if _, err := writer.Run(context.Background(), globalHomeArgs(t, path)); err == nil {
					t.Errorf("Write created missing global path %s", path)
				}
				if _, err := os.Stat(filepath.Join(root, "missing")); !os.IsNotExist(err) {
					t.Errorf("Write created global parents: %v", err)
				}
			}
			for _, path := range []string{filepath.Join(f.home, "documents", "ordinary.txt"), filepath.Join(f.home, "workspace", ".paw", "legacy.txt")} {
				args := globalHomeArgs(t, path)
				if _, err := reader.Run(context.Background(), args); err != nil {
					t.Fatal(err)
				}
				if _, err := editor.Run(context.Background(), args); err != nil {
					t.Errorf("ordinary Edit(%s): %v", path, err)
				}
				if _, err := writer.Run(context.Background(), args); err != nil {
					t.Errorf("ordinary Write(%s): %v", path, err)
				}
			}
		})
	}
}
