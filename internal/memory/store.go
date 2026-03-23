package memory

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	_ "modernc.org/sqlite"
)

// Store provides repository-pattern access to the SQLite database.
type Store struct {
	pool *Pool
}

// NewStore creates a Store backed by the given Pool.
func NewStore(pool *Pool) *Store {
	return &Store{pool: pool}
}

// Session represents a conversation session.
type Session struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Channel   string    `json:"channel"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Metadata  string    `json:"metadata"`
}

// Message represents a single message in a session.
type Message struct {
	ID         int64     `json:"id"`
	SessionID  string    `json:"session_id"`
	Role       string    `json:"role"`
	Content    string    `json:"content"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	ToolName   string    `json:"tool_name,omitempty"`
	ToolInput  string    `json:"tool_input,omitempty"`
	TokenCount int       `json:"token_count"`
	CreatedAt  time.Time `json:"created_at"`
}

// MemoryEntry represents a key-value memory record with optional embedding.
type MemoryEntry struct {
	ID        int64     `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	Embedding []float32 `json:"embedding,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MemoryMatch is a MemoryEntry with a similarity score from vector recall.
type MemoryMatch struct {
	MemoryEntry
	Score float64 `json:"score"`
}

// CreateSession inserts a new session.
func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO sessions (id, tenant_id, channel, user_id, metadata)
			 VALUES (?, ?, ?, ?, ?)`,
			sess.ID, sess.TenantID, sess.Channel, sess.UserID, sess.Metadata,
		)
		return err
	})
}

// UpsertSession creates a session if it doesn't exist, or bumps updated_at if it does.
func (s *Store) UpsertSession(ctx context.Context, sess Session) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO sessions (id, tenant_id, channel, user_id, metadata)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET updated_at = datetime('now')`,
			sess.ID, sess.TenantID, sess.Channel, sess.UserID, sess.Metadata,
		)
		return err
	})
}

// DeleteOldSessions removes sessions (and their messages/tool_executions) that
// have not been updated within the given duration. Returns the number of sessions deleted.
func (s *Store) DeleteOldSessions(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := time.Now().Add(-olderThan).Format("2006-01-02 15:04:05")
	var count int
	err := s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		// Collect IDs to delete so we can cascade manually (no ON DELETE CASCADE)
		rows, err := tx.QueryContext(ctx,
			`SELECT id FROM sessions WHERE updated_at < ?`, cutoff)
		if err != nil {
			return fmt.Errorf("querying old sessions: %w", err)
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `DELETE FROM tool_executions WHERE session_id = ?`, id); err != nil {
				return fmt.Errorf("deleting tool executions for %s: %w", id, err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE session_id = ?`, id); err != nil {
				return fmt.Errorf("deleting messages for %s: %w", id, err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
				return fmt.Errorf("deleting session %s: %w", id, err)
			}
			count++
		}
		return nil
	})
	return count, err
}

// ListSessions returns the most recent sessions for tenantID ordered by updated_at descending.
func (s *Store) ListSessions(ctx context.Context, tenantID string, limit, offset int) ([]Session, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.ReadDB().QueryContext(ctx,
		`SELECT id, tenant_id, channel, user_id, created_at, updated_at, metadata
		 FROM sessions WHERE tenant_id = ? ORDER BY updated_at DESC LIMIT ? OFFSET ?`,
		tenantID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		var createdAt, updatedAt string
		if err := rows.Scan(&sess.ID, &sess.TenantID, &sess.Channel, &sess.UserID,
			&createdAt, &updatedAt, &sess.Metadata); err != nil {
			return nil, fmt.Errorf("scanning session: %w", err)
		}
		sess.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		sess.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

// CountMessages returns the number of messages in a session.
func (s *Store) CountMessages(ctx context.Context, sessionID string) (int, error) {
	var count int
	err := s.pool.ReadDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE session_id = ?`, sessionID,
	).Scan(&count)
	return count, err
}

// GetSession retrieves a session by ID scoped to a tenant.
func (s *Store) GetSession(ctx context.Context, tenantID, id string) (Session, error) {
	var sess Session
	var createdAt, updatedAt string

	err := s.pool.ReadDB().QueryRowContext(ctx,
		`SELECT id, tenant_id, channel, user_id, created_at, updated_at, metadata
		 FROM sessions WHERE id = ? AND tenant_id = ?`, id, tenantID,
	).Scan(&sess.ID, &sess.TenantID, &sess.Channel, &sess.UserID,
		&createdAt, &updatedAt, &sess.Metadata)
	if err != nil {
		return Session{}, fmt.Errorf("getting session %s: %w", id, err)
	}

	sess.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	sess.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
	return sess, nil
}

// SaveMessage inserts a message into a session.
func (s *Store) SaveMessage(ctx context.Context, msg Message) (int64, error) {
	var id int64
	err := s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx,
			`INSERT INTO messages (session_id, role, content, tool_call_id, tool_name, tool_input, token_count)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			msg.SessionID, msg.Role, msg.Content, msg.ToolCallID, msg.ToolName, msg.ToolInput, msg.TokenCount,
		)
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		return err
	})
	return id, err
}

// GetMessages retrieves all messages for a session, ordered by creation time.
func (s *Store) GetMessages(ctx context.Context, sessionID string) ([]Message, error) {
	rows, err := s.pool.ReadDB().QueryContext(ctx,
		`SELECT id, session_id, role, content, tool_call_id, tool_name, tool_input, token_count, created_at
		 FROM messages WHERE session_id = ? ORDER BY id ASC`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying messages: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		var createdAt string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content,
			&m.ToolCallID, &m.ToolName, &m.ToolInput, &m.TokenCount, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning message: %w", err)
		}
		m.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// StoreMemory inserts or updates a memory entry for a tenant.
func (s *Store) StoreMemory(ctx context.Context, entry MemoryEntry) error {
	var embeddingBytes []byte
	if entry.Embedding != nil {
		embeddingBytes = encodeEmbedding(entry.Embedding)
	}

	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO memory (tenant_id, key, value, embedding)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT(tenant_id, key) DO UPDATE SET
			   value = excluded.value,
			   embedding = excluded.embedding,
			   updated_at = datetime('now')`,
			entry.TenantID, entry.Key, entry.Value, embeddingBytes,
		)
		return err
	})
}

// RecallMemory finds memory entries similar to the query embedding using
// brute-force cosine similarity. Returns up to limit results above the
// minimum score threshold.
func (s *Store) RecallMemory(ctx context.Context, tenantID string, queryEmbedding []float32, limit int, minScore float64) ([]MemoryMatch, error) {
	rows, err := s.pool.ReadDB().QueryContext(ctx,
		`SELECT id, tenant_id, key, value, embedding, created_at, updated_at
		 FROM memory WHERE tenant_id = ? AND embedding IS NOT NULL`,
		tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying memory: %w", err)
	}
	defer rows.Close()

	var matches []MemoryMatch
	for rows.Next() {
		var entry MemoryEntry
		var embeddingJSON []byte
		var createdAt, updatedAt string

		if err := rows.Scan(&entry.ID, &entry.TenantID, &entry.Key, &entry.Value,
			&embeddingJSON, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning memory entry: %w", err)
		}

		entry.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		entry.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)

		entry.Embedding = decodeEmbedding(embeddingJSON)
		if entry.Embedding == nil {
			continue // skip entries with corrupted embeddings
		}

		score := cosineSimilarity(queryEmbedding, entry.Embedding)
		if score >= minScore {
			matches = append(matches, MemoryMatch{MemoryEntry: entry, Score: score})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Sort by score descending
	sortByScoreDesc(matches)

	if len(matches) > limit {
		matches = matches[:limit]
	}

	return matches, nil
}

// ToolExecution records a single tool invocation for audit logging.
type ToolExecution struct {
	ID         int64     `json:"id"`
	SessionID  string    `json:"session_id"`
	ToolName   string    `json:"tool_name"`
	Input      string    `json:"input"`
	Output     string    `json:"output"`
	DurationMs int64     `json:"duration_ms"`
	Success    bool      `json:"success"`
	CreatedAt  time.Time `json:"created_at"`
}

// SaveToolExecution records a tool execution in the audit log.
func (s *Store) SaveToolExecution(ctx context.Context, exec ToolExecution) error {
	successInt := 0
	if exec.Success {
		successInt = 1
	}
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO tool_executions (session_id, tool_name, input, output, duration_ms, success)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			exec.SessionID, exec.ToolName, exec.Input, exec.Output, exec.DurationMs, successInt,
		)
		return err
	})
}

// ListMemory returns all memory entries for a tenant.
func (s *Store) ListMemory(ctx context.Context, tenantID string) ([]MemoryEntry, error) {
	rows, err := s.pool.ReadDB().QueryContext(ctx,
		`SELECT id, tenant_id, key, value, created_at, updated_at
		 FROM memory WHERE tenant_id = ? ORDER BY key ASC`, tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing memory: %w", err)
	}
	defer rows.Close()

	var entries []MemoryEntry
	for rows.Next() {
		var e MemoryEntry
		var createdAt, updatedAt string
		if err := rows.Scan(&e.ID, &e.TenantID, &e.Key, &e.Value, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning memory entry: %w", err)
		}
		e.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		e.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// DeleteMemory removes a memory entry by tenant and key.
func (s *Store) DeleteMemory(ctx context.Context, tenantID, key string) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`DELETE FROM memory WHERE tenant_id = ? AND key = ?`,
			tenantID, key,
		)
		return err
	})
}

// Automation represents a scheduled agent run.
type Automation struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	RRule     string     `json:"rrule"`
	StartAt   *time.Time `json:"start_at"`
	EndAt     *time.Time `json:"end_at"`
	Prompt    string     `json:"prompt"`
	SkillName string     `json:"skill_name"`
	Enabled   bool       `json:"enabled"`
	LastRunAt *time.Time `json:"last_run_at"`
	NextRunAt *time.Time `json:"next_run_at"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// AutomationRun is a single execution record for an automation.
type AutomationRun struct {
	ID           int64      `json:"id"`
	AutomationID int64      `json:"automation_id"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	Status       string     `json:"status"`
	Response     string     `json:"response"`
	Error        string     `json:"error"`
}

// CreateAutomation inserts a new automation and returns its ID.
func (s *Store) CreateAutomation(ctx context.Context, a Automation) (int64, error) {
	var id int64
	err := s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		var nextRunAt, startAt, endAt interface{}
		if a.NextRunAt != nil {
			nextRunAt = a.NextRunAt.UTC().Format("2006-01-02 15:04:05")
		}
		if a.StartAt != nil {
			startAt = a.StartAt.UTC().Format("2006-01-02 15:04:05")
		}
		if a.EndAt != nil {
			endAt = a.EndAt.UTC().Format("2006-01-02 15:04:05")
		}
		result, err := tx.ExecContext(ctx,
			`INSERT INTO automations (name, rrule, start_at, end_at, prompt, skill_name, enabled, next_run_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			a.Name, a.RRule, startAt, endAt, a.Prompt, a.SkillName, boolInt(a.Enabled), nextRunAt,
		)
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		return err
	})
	return id, err
}

// ListAutomations returns all automations ordered by name.
func (s *Store) ListAutomations(ctx context.Context) ([]Automation, error) {
	rows, err := s.pool.ReadDB().QueryContext(ctx,
		`SELECT id, name, rrule, start_at, end_at, prompt, skill_name, enabled, last_run_at, next_run_at, created_at, updated_at
		 FROM automations ORDER BY name ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing automations: %w", err)
	}
	defer rows.Close()
	return scanAutomations(rows)
}

// GetAutomation retrieves a single automation by ID.
func (s *Store) GetAutomation(ctx context.Context, id int64) (Automation, error) {
	rows, err := s.pool.ReadDB().QueryContext(ctx,
		`SELECT id, name, rrule, start_at, end_at, prompt, skill_name, enabled, last_run_at, next_run_at, created_at, updated_at
		 FROM automations WHERE id = ?`, id,
	)
	if err != nil {
		return Automation{}, fmt.Errorf("getting automation: %w", err)
	}
	defer rows.Close()
	list, err := scanAutomations(rows)
	if err != nil {
		return Automation{}, err
	}
	if len(list) == 0 {
		return Automation{}, fmt.Errorf("automation %d not found", id)
	}
	return list[0], nil
}

// UpdateAutomation updates an existing automation's fields.
func (s *Store) UpdateAutomation(ctx context.Context, a Automation) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		var nextRunAt, startAt, endAt interface{}
		if a.NextRunAt != nil {
			nextRunAt = a.NextRunAt.UTC().Format("2006-01-02 15:04:05")
		}
		if a.StartAt != nil {
			startAt = a.StartAt.UTC().Format("2006-01-02 15:04:05")
		}
		if a.EndAt != nil {
			endAt = a.EndAt.UTC().Format("2006-01-02 15:04:05")
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE automations SET name=?, rrule=?, start_at=?, end_at=?, prompt=?, skill_name=?, enabled=?, next_run_at=?, updated_at=datetime('now')
			 WHERE id=?`,
			a.Name, a.RRule, startAt, endAt, a.Prompt, a.SkillName, boolInt(a.Enabled), nextRunAt, a.ID,
		)
		return err
	})
}

// DeleteAutomation removes an automation and its run history.
func (s *Store) DeleteAutomation(ctx context.Context, id int64) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM automation_runs WHERE automation_id=?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM automations WHERE id=?`, id)
		return err
	})
}

// ListDueAutomations returns enabled automations whose next_run_at is in the past.
func (s *Store) ListDueAutomations(ctx context.Context) ([]Automation, error) {
	rows, err := s.pool.ReadDB().QueryContext(ctx,
		`SELECT id, name, rrule, start_at, end_at, prompt, skill_name, enabled, last_run_at, next_run_at, created_at, updated_at
		 FROM automations WHERE enabled=1 AND next_run_at IS NOT NULL AND next_run_at <= datetime('now')
		   AND (end_at IS NULL OR end_at > datetime('now'))`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing due automations: %w", err)
	}
	defer rows.Close()
	return scanAutomations(rows)
}

// StartAutomationRun inserts a running record and returns its ID.
func (s *Store) StartAutomationRun(ctx context.Context, automationID int64) (int64, error) {
	var id int64
	err := s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx,
			`INSERT INTO automation_runs (automation_id, status) VALUES (?, 'running')`, automationID,
		)
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		return err
	})
	return id, err
}

// FinishAutomationRun marks a run as complete with status, response, and error.
func (s *Store) FinishAutomationRun(ctx context.Context, runID int64, status, response, errMsg string) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE automation_runs SET finished_at=datetime('now'), status=?, response=?, error=? WHERE id=?`,
			status, response, errMsg, runID,
		)
		return err
	})
}

// UpdateAutomationSchedule records last/next run times after a successful trigger.
func (s *Store) UpdateAutomationSchedule(ctx context.Context, id int64, lastRunAt, nextRunAt time.Time) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE automations SET last_run_at=?, next_run_at=?, updated_at=datetime('now') WHERE id=?`,
			lastRunAt.UTC().Format("2006-01-02 15:04:05"),
			nextRunAt.UTC().Format("2006-01-02 15:04:05"),
			id,
		)
		return err
	})
}

// ListAutomationRuns returns the most recent runs for an automation.
func (s *Store) ListAutomationRuns(ctx context.Context, automationID int64, limit int) ([]AutomationRun, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.ReadDB().QueryContext(ctx,
		`SELECT id, automation_id, started_at, finished_at, status, response, error
		 FROM automation_runs WHERE automation_id=? ORDER BY id DESC LIMIT ?`,
		automationID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing automation runs: %w", err)
	}
	defer rows.Close()

	var runs []AutomationRun
	for rows.Next() {
		var r AutomationRun
		var startedAt, finishedAt string
		var finishedAtPtr *string
		if err := rows.Scan(&r.ID, &r.AutomationID, &startedAt, &finishedAtPtr, &r.Status, &r.Response, &r.Error); err != nil {
			return nil, fmt.Errorf("scanning automation run: %w", err)
		}
		r.StartedAt, _ = time.Parse("2006-01-02 15:04:05", startedAt)
		if finishedAtPtr != nil {
			t, _ := time.Parse("2006-01-02 15:04:05", *finishedAtPtr)
			r.FinishedAt = &t
		}
		_ = finishedAt
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

func scanAutomations(rows *sql.Rows) ([]Automation, error) {
	var list []Automation
	for rows.Next() {
		var a Automation
		var enabledInt int
		var createdAt, updatedAt string
		var startAt, endAt, lastRunAt, nextRunAt *string
		if err := rows.Scan(&a.ID, &a.Name, &a.RRule, &startAt, &endAt, &a.Prompt, &a.SkillName, &enabledInt,
			&lastRunAt, &nextRunAt, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning automation: %w", err)
		}
		a.Enabled = enabledInt != 0
		a.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		a.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
		if startAt != nil {
			t, _ := time.Parse("2006-01-02 15:04:05", *startAt)
			a.StartAt = &t
		}
		if endAt != nil {
			t, _ := time.Parse("2006-01-02 15:04:05", *endAt)
			a.EndAt = &t
		}
		if lastRunAt != nil {
			t, _ := time.Parse("2006-01-02 15:04:05", *lastRunAt)
			a.LastRunAt = &t
		}
		if nextRunAt != nil {
			t, _ := time.Parse("2006-01-02 15:04:05", *nextRunAt)
			a.NextRunAt = &t
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// cosineSimilarity computes the cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// sortByScoreDesc sorts matches by score descending using insertion sort
// (fine for small result sets from brute-force search).
func sortByScoreDesc(matches []MemoryMatch) {
	for i := 1; i < len(matches); i++ {
		key := matches[i]
		j := i - 1
		for j >= 0 && matches[j].Score < key.Score {
			matches[j+1] = matches[j]
			j--
		}
		matches[j+1] = key
	}
}

// encodeEmbedding serializes a float32 slice to raw little-endian bytes.
// This is ~10x faster to decode than JSON for large embeddings.
func encodeEmbedding(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// decodeEmbedding deserializes raw little-endian bytes to a float32 slice.
func decodeEmbedding(b []byte) []float32 {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}
