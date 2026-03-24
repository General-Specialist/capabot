package memory

import (
	"context"
	"fmt"
	"math"
	"os"
	"sync"
	"testing"
	"time"
)

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("CAPABOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("CAPABOT_TEST_DATABASE_URL not set, skipping Postgres tests")
	}
	return dsn
}

func setupTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := testDSN(t)
	pool, err := NewPool(dsn)
	if err != nil {
		t.Fatalf("creating pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("running migrations: %v", err)
	}

	// Clean tables between tests
	db := pool.DB()
	for _, table := range []string{"automation_runs", "automations", "tool_executions", "messages", "sessions", "memory", "personas"} {
		if _, err := db.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("cleaning table %s: %v", table, err)
		}
	}

	return NewStore(pool)
}

func TestStore_Migration(t *testing.T) {
	dsn := testDSN(t)
	pool, err := NewPool(dsn)
	if err != nil {
		t.Fatalf("creating pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()

	// Apply migrations twice — should be idempotent
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("second migration (idempotent): %v", err)
	}

	var count int
	err = pool.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_versions").Scan(&count)
	if err != nil {
		t.Fatalf("querying schema_versions: %v", err)
	}
	if count < 1 {
		t.Errorf("expected at least 1 schema version, got %d", count)
	}
}

func TestStore_CRUD(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create session
	sess := Session{
		ID:       "sess-1",
		TenantID: "tenant-a",
		Channel:  "http",
		UserID:   "user-1",
		Metadata: `{"key":"value"}`,
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("creating session: %v", err)
	}

	// Read session
	got, err := store.GetSession(ctx, "tenant-a", "sess-1")
	if err != nil {
		t.Fatalf("getting session: %v", err)
	}
	if got.ID != "sess-1" || got.TenantID != "tenant-a" || got.Channel != "http" {
		t.Errorf("unexpected session: %+v", got)
	}

	// Save messages
	msg1 := Message{SessionID: "sess-1", Role: "user", Content: "hello", TokenCount: 5}
	msg2 := Message{SessionID: "sess-1", Role: "assistant", Content: "hi there", TokenCount: 8}

	id1, err := store.SaveMessage(ctx, msg1)
	if err != nil {
		t.Fatalf("saving message 1: %v", err)
	}
	id2, err := store.SaveMessage(ctx, msg2)
	if err != nil {
		t.Fatalf("saving message 2: %v", err)
	}
	if id1 >= id2 {
		t.Errorf("expected id1 < id2, got %d >= %d", id1, id2)
	}

	// Read messages
	messages, err := store.GetMessages(ctx, "sess-1")
	if err != nil {
		t.Fatalf("getting messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[0].Content != "hello" || messages[1].Content != "hi there" {
		t.Errorf("unexpected message contents: %+v", messages)
	}

	// Get nonexistent session
	_, err = store.GetSession(ctx, "tenant-a", "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestStore_CascadeDelete(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create session with messages and tool executions
	if err := store.CreateSession(ctx, Session{
		ID: "sess-cascade", TenantID: "tenant-a", UserID: "user-1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveMessage(ctx, Message{SessionID: "sess-cascade", Role: "user", Content: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveToolExecution(ctx, ToolExecution{SessionID: "sess-cascade", ToolName: "test", Input: "{}", Output: "ok", Success: true}); err != nil {
		t.Fatal(err)
	}

	// Delete session — messages and tool_executions should cascade
	count, err := store.DeleteOldSessions(ctx, 0)
	if err != nil {
		t.Fatalf("deleting sessions: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 deleted, got %d", count)
	}

	// Verify cascade
	msgs, err := store.GetMessages(ctx, "sess-cascade")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after cascade, got %d", len(msgs))
	}
	execs, err := store.GetToolExecutions(ctx, "sess-cascade")
	if err != nil {
		t.Fatal(err)
	}
	if len(execs) != 0 {
		t.Errorf("expected 0 tool executions after cascade, got %d", len(execs))
	}
}

func TestStore_ConcurrentWriters(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.CreateSession(ctx, Session{
		ID: "sess-concurrent", TenantID: "tenant-a", UserID: "user-1",
	}); err != nil {
		t.Fatalf("creating session: %v", err)
	}

	const numWriters = 10
	const msgsPerWriter = 10

	var wg sync.WaitGroup
	errs := make(chan error, numWriters*msgsPerWriter)

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < msgsPerWriter; j++ {
				_, err := store.SaveMessage(ctx, Message{
					SessionID:  "sess-concurrent",
					Role:       "user",
					Content:    fmt.Sprintf("writer-%d msg-%d", writerID, j),
					TokenCount: 5,
				})
				if err != nil {
					errs <- fmt.Errorf("writer %d msg %d: %w", writerID, j, err)
				}
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent write error: %v", err)
	}

	messages, err := store.GetMessages(ctx, "sess-concurrent")
	if err != nil {
		t.Fatalf("getting messages: %v", err)
	}
	expected := numWriters * msgsPerWriter
	if len(messages) != expected {
		t.Errorf("expected %d messages, got %d", expected, len(messages))
	}
}

func TestStore_CosineSimilarity(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	entries := []MemoryEntry{
		{TenantID: "t1", Key: "cat", Value: "cats are fluffy", Embedding: []float32{1, 0, 0, 0}},
		{TenantID: "t1", Key: "dog", Value: "dogs are loyal", Embedding: []float32{0.9, 0.1, 0, 0}},
		{TenantID: "t1", Key: "car", Value: "cars go fast", Embedding: []float32{0, 0, 1, 0}},
		{TenantID: "t1", Key: "bike", Value: "bikes are fun", Embedding: []float32{0, 0, 0.8, 0.2}},
	}
	for _, e := range entries {
		if err := store.StoreMemory(ctx, e); err != nil {
			t.Fatalf("storing memory %s: %v", e.Key, err)
		}
	}

	query := []float32{0.95, 0.05, 0, 0}

	start := time.Now()
	matches, err := store.RecallMemory(ctx, "t1", query, 2, 0.5)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("recalling memory: %v", err)
	}

	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	if matches[0].Key != "cat" {
		t.Errorf("expected first match to be 'cat', got %q", matches[0].Key)
	}
	if matches[1].Key != "dog" {
		t.Errorf("expected second match to be 'dog', got %q", matches[1].Key)
	}
	if matches[0].Score < 0.9 {
		t.Errorf("expected cat score > 0.9, got %f", matches[0].Score)
	}

	if elapsed > 100*time.Millisecond {
		t.Logf("WARNING: cosine similarity took %v", elapsed)
	}
}

func TestStore_MemoryDelete(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.StoreMemory(ctx, MemoryEntry{
		TenantID: "t1", Key: "to-delete", Value: "temp data",
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteMemory(ctx, "t1", "to-delete"); err != nil {
		t.Fatalf("deleting memory: %v", err)
	}

	matches, err := store.RecallMemory(ctx, "t1", []float32{1, 0}, 10, 0.0)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range matches {
		if m.Key == "to-delete" {
			t.Error("deleted entry should not appear in results")
		}
	}
}

func TestCosineSimilarity_Unit(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float64
	}{
		{"identical", []float32{1, 0, 0}, []float32{1, 0, 0}, 1.0},
		{"orthogonal", []float32{1, 0, 0}, []float32{0, 1, 0}, 0.0},
		{"opposite", []float32{1, 0}, []float32{-1, 0}, -1.0},
		{"empty", []float32{}, []float32{}, 0.0},
		{"length mismatch", []float32{1, 0}, []float32{1, 0, 0}, 0.0},
		{"zero vector", []float32{0, 0, 0}, []float32{1, 0, 0}, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cosineSimilarity(tt.a, tt.b)
			if math.Abs(got-tt.want) > 1e-6 {
				t.Errorf("cosineSimilarity(%v, %v) = %f, want %f", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
