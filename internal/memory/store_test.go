package memory

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func setupTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	pool, err := NewPool(dbPath, 4)
	if err != nil {
		t.Fatalf("creating pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("running migrations: %v", err)
	}

	return NewStore(pool)
}

func TestStore_Migration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	pool, err := NewPool(dbPath, 4)
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

	// Verify schema_versions has exactly one entry
	var count int
	err = pool.ReadDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_versions").Scan(&count)
	if err != nil {
		t.Fatalf("querying schema_versions: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 schema version, got %d", count)
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
	got, err := store.GetSession(ctx, "sess-1")
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
	_, err = store.GetSession(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestStore_ConcurrentWriters(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create a session for all writers to use
	if err := store.CreateSession(ctx, Session{
		ID: "sess-concurrent", TenantID: "tenant-a", UserID: "user-1",
	}); err != nil {
		t.Fatalf("creating session: %v", err)
	}

	// 10 goroutines each writing 10 messages simultaneously
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

	// Verify all messages were written
	messages, err := store.GetMessages(ctx, "sess-concurrent")
	if err != nil {
		t.Fatalf("getting messages: %v", err)
	}
	expected := numWriters * msgsPerWriter
	if len(messages) != expected {
		t.Errorf("expected %d messages, got %d", expected, len(messages))
	}
}

func TestStore_PerTenantIsolation(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// Create two separate stores (simulating per-tenant DBs)
	poolA, err := NewPool(filepath.Join(dir, "tenant-a.db"), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer poolA.Close()
	if err := Migrate(ctx, poolA); err != nil {
		t.Fatal(err)
	}
	storeA := NewStore(poolA)

	poolB, err := NewPool(filepath.Join(dir, "tenant-b.db"), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer poolB.Close()
	if err := Migrate(ctx, poolB); err != nil {
		t.Fatal(err)
	}
	storeB := NewStore(poolB)

	// Write to tenant A
	if err := storeA.CreateSession(ctx, Session{
		ID: "sess-a", TenantID: "tenant-a", UserID: "user-a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := storeA.SaveMessage(ctx, Message{
		SessionID: "sess-a", Role: "user", Content: "secret data",
	}); err != nil {
		t.Fatal(err)
	}

	// Tenant B should have no sessions or messages
	_, err = storeB.GetSession(ctx, "sess-a")
	if err == nil {
		t.Error("tenant B should not see tenant A's session")
	}

	msgs, err := storeB.GetMessages(ctx, "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("tenant B should have 0 messages, got %d", len(msgs))
	}
}

func TestStore_CosineSimilarity(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Store entries with embeddings
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

	// Query close to "cat" vector
	query := []float32{0.95, 0.05, 0, 0}

	start := time.Now()
	matches, err := store.RecallMemory(ctx, "t1", query, 2, 0.5)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("recalling memory: %v", err)
	}

	// Should return cat and dog (highest similarity to query)
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	if matches[0].Key != "cat" {
		t.Errorf("expected first match to be 'cat', got %q", matches[0].Key)
	}
	if matches[1].Key != "dog" {
		t.Errorf("expected second match to be 'dog', got %q", matches[1].Key)
	}

	// Verify scores are reasonable
	if matches[0].Score < 0.9 {
		t.Errorf("expected cat score > 0.9, got %f", matches[0].Score)
	}

	// Sub-10ms for 4 vectors is trivially true, but verifies the path works
	if elapsed > 10*time.Millisecond {
		t.Logf("WARNING: cosine similarity took %v (should be sub-10ms for small sets)", elapsed)
	}
}

func TestStore_CosineSimilarity_LargerSet(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Store 5000 entries with random-ish embeddings
	const dim = 128
	const count = 5000

	for i := 0; i < count; i++ {
		emb := make([]float32, dim)
		for j := range emb {
			emb[j] = float32(math.Sin(float64(i*dim+j) * 0.1))
		}
		if err := store.StoreMemory(ctx, MemoryEntry{
			TenantID:  "t1",
			Key:       fmt.Sprintf("entry-%d", i),
			Value:     fmt.Sprintf("value %d", i),
			Embedding: emb,
		}); err != nil {
			t.Fatalf("storing entry %d: %v", i, err)
		}
	}

	// Query
	query := make([]float32, dim)
	for j := range query {
		query[j] = float32(math.Sin(float64(j) * 0.1))
	}

	start := time.Now()
	matches, err := store.RecallMemory(ctx, "t1", query, 5, 0.0)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("recalling memory: %v", err)
	}
	if len(matches) != 5 {
		t.Fatalf("expected 5 matches, got %d", len(matches))
	}

	// Sub-100ms for 5K vectors with 128-dim (includes SQLite read + cosine compute).
	// Pure cosine on 5K is sub-10ms; DB I/O adds overhead.
	if elapsed > 100*time.Millisecond {
		t.Errorf("recall over %d vectors took %v (target: <100ms including DB I/O)", count, elapsed)
	}
	t.Logf("recall over %d vectors took %v", count, elapsed)

	// First match should be entry-0 (identical embedding)
	if matches[0].Key != "entry-0" {
		t.Errorf("expected first match to be entry-0, got %q", matches[0].Key)
	}
	if matches[0].Score < 0.99 {
		t.Errorf("expected near-perfect score for identical vector, got %f", matches[0].Score)
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

	// Verify deletion — recall with nil embedding should return nothing
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

func TestPool_WALMode(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	pool, err := NewPool(dbPath, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var journalMode string
	err = pool.ReadDB().QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Errorf("expected WAL journal mode, got %q", journalMode)
	}
}
