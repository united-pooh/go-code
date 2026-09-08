package loop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"paw/internal/capability/tool"
	"paw/internal/message"
)

func TestRunTurnStreamMAArtifactFailuresWarnWithoutFailingTurn(t *testing.T) {
	for _, command := range []string{"/streamma", "/streamma-trace"} {
		for _, failure := range []string{"missing-home", "blocked-storage"} {
			t.Run(command+"/"+failure, func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				t.Setenv("PAW_CONFIG_HOME", "")
				if failure == "missing-home" {
					t.Setenv("HOME", "")
				} else {
					blocked := filepath.Join(t.TempDir(), "not-a-directory")
					if err := os.WriteFile(blocked, []byte("blocked"), 0o600); err != nil {
						t.Fatal(err)
					}
					t.Setenv("PAW_CONFIG_HOME", blocked)
				}
				workspace := t.TempDir()
				output := &fakeUI{}
				store := &fakeStore{}
				engine := NewEngineWithInstructionRoot(&fakeModel{}, output, tool.NewRegistry(), store, "artifact-failure-session", workspace)
				engine.SetStreamMATaskRunner(&fakeStreamMATaskRunner{})
				input := command + " 制作一个临时游戏"

				msg, err := engine.RunTurn(context.Background(), input)
				if err != nil {
					t.Fatalf("artifact failure failed successful turn: %v", err)
				}
				if msg.Role != message.RoleAssistant || msg.Content != "final StreamMA answer" {
					t.Fatalf("final message = %#v", msg)
				}
				if got := strings.Join(output.deltas, ""); got != msg.Content || output.doneCount != 1 {
					t.Fatalf("displayed %q with %d completions, want final answer once", got, output.doneCount)
				}
				if len(store.appends) != 1 || len(store.appends[0]) != 2 || store.appends[0][0].Content != input || store.appends[0][1].Content != msg.Content {
					t.Fatalf("committed history = %#v, want user and final answer once", store.appends)
				}
				events := output.systemEvents()
				for _, warning := range []string{"routing rationale write failed:", "runlogger write failed:", "evidence write failed:"} {
					if got := countSystemEvents(events, warning); got != 1 {
						t.Errorf("warning %q count = %d, want 1; events = %#v", warning, got, events)
					}
				}
				if entries, err := os.ReadDir(workspace); err != nil || len(entries) != 0 {
					t.Fatalf("artifact failure polluted workspace: %v, %v", entries, err)
				}
			})
		}
	}
}
