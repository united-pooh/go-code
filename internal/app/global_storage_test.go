package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"paw/internal/capability/tool"
	"paw/internal/platform/pawpath"
	"paw/internal/storage/session"
)

func TestRuntimeFromHomeIsolatesGlobalStorage(t *testing.T) {
	for _, worker := range []bool{false, true} {
		for _, override := range []bool{false, true} {
			t.Run(fmt.Sprintf("worker=%t/override=%t", worker, override), func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("USERPROFILE", home)
				t.Setenv("PAW_CONFIG_HOME", "")
				if override {
					t.Setenv("PAW_CONFIG_HOME", filepath.Join(home, "state", "paw"))
				}
				t.Setenv("PAW_STORAGE_RUNTIME_KEY", "synthetic")
				storage, err := pawpath.Home()
				if err != nil {
					t.Fatal(err)
				}
				project, err := pawpath.ProjectDir(home)
				if err != nil {
					t.Fatal(err)
				}
				other, err := pawpath.ProjectDir(filepath.Join(home, "other-workspace"))
				if err != nil {
					t.Fatal(err)
				}
				files := map[string]string{
					filepath.Join(storage, "config.jsonc"):               `{"schemaVersion":2,"activeModel":"demo/chat","providers":{"demo":{"transport":"openai-compatible","endpoint":"https://example.invalid/v1","auth":{"env":["PAW_STORAGE_RUNTIME_KEY"]}}},"models":{"demo/chat":{"provider":"demo","name":"chat"}}}`,
					filepath.Join(project, "tasks", "current.txt"):       "needle original",
					filepath.Join(storage, "skills", "demo", "SKILL.md"): "needle original",
					filepath.Join(other, "foreign.txt"):                  "needle original",
					filepath.Join(home, "ordinary.txt"):                  "needle original",
				}
				for path, content := range files {
					if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(content), 0600); err != nil {
						t.Fatal(err)
					}
				}
				var registry *tool.Registry
				runtime, err := BuildWorkspaceRuntime(context.Background(), WorkspaceRuntimeOptions{
					Root: home, WorkerContext: WorkerContext{WorkerMode: worker, MCPBroker: emptyMCPBroker{}},
				}, func(value *tool.Registry) error { registry = value; return nil })
				if err != nil {
					t.Fatal(err)
				}
				defer runtime.Close()
				run := func(name string, args map[string]any) (string, error) {
					value, ok := registry.Get(name)
					if !ok {
						t.Fatalf("%s missing", name)
					}
					raw, err := json.Marshal(args)
					if err != nil {
						t.Fatal(err)
					}
					return value.Run(context.Background(), raw)
				}
				for path := range files {
					allowed := path != filepath.Join(other, "foreign.txt") && path != filepath.Join(storage, "config.jsonc")
					if _, err := run("Read", map[string]any{"file_path": path}); (err == nil) != allowed {
						t.Errorf("Read(%s) error=%v, allowed=%t", path, err, allowed)
					}
				}
				for _, name := range []string{"LS", "Grep", "Glob"} {
					if _, err := run(name, map[string]any{"path": other, "pattern": "**"}); err == nil {
						t.Errorf("%s allowed another project", name)
					}
					pattern := "needle"
					if name == "Glob" {
						pattern = "**"
					}
					out, err := run(name, map[string]any{"pattern": pattern})
					if err != nil || !strings.Contains(out, "ordinary.txt") || strings.Contains(out, "foreign.txt") || strings.Contains(out, "config.jsonc") {
						t.Errorf("%s home = %q, %v", name, out, err)
					}
				}
				artifact := filepath.Join(project, "tasks", "current.txt")
				if _, err := run("Write", map[string]any{"file_path": artifact, "content": "changed"}); err == nil {
					t.Error("Write overwrote internal artifact after allowed Read")
				}
				if _, err := run("Edit", map[string]any{"file_path": artifact, "old_string": "original", "new_string": "changed"}); err == nil {
					t.Error("Edit overwrote internal artifact after allowed Read")
				}
				if _, err := run("Write", map[string]any{"file_path": "result.txt", "content": "user output"}); err != nil {
					t.Fatal(err)
				}
				if content, err := os.ReadFile(filepath.Join(home, "result.txt")); err != nil || string(content) != "user output" {
					t.Fatalf("ordinary tool cwd changed: %q, %v", content, err)
				}
			})
		}
	}
}

func TestRuntimeStoresStateGloballyAndCanReadProjectArtifacts(t *testing.T) {
	for _, worker := range []bool{false, true} {
		t.Run(map[bool]string{false: "main", true: "worker"}[worker], func(t *testing.T) {
			home, configHome, workspace := t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("PAW_CONFIG_HOME", configHome)
			t.Setenv("PAW_STORAGE_RUNTIME_KEY", "synthetic")
			raw := []byte(`{"schemaVersion":2,"activeModel":"demo/chat","providers":{"demo":{"transport":"openai-compatible","endpoint":"https://example.invalid/v1","auth":{"env":["PAW_STORAGE_RUNTIME_KEY"]}}},"models":{"demo/chat":{"provider":"demo","name":"chat"}}}`)
			if err := os.WriteFile(filepath.Join(configHome, "config.jsonc"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			var registry *tool.Registry
			runtime, err := BuildWorkspaceRuntime(context.Background(), WorkspaceRuntimeOptions{
				Root: workspace, WorkerContext: WorkerContext{WorkerMode: worker, MCPBroker: emptyMCPBroker{}},
			}, func(value *tool.Registry) error { registry = value; return nil })
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			project, err := pawpath.ProjectDir(workspace)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.Store.Root() != project {
				t.Fatalf("store root = %q, want %q", runtime.Store.Root(), project)
			}
			if _, err := runtime.Store.CreateRoot(context.Background(), session.CreateRootRequest{SessionID: runtime.SessionID}); err != nil {
				t.Fatal(err)
			}
			artifact := filepath.Join(project, "tasks", "test", "output.json")
			if err := os.MkdirAll(filepath.Dir(artifact), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(artifact, []byte("global project artifact"), 0600); err != nil {
				t.Fatal(err)
			}
			reader, ok := registry.Get("Read")
			if !ok {
				t.Fatal("Read missing")
			}
			args, _ := json.Marshal(map[string]any{"file_path": artifact})
			result, err := reader.Run(context.Background(), args)
			if err != nil || !strings.Contains(result, "global project artifact") {
				t.Fatalf("read global artifact = %q, %v", result, err)
			}
			otherProject, err := pawpath.ProjectDir(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			otherArtifact := filepath.Join(otherProject, "output.json")
			if err := os.MkdirAll(otherProject, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(otherArtifact, []byte("other workspace"), 0600); err != nil {
				t.Fatal(err)
			}
			args, _ = json.Marshal(map[string]any{"file_path": otherArtifact})
			if _, err := reader.Run(context.Background(), args); err == nil {
				t.Fatal("Read allowed another workspace's internal data")
			}
			writer, ok := registry.Get("Write")
			if !ok {
				t.Fatal("Write missing")
			}
			args, _ = json.Marshal(map[string]any{"file_path": "result.txt", "content": "user output"})
			if _, err := writer.Run(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			if err := runtime.Close(); err != nil {
				t.Fatal(err)
			}
			for _, root := range []string{home, workspace} {
				if _, err := os.Stat(filepath.Join(root, ".paw")); !os.IsNotExist(err) {
					t.Fatalf("unexpected .paw under %s: %v", root, err)
				}
			}
		})
	}
}
