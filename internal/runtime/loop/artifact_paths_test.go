package loop

import (
	"os"
	"path/filepath"
	"testing"

	"paw/internal/platform/pawpath"
	"paw/internal/runtime/streamma"
)

func TestStreamMAArtifactsStayOutsideWorkspace(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(map[bool]string{false: "default-home", true: "override-home"}[override], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("PAW_CONFIG_HOME", "")
			if override {
				t.Setenv("PAW_CONFIG_HOME", t.TempDir())
			}
			workspace := t.TempDir()
			t.Chdir(workspace)
			for _, workRoot := range []string{"", workspace, t.TempDir()} {
				engine := &Engine{}
				engine.workRoot = workRoot
				if err := engine.writeStreamMAJSONArtifact("probe.json", map[string]string{"status": "ok"}); err != nil {
					t.Fatal(err)
				}
				if err := engine.writeStreamMARunJSONArtifact("../run:1", "probe.json", nil); err != nil {
					t.Fatal(err)
				}
				if err := engine.appendStreamMAEvidence(streamma.RoutingEvidenceRecord{}); err != nil {
					t.Fatal(err)
				}
				project, err := pawpath.ProjectDir(workRoot)
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"probe.json", "runs/run_1/probe.json", "evidence.jsonl"} {
					if _, err := os.Stat(filepath.Join(project, "artifacts", "streamma-routing", filepath.FromSlash(name))); err != nil {
						t.Fatal(err)
					}
				}
				if workRoot != "" {
					if entries, err := os.ReadDir(workRoot); err != nil || len(entries) != 0 {
						t.Fatalf("workspace polluted: %v, %v", entries, err)
					}
				}
			}
			if entries, err := os.ReadDir(workspace); err != nil || len(entries) != 0 {
				t.Fatalf("cwd polluted: %v, %v", entries, err)
			}
		})
	}
}

func TestStreamMAArtifactPathErrorsDoNotFallBackToWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", "")
	t.Setenv("PAW_CONFIG_HOME", "")
	engine := &Engine{}
	engine.workRoot = root
	if err := engine.writeStreamMAJSONArtifact("probe.json", nil); err == nil {
		t.Fatal("missing home accepted")
	}
	if err := engine.appendStreamMAEvidence(streamma.RoutingEvidenceRecord{}); err == nil {
		t.Fatal("missing home accepted by evidence writer")
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("fallback polluted workspace: %v, %v", entries, err)
	}
}

func TestStreamMAArtifactPathsPreserveWorkspaceWhitespace(t *testing.T) {
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	parent := t.TempDir()
	seen := map[string]bool{}
	for _, name := range []string{"workspace", "workspace "} {
		root := filepath.Join(parent, name)
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		engine := &Engine{}
		engine.workRoot = root
		got, err := engine.streamMAArtifactDir()
		if err != nil {
			t.Fatal(err)
		}
		project, err := pawpath.ProjectDir(root)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(project, filepath.FromSlash(streamma.ArtifactDirectory)); got != want || seen[got] {
			t.Fatalf("workspace %q artifacts = %q, want distinct %q", root, got, want)
		}
		seen[got] = true
	}
}
