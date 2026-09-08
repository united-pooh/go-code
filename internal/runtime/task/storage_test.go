package task

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"paw/internal/capability/model"
	toolfile "paw/internal/capability/tool/file"
	"paw/internal/platform/pawpath"
	"paw/internal/platform/settings"
	"paw/internal/storage/session"
)

func taskProjectDirForTest(t *testing.T, root string) string {
	t.Helper()
	dir, err := pawpath.ProjectDir(root)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "paw-task-config-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("PAW_CONFIG_HOME", home); err != nil {
		_ = os.RemoveAll(home)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

func TestTaskStorageDoesNotCreateWorkspacePaw(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("PAW_CONFIG_HOME", home)
	registry := newTaskRegistry(root)
	task := TaskSnapshot{ID: "global-task", SessionID: "global-task", Status: TaskCompleted}
	if err := registry.saveTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := registry.saveOutput(context.Background(), task.ID, WorkerResult{Content: "global output"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(registry.outputPath(task.ID), home+string(filepath.Separator)) {
		t.Fatalf("output path %q is outside PAW_CONFIG_HOME", registry.outputPath(task.ID))
	}
	if _, err := os.Stat(filepath.Join(root, ".paw")); !os.IsNotExist(err) {
		t.Fatalf("workspace .paw exists or cannot be checked: %v", err)
	}
}

func TestLegacyTaskMetadataAndOutputAreReadOnly(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	legacyDir := filepath.Join(root, ".paw", "tasks", "legacy-task")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyOutput := filepath.Join(legacyDir, "output.json")
	task := TaskSnapshot{ID: "legacy-task", SessionID: "legacy-task", Status: TaskRunning, OutputPath: legacyOutput, StartedAt: time.Now().UTC()}
	meta, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		filepath.Join(legacyDir, "meta.json"): meta,
		legacyOutput:                          []byte(`{"content":"legacy output"}`),
	}
	for path, data := range files {
		if err := os.WriteFile(path, data, 0o400); err != nil {
			t.Fatal(err)
		}
	}
	registry := newTaskRegistry(root)
	loaded, found, err := registry.loadTask(context.Background(), task.ID)
	if err != nil || !found {
		t.Fatalf("load legacy task = %#v, %v, %v", loaded, found, err)
	}
	if loaded.OutputPath == legacyOutput || strings.HasPrefix(loaded.OutputPath, root+string(filepath.Separator)) {
		t.Fatalf("legacy output was not redirected: %q", loaded.OutputPath)
	}
	output, err := os.ReadFile(loaded.OutputPath)
	if err != nil || string(output) != string(files[legacyOutput]) {
		t.Fatalf("imported output = %q, %v", output, err)
	}
	manager := NewManager(Config{Root: root})
	defer manager.Close()
	interrupted, ok := manager.Status(task.ID)
	if !ok || interrupted.Status != TaskInterrupted || interrupted.OutputPath != loaded.OutputPath {
		t.Fatalf("reconciled legacy task = %#v, found=%v", interrupted, ok)
	}
	for path, want := range files {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(want) {
			t.Fatalf("legacy file changed: %s: %q, %v", path, got, err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o400 {
			t.Fatalf("legacy permissions changed: %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".paw", "actors")); !os.IsNotExist(err) {
		t.Fatalf("legacy actor directory created: %v", err)
	}
}

func TestUnavailableTaskActorsReturnErrors(t *testing.T) {
	var host *taskActorHost
	if _, _, err := host.status(context.Background(), "task"); err == nil {
		t.Error("unavailable actor status returned no error")
	}
	if _, err := host.list(context.Background()); err == nil {
		t.Error("unavailable actor list returned no error")
	}
	if _, err := host.owned(context.Background(), "parent", "turn"); err == nil {
		t.Error("unavailable actor ownership returned no error")
	}
}

func TestTaskManagerUsesGlobalStorageAndScopedReadRoots(t *testing.T) {
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	manager, store, root := newTestManager(t, &recordingModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "answer"}, {Done: true}}}}}, settings.DefaultConfig(), nil)
	result, err := manager.Run(context.Background(), Request{Prompt: "task", ContextMode: settings.ContextModeEmpty})
	if err != nil {
		t.Fatal(err)
	}
	projectDir := taskProjectDirForTest(t, root)
	if want := filepath.Join(projectDir, "tasks", result.SessionID, "output.json"); result.OutputPath != want {
		t.Fatalf("output path = %q, want %q", result.OutputPath, want)
	}
	if result.TranscriptPath != store.TranscriptPath(result.SessionID) || result.TranscriptPath != TranscriptPath(root, result.SessionID) {
		t.Fatalf("unexpected transcript path %q", result.TranscriptPath)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "actors")); err != nil {
		t.Fatalf("global actor storage is missing: %v", err)
	}
	registry := manager.toolRegistry(false)
	readTool, _ := registry.Get("Read")
	reader := readTool.(*toolfile.ReadTool)
	if reader.Root != root || reader.AllowOutsideRoot {
		t.Fatalf("Read tool workspace or access policy changed: %#v", reader)
	}
	for _, readRoot := range reader.ReadRoots {
		if readRoot == os.Getenv("PAW_CONFIG_HOME") {
			t.Fatal("ReadRoots exposes the whole global config home")
		}
	}
	raw, _ := json.Marshal(map[string]string{"file_path": result.OutputPath})
	if output, err := readTool.Run(context.Background(), raw); err != nil || !strings.Contains(output, "answer") {
		t.Fatalf("Read(global output) = %q, %v", output, err)
	}
	other := newTaskRegistry(t.TempDir())
	if err := other.saveOutput(context.Background(), "other", WorkerResult{Content: "private"}); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(map[string]string{"file_path": other.outputPath("other")})
	if _, err := readTool.Run(context.Background(), raw); err == nil {
		t.Fatal("Read accepted another workspace's output")
	}
	writeTool, _ := registry.Get("Write")
	raw, _ = json.Marshal(map[string]string{"file_path": filepath.Join(projectDir, "not-allowed"), "content": "no"})
	if _, err := writeTool.Run(context.Background(), raw); err == nil {
		t.Fatal("Write accepted a global data path")
	}
	for _, name := range []string{"LS", "Grep", "Glob"} {
		item, _ := registry.Get(name)
		var roots []string
		switch item := item.(type) {
		case *toolfile.LSTool:
			roots = item.ReadRoots
		case *toolfile.GrepTool:
			roots = item.ReadRoots
		case *toolfile.GlobTool:
			roots = item.ReadRoots
		}
		found := false
		for _, dir := range roots {
			found = found || dir == projectDir
		}
		if !found {
			t.Errorf("%s cannot read current project data", name)
		}
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".paw")); !os.IsNotExist(err) {
		t.Fatalf("workspace .paw was created: %v", err)
	}
}

func TestTaskRegistryPathErrorsNeverFallBackToWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PAW_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	registry := newTaskRegistry(root)
	if registry.initErr == nil {
		t.Fatal("expected global home resolution error")
	}
	ctx := context.Background()
	if err := registry.saveTask(ctx, TaskSnapshot{ID: "task"}); err == nil {
		t.Error("saveTask accepted unavailable storage")
	}
	if err := registry.saveOutput(ctx, "task", WorkerResult{}); err == nil {
		t.Error("saveOutput accepted unavailable storage")
	}
	if _, _, err := registry.loadTask(ctx, "task"); err == nil {
		t.Error("loadTask accepted unavailable storage")
	}
	if _, err := registry.listTasks(ctx); err == nil {
		t.Error("listTasks accepted unavailable storage")
	}
	if registry.outputPath("task") != "" || registry.metaPath("task") != "" || TranscriptPath(root, "task") != "" {
		t.Fatal("path resolution error returned a fallback path")
	}
	manager := NewManager(Config{Root: root})
	defer manager.Close()
	for name, check := range map[string]func() error{
		"Run":        func() error { _, err := manager.Run(ctx, Request{Prompt: "task"}); return err },
		"Launch":     func() error { _, err := manager.Launch(ctx, Request{Prompt: "task"}); return err },
		"Stream":     func() error { _, err := manager.Stream(ctx, Request{Prompt: "task"}); return err },
		"Stop":       func() error { _, err := manager.Stop(ctx, "task"); return err },
		"WaitAny":    func() error { _, err := manager.WaitAny(ctx, []string{"task"}, 0); return err },
		"TaskStatus": func() error { _, err := NewStatusTool(manager).Run(ctx, nil); return err },
	} {
		if err := check(); err == nil || !strings.Contains(err.Error(), "resolve task storage") {
			t.Errorf("%s error = %v, want storage resolution error", name, err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("workspace changed after path error: %v, %v", entries, err)
	}
}

func TestTaskRegistryEmptyWorkspaceUsesCWD(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	registry := newTaskRegistry("")
	if registry.initErr != nil || registry.root != root {
		t.Fatalf("empty workspace = %#v", registry)
	}
	manager := NewManager(Config{Root: ""})
	defer manager.Close()
	if manager.root != root || manager.registry.projectDir != taskProjectDirForTest(t, root) {
		t.Fatalf("manager workspace = %q, project = %q", manager.root, manager.registry.projectDir)
	}
}

func TestLegacyTaskImportPrefersGlobalMetadataAndOutput(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	registry := newTaskRegistry(root)
	ctx := context.Background()
	legacyDir := filepath.Join(root, ".paw", "tasks", "task")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "meta.json"), []byte(`{"id":"task","status":"running","output_path":"../../wrong-output"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "output.json"), []byte(`{"content":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := registry.saveOutput(ctx, "task", WorkerResult{Content: "new"}); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := registry.loadTask(ctx, "task")
	if err != nil || !ok || loaded.OutputPath != registry.outputPath("task") {
		t.Fatalf("loadTask = %#v, %v, %v", loaded, ok, err)
	}
	data, err := os.ReadFile(loaded.OutputPath)
	if err != nil || !strings.Contains(string(data), "new") {
		t.Fatalf("existing global output overwritten: %q, %v", data, err)
	}
	loaded.Status = TaskCompleted
	if err := registry.saveTask(ctx, loaded); err != nil {
		t.Fatal(err)
	}
	tasks, err := registry.listTasks(ctx)
	if err != nil || len(tasks) != 1 || tasks[0].Status != TaskCompleted {
		t.Fatalf("global metadata not preferred or task duplicated: %#v, %v", tasks, err)
	}
}

func TestTaskManagerInvalidStorageReturnsErrorBeforeCreatingSession(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(home, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PAW_CONFIG_HOME", home)
	manager := NewManager(Config{Root: root, Store: store, Launcher: &blockingLauncher{}})
	defer manager.Close()
	if _, err := manager.Launch(context.Background(), Request{Prompt: "task", SessionID: "must-not-exist"}); err == nil {
		t.Fatal("Launch succeeded with unusable storage")
	}
	if _, err := os.Stat(store.TranscriptPath("must-not-exist")); !os.IsNotExist(err) {
		t.Fatalf("session created despite storage error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".paw")); !os.IsNotExist(err) {
		t.Fatalf("workspace .paw was created: %v", err)
	}
}

func TestTaskRegistryRejectsPathTraversal(t *testing.T) {
	registry := newTaskRegistry(t.TempDir())
	for _, id := range []string{"", ".", "..", "../escape", "nested/task", `nested\task`} {
		if err := registry.saveTask(context.Background(), TaskSnapshot{ID: id}); err == nil {
			t.Errorf("saveTask accepted id %q", id)
		}
		if err := registry.saveOutput(context.Background(), id, WorkerResult{}); err == nil {
			t.Errorf("saveOutput accepted id %q", id)
		}
		if _, _, err := registry.loadTask(context.Background(), id); err == nil {
			t.Errorf("loadTask accepted id %q", id)
		}
		if registry.outputPath(id) != "" || registry.metaPath(id) != "" {
			t.Errorf("invalid id %q returned a storage path", id)
		}
	}
	if _, err := os.Stat(registry.projectDir); !os.IsNotExist(err) {
		t.Fatalf("invalid task id created storage: %v", err)
	}
}

func TestTaskRegistryMissingCWDDoesNotFallBack(t *testing.T) {
	base := t.TempDir()
	cwd := filepath.Join(base, "removed-cwd")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	if err := os.Remove(cwd); err != nil {
		t.Skipf("platform does not allow removing cwd: %v", err)
	}
	if _, err := os.Getwd(); err == nil {
		t.Skip("platform can still resolve a removed cwd")
	}
	for _, workspace := range []string{"", "relative-workspace"} {
		registry := newTaskRegistry(workspace)
		if registry.initErr == nil {
			t.Fatalf("workspace %q unexpectedly resolved", workspace)
		}
		if err := registry.saveTask(context.Background(), TaskSnapshot{ID: "task"}); err == nil {
			t.Fatal("saveTask ignored workspace resolution error")
		}
		if registry.metaPath("task") != "" || registry.outputPath("task") != "" || TranscriptPath(workspace, "task") != "" {
			t.Fatal("workspace resolution error returned fallback paths")
		}
	}
	t.Setenv("PAW_CONFIG_HOME", "relative-config-home")
	registry := newTaskRegistry(base)
	if registry.initErr == nil || registry.outputPath("task") != "" {
		t.Fatal("relative config home ignored cwd resolution error")
	}
}
