package app

import (
	"context"
	"paw/internal/storage/session"
	"strings"
	"testing"
)

// TestResolveSessionIDWithFlag 验证 -s flag 存在时复用已有 session。
func TestResolveSessionIDWithFlag(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ctx := context.Background()

	// 预先创建一个 session
	id, err := session.GenerateSessionID()
	if err != nil {
		t.Fatalf("GenerateSessionID: %v", err)
	}
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: id}); err != nil {
		t.Fatalf("CreateRoot: %v", err)
	}

	got, err := ResolveRuntimeSessionID(ctx, store, id)
	if err != nil {
		t.Fatalf("resolveSessionID with flag: %v", err)
	}
	if got != id {
		t.Fatalf("resolveSessionID = %q, want %q", got, id)
	}
}

// TestResolveSessionIDWithFlagNotFound 验证 -s flag 指向不存在的 session 时返回错误。
func TestResolveSessionIDWithFlagNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ctx := context.Background()

	_, err = ResolveRuntimeSessionID(ctx, store, "nonexistent-session-id")
	if err == nil {
		t.Fatalf("expected error for nonexistent session, got nil")
	}
	if !strings.Contains(err.Error(), "session 不存在") {
		t.Fatalf("error = %q, want 'session 不存在'", err.Error())
	}
}

// TestResolveSessionIDWithoutFlag 验证无 -s flag 时每次生成新 session ID，但不立即落盘。
func TestResolveSessionIDWithoutFlag(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ctx := context.Background()

	id1, err := ResolveRuntimeSessionID(ctx, store, "")
	if err != nil {
		t.Fatalf("resolveSessionID without flag: %v", err)
	}
	if id1 == "" {
		t.Fatalf("resolveSessionID returned empty session ID")
	}

	// 第二次调用应生成不同的 session ID（每次新建）
	id2, err := ResolveRuntimeSessionID(ctx, store, "")
	if err != nil {
		t.Fatalf("second resolveSessionID without flag: %v", err)
	}
	if id1 == id2 {
		t.Fatalf("expected different session IDs, both = %q", id1)
	}

	for _, id := range []string{id1, id2} {
		exists, err := store.Exists(ctx, id)
		if err != nil {
			t.Fatalf("Exists(%q): %v", id, err)
		}
		if exists {
			t.Fatalf("session %q should not exist before first append", id)
		}
	}
}

// TestResolveSessionID_NoFlag_CreatesNewSessions 验证无 flag 时每次调用生成不同的新会话 ID。
func TestResolveSessionID_NoFlag_CreatesNewSessions(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ctx := context.Background()

	id1, err := ResolveRuntimeSessionID(ctx, store, "")
	if err != nil {
		t.Fatalf("first resolveSessionID: %v", err)
	}
	if id1 == "" {
		t.Fatalf("first resolveSessionID returned empty ID")
	}

	id2, err := ResolveRuntimeSessionID(ctx, store, "")
	if err != nil {
		t.Fatalf("second resolveSessionID: %v", err)
	}
	if id2 == "" {
		t.Fatalf("second resolveSessionID returned empty ID")
	}

	if id1 == id2 {
		t.Fatalf("expected different IDs for two calls without flag, both = %q", id1)
	}
}

// TestResolveSessionID_WithExistingFlag 验证传入已存在的 session ID 时原样返回。
func TestResolveSessionID_WithExistingFlag(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ctx := context.Background()

	id, err := session.GenerateSessionID()
	if err != nil {
		t.Fatalf("GenerateSessionID: %v", err)
	}
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: id}); err != nil {
		t.Fatalf("CreateRoot: %v", err)
	}

	got, err := ResolveRuntimeSessionID(ctx, store, id)
	if err != nil {
		t.Fatalf("resolveSessionID with existing flag: %v", err)
	}
	if got != id {
		t.Fatalf("resolveSessionID = %q, want %q", got, id)
	}
}

// TestResolveSessionID_WithMissingFlag 验证传入不存在的 session ID 时返回错误。
func TestResolveSessionID_WithMissingFlag(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ctx := context.Background()

	_, err = ResolveRuntimeSessionID(ctx, store, "nonexistent-random-session-id")
	if err == nil {
		t.Fatalf("expected error for missing session, got nil")
	}
}
