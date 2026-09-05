package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimePinsAbsoluteConfigHomeForWorkers(t *testing.T) {
	parent, workspace := t.TempDir(), t.TempDir()
	t.Chdir(parent)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PAW_CONFIG_HOME", "portable")
	runtime, err := BuildWorkspaceRuntime(context.Background(), WorkspaceRuntimeOptions{Root: workspace, AllowIncomplete: true, WorkerContext: WorkerContext{WorkerMode: true, MCPBroker: emptyMCPBroker{}}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	want := "PAW_CONFIG_HOME=" + filepath.Join(parent, "portable")
	// macOS getwd can canonicalize /var to /private/var.
	actualParent, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	want = "PAW_CONFIG_HOME=" + filepath.Join(actualParent, "portable")
	found := false
	for _, value := range runtime.taskLauncher.Env {
		found = found || value == want
	}
	if !found {
		t.Fatalf("worker env=%v, want %q", runtime.taskLauncher.Env, want)
	}
}
