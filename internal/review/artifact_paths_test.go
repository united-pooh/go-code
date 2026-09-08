package review

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"paw/internal/platform/pawpath"
)

func TestReviewArtifactsSeparateSourceAndStorage(t *testing.T) {
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	parent := t.TempDir()
	for _, root := range []string{filepath.Join(parent, "workspace"), filepath.Join(parent, "workspace ")} {
		writeTestFile(t, filepath.Join(root, "go.mod"), "module fixture\n\ngo 1.25.0\n")
		writeTestFile(t, filepath.Join(root, "internal", "sample", "main.go"), "package sample\nfunc Run() {}\n")
		result, err := WriteReviewArtifacts(context.Background(), ReviewArtifactsOptions{Root: root})
		if err != nil {
			t.Fatal(err)
		}
		project, err := pawpath.ProjectDir(root)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(project, "artifacts", "review")
		if result.ArtifactDir != want || filepath.Dir(result.SummaryPath) != want {
			t.Fatalf("paths = %s / %s, want %s", result.ArtifactDir, result.SummaryPath, want)
		}
		for _, name := range []string{"spec.json", "final-assessment.json"} {
			if _, err := os.Stat(filepath.Join(want, name)); err != nil {
				t.Fatal(err)
			}
		}
		if entries, err := os.ReadDir(root); err != nil || len(entries) != 2 {
			t.Fatalf("source polluted: %v, %v", entries, err)
		}
	}
}

func TestGroupedModuleFamiliesPreserveClassification(t *testing.T) {
	for path, want := range map[string]string{
		"cmd/paw": "cli", "internal/entry/worker": "cli",
		"internal/runtime/sessionactor": "session", "internal/storage/session": "session",
		"internal/runtime/loop": "orchestration", "internal/runtime/streamma": "runtime",
		"internal/runtime/task": "task", "internal/runtime/actor": "package",
		"internal/ui": "ui", "internal/ui/bubble": "ui", "internal/ui/headless": "ui",
		"internal/ui/web": "package", "internal/ui/complete": "package", "internal/ui/theme": "package",
		"internal/capability/tool/file": "tool", "internal/capability/model": "model",
	} {
		if got := moduleFamily(path); got != want {
			t.Errorf("moduleFamily(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestDocumentationMovePreservesReviewScore(t *testing.T) {
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "internal", "sample", "main.go"), "package sample\nfunc Run() {}\n")
	writeTestFile(t, filepath.Join(root, "internal", "localonly", "main.go"), "package localonly\nfunc Run() {}\n")
	writeTestFile(t, filepath.Join(root, "README.md"), "internal/sample\n")
	scores := func() map[string]int {
		t.Helper()
		result, err := WriteReviewArtifacts(context.Background(), ReviewArtifactsOptions{Root: root})
		if err != nil {
			t.Fatal(err)
		}
		var grading TreeGradingArtifact
		readJSONFile(t, filepath.Join(result.ArtifactDir, "tree-grading-individual.json"), &grading)
		out := map[string]int{}
		for _, module := range grading.Modules {
			out[module.Path] = module.Score
		}
		return out
	}
	before := scores()
	writeTestFile(t, filepath.Join(root, "README.md"), "See docs/reference/sample.md\n")
	writeTestFile(t, filepath.Join(root, "docs", "reference", "sample.md"), "internal/sample\n")
	writeTestFile(t, filepath.Join(root, "docs", "local", "notes.md"), "internal/localonly\n")
	after := scores()
	for path, want := range before {
		if got := after[path]; got != want {
			t.Errorf("%s score after moving documentation = %d, want %d", path, got, want)
		}
	}
}
