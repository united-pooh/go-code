package plan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalPlanDirectoryImportsLegacyWithoutChangingSource(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	oldDir := filepath.Join(root, "docs", "superpowers", "plans")
	newDir := filepath.Join(root, "docs", "local", "plans")
	old := NewFileStore(oldDir)
	doc := PlanDoc{ID: "legacy-plan", Title: "Legacy", Content: "original", SessionID: "session-1"}
	if err := old.Create(ctx, doc); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(oldDir, "legacy-plan.md")
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	store := NewFileStore(newDir)
	got, ok, err := store.Get(ctx, doc.ID)
	if err != nil || !ok || got.Path != filepath.Join(newDir, "legacy-plan.md") {
		t.Fatalf("Get = %#v, %v, %v", got, ok, err)
	}
	got.Content = "updated"
	if err := store.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List(ctx); err != nil {
		t.Fatal(err)
	}
	got, ok, err = store.Get(ctx, doc.ID)
	if err != nil || !ok || got.Content != "updated\n" {
		t.Fatalf("local copy overwritten: %#v, %v, %v", got, ok, err)
	}
	after, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || !info.ModTime().Equal(afterInfo.ModTime()) || info.Mode() != afterInfo.Mode() {
		t.Fatal("legacy source was modified")
	}
	if err := store.Create(ctx, doc); err == nil {
		t.Fatal("duplicate legacy ID accepted")
	}
}

func TestLocalPlanDirectoryRestoresEventProjectionAtNewPath(t *testing.T) {
	ctx := context.Background()
	root, events := t.TempDir(), t.TempDir()
	oldDir := filepath.Join(root, "docs", "superpowers", "plans")
	newDir := filepath.Join(root, "docs", "local", "plans")
	old, err := NewEventStore(oldDir, events)
	if err != nil {
		t.Fatal(err)
	}
	doc := PlanDoc{ID: "active-plan", Title: "Active", Content: "original", SessionID: "session-1"}
	if err := old.Create(ctx, doc); err != nil {
		t.Fatal(err)
	}
	store, err := NewEventStore(newDir, events)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get(ctx, doc.ID)
	if err != nil || !ok || got.Path != filepath.Join(newDir, "active-plan.md") {
		t.Fatalf("restore = %#v, %v, %v", got, ok, err)
	}
	got.Content = "continued"
	if err := store.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkApproved(ctx, doc.ID); err != nil {
		t.Fatal(err)
	}
	got, ok, err = store.Get(ctx, doc.ID)
	if err != nil || !ok || got.Status != PlanApproved || got.Path != filepath.Join(newDir, "active-plan.md") {
		t.Fatalf("approved = %#v, %v, %v", got, ok, err)
	}
}

func TestDefaultPlanDirectoryIsLocal(t *testing.T) {
	runtime := NewRuntime(RuntimeConfig{})
	if got := filepath.Clean(runtime.store.Dir()); got != filepath.Join("docs", "local", "plans") {
		t.Fatalf("default dir = %q", got)
	}
}

func TestLocalPlanMigrationRejectsSymlinkSource(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "docs", "superpowers", "plans")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "private.md")
	if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(legacy, "linked.md")); err != nil {
		t.Fatal(err)
	}
	store := NewFileStore(filepath.Join(root, "docs", "local", "plans"))
	if _, _, err := store.Get(context.Background(), "linked"); err == nil {
		t.Fatal("symlink source accepted")
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), "linked.md")); !os.IsNotExist(err) {
		t.Fatalf("symlink target copied: %v", err)
	}
}

func TestLocalPlanMigrationDoesNotFollowDestinationDirectoryLink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	legacy := NewFileStore(filepath.Join(root, "docs", "superpowers", "plans"))
	if err := legacy.Create(context.Background(), PlanDoc{ID: "private-plan", Content: "private"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "docs", "local")); err != nil {
		t.Fatal(err)
	}
	store := NewFileStore(filepath.Join(root, "docs", "local", "plans"))
	if _, _, err := store.Get(context.Background(), "private-plan"); err == nil {
		t.Fatal("destination directory symlink accepted")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("outside directory changed: %v, %v", entries, err)
	}
}
