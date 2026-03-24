package memory

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"time"
)

// Store provides repository-pattern access to the Postgres database.
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
	Title     string    `json:"title"`
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
			`INSERT INTO sessions (id, tenant_id, channel, title, user_id, metadata)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			sess.ID, sess.TenantID, sess.Channel, sess.Title, sess.UserID, sess.Metadata,
		)
		return err
	})
}

// UpsertSession creates a session if it doesn't exist, or bumps updated_at if it does.
func (s *Store) UpsertSession(ctx context.Context, sess Session) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO sessions (id, tenant_id, channel, title, user_id, metadata)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT(id) DO UPDATE SET updated_at = NOW()`,
			sess.ID, sess.TenantID, sess.Channel, sess.Title, sess.UserID, sess.Metadata,
		)
		return err
	})
}

// DeleteOldSessions removes sessions older than the given duration.
// Messages and tool_executions cascade automatically.
func (s *Store) DeleteOldSessions(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := time.Now().Add(-olderThan)
	var count int
	err := s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx,
			`DELETE FROM sessions WHERE updated_at < $1`, cutoff)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		count = int(n)
		return nil
	})
	return count, err
}

// ListSessions returns the most recent sessions for tenantID ordered by updated_at descending.
func (s *Store) ListSessions(ctx context.Context, tenantID string, limit, offset int) ([]Session, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.DB().QueryContext(ctx,
		`SELECT id, tenant_id, channel, title, user_id, created_at, updated_at, metadata
		 FROM sessions WHERE tenant_id = $1 ORDER BY updated_at DESC LIMIT $2 OFFSET $3`,
		tenantID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.TenantID, &sess.Channel, &sess.Title, &sess.UserID,
			&sess.CreatedAt, &sess.UpdatedAt, &sess.Metadata); err != nil {
			return nil, fmt.Errorf("scanning session: %w", err)
		}
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

// CountMessages returns the number of messages in a session.
func (s *Store) CountMessages(ctx context.Context, sessionID string) (int, error) {
	var count int
	err := s.pool.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE session_id = $1`, sessionID,
	).Scan(&count)
	return count, err
}

// GetSession retrieves a session by ID scoped to a tenant.
func (s *Store) GetSession(ctx context.Context, tenantID, id string) (Session, error) {
	var sess Session
	err := s.pool.DB().QueryRowContext(ctx,
		`SELECT id, tenant_id, channel, title, user_id, created_at, updated_at, metadata
		 FROM sessions WHERE id = $1 AND tenant_id = $2`, id, tenantID,
	).Scan(&sess.ID, &sess.TenantID, &sess.Channel, &sess.Title, &sess.UserID,
		&sess.CreatedAt, &sess.UpdatedAt, &sess.Metadata)
	if err != nil {
		return Session{}, fmt.Errorf("getting session %s: %w", id, err)
	}
	return sess, nil
}

// SaveMessage inserts a message into a session.
func (s *Store) SaveMessage(ctx context.Context, msg Message) (int64, error) {
	var id int64
	err := s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`INSERT INTO messages (session_id, role, content, tool_call_id, tool_name, tool_input, token_count)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 RETURNING id`,
			msg.SessionID, msg.Role, msg.Content, msg.ToolCallID, msg.ToolName, msg.ToolInput, msg.TokenCount,
		).Scan(&id)
	})
	return id, err
}

// GetMessages retrieves all messages for a session, ordered by creation time.
func (s *Store) GetMessages(ctx context.Context, sessionID string) ([]Message, error) {
	rows, err := s.pool.DB().QueryContext(ctx,
		`SELECT id, session_id, role, content, tool_call_id, tool_name, tool_input, token_count, created_at
		 FROM messages WHERE session_id = $1 ORDER BY id ASC`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying messages: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content,
			&m.ToolCallID, &m.ToolName, &m.ToolInput, &m.TokenCount, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning message: %w", err)
		}
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
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT(tenant_id, key) DO UPDATE SET
			   value = EXCLUDED.value,
			   embedding = EXCLUDED.embedding,
			   updated_at = NOW()`,
			entry.TenantID, entry.Key, entry.Value, embeddingBytes,
		)
		return err
	})
}

// RecallMemory finds memory entries similar to the query embedding using
// brute-force cosine similarity. Returns up to limit results above the
// minimum score threshold.
func (s *Store) RecallMemory(ctx context.Context, tenantID string, queryEmbedding []float32, limit int, minScore float64) ([]MemoryMatch, error) {
	rows, err := s.pool.DB().QueryContext(ctx,
		`SELECT id, tenant_id, key, value, embedding, created_at, updated_at
		 FROM memory WHERE tenant_id = $1 AND embedding IS NOT NULL`,
		tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying memory: %w", err)
	}
	defer rows.Close()

	var matches []MemoryMatch
	for rows.Next() {
		var entry MemoryEntry
		var embeddingBytes []byte

		if err := rows.Scan(&entry.ID, &entry.TenantID, &entry.Key, &entry.Value,
			&embeddingBytes, &entry.CreatedAt, &entry.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning memory entry: %w", err)
		}

		entry.Embedding = decodeEmbedding(embeddingBytes)
		if entry.Embedding == nil {
			continue
		}

		score := cosineSimilarity(queryEmbedding, entry.Embedding)
		if score >= minScore {
			matches = append(matches, MemoryMatch{MemoryEntry: entry, Score: score})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

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
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO tool_executions (session_id, tool_name, input, output, duration_ms, success)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			exec.SessionID, exec.ToolName, exec.Input, exec.Output, exec.DurationMs, exec.Success,
		)
		return err
	})
}

// GetToolExecutions returns all tool executions for a session.
func (s *Store) GetToolExecutions(ctx context.Context, sessionID string) ([]ToolExecution, error) {
	rows, err := s.pool.DB().QueryContext(ctx,
		`SELECT id, session_id, tool_name, input, output, duration_ms, success, created_at
		 FROM tool_executions WHERE session_id = $1 ORDER BY id ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ToolExecution
	for rows.Next() {
		var e ToolExecution
		if err := rows.Scan(&e.ID, &e.SessionID, &e.ToolName, &e.Input, &e.Output, &e.DurationMs, &e.Success, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListMemory returns all memory entries for a tenant.
func (s *Store) ListMemory(ctx context.Context, tenantID string) ([]MemoryEntry, error) {
	rows, err := s.pool.DB().QueryContext(ctx,
		`SELECT id, tenant_id, key, value, created_at, updated_at
		 FROM memory WHERE tenant_id = $1 ORDER BY key ASC`, tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing memory: %w", err)
	}
	defer rows.Close()

	var entries []MemoryEntry
	for rows.Next() {
		var e MemoryEntry
		if err := rows.Scan(&e.ID, &e.TenantID, &e.Key, &e.Value, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning memory entry: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// DeleteMemory removes a memory entry by tenant and key.
func (s *Store) DeleteMemory(ctx context.Context, tenantID, key string) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`DELETE FROM memory WHERE tenant_id = $1 AND key = $2`,
			tenantID, key,
		)
		return err
	})
}

// Automation represents a scheduled agent run.
type Automation struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	RRule       string     `json:"rrule"`
	StartAt     *time.Time `json:"start_at"`
	EndAt       *time.Time `json:"end_at"`
	Prompt      string     `json:"prompt"`
	SkillNames  []string   `json:"skill_names"`
	StartOffset string     `json:"start_offset"`
	EndOffset   string     `json:"end_offset"`
	Enabled     bool       `json:"enabled"`
	LastRunAt   *time.Time `json:"last_run_at"`
	NextRunAt   *time.Time `json:"next_run_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
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
		return tx.QueryRowContext(ctx,
			`INSERT INTO automations (name, rrule, start_at, end_at, prompt, skill_names, start_offset, end_offset, enabled, next_run_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			 RETURNING id`,
			a.Name, a.RRule, a.StartAt, a.EndAt, a.Prompt, skillNamesArg(a.SkillNames), a.StartOffset, a.EndOffset, a.Enabled, a.NextRunAt,
		).Scan(&id)
	})
	return id, err
}

// ListAutomations returns all automations ordered by name.
func (s *Store) ListAutomations(ctx context.Context) ([]Automation, error) {
	rows, err := s.pool.DB().QueryContext(ctx,
		`SELECT id, name, rrule, start_at, end_at, prompt, skill_names, start_offset, end_offset, enabled, last_run_at, next_run_at, created_at, updated_at
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
	rows, err := s.pool.DB().QueryContext(ctx,
		`SELECT id, name, rrule, start_at, end_at, prompt, skill_names, start_offset, end_offset, enabled, last_run_at, next_run_at, created_at, updated_at
		 FROM automations WHERE id = $1`, id,
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
		_, err := tx.ExecContext(ctx,
			`UPDATE automations SET name=$1, rrule=$2, start_at=$3, end_at=$4, prompt=$5, skill_names=$6, start_offset=$7, end_offset=$8, enabled=$9, next_run_at=$10, updated_at=NOW()
			 WHERE id=$11`,
			a.Name, a.RRule, a.StartAt, a.EndAt, a.Prompt, skillNamesArg(a.SkillNames), a.StartOffset, a.EndOffset, a.Enabled, a.NextRunAt, a.ID,
		)
		return err
	})
}

// DeleteAutomation removes an automation. Runs cascade automatically.
func (s *Store) DeleteAutomation(ctx context.Context, id int64) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM automations WHERE id=$1`, id)
		return err
	})
}

// ListDueAutomations returns enabled automations whose next_run_at is in the past.
func (s *Store) ListDueAutomations(ctx context.Context) ([]Automation, error) {
	rows, err := s.pool.DB().QueryContext(ctx,
		`SELECT id, name, rrule, start_at, end_at, prompt, skill_names, start_offset, end_offset, enabled, last_run_at, next_run_at, created_at, updated_at
		 FROM automations WHERE enabled=TRUE AND next_run_at IS NOT NULL AND next_run_at <= NOW()
		   AND (end_at IS NULL OR end_at > NOW())`,
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
		return tx.QueryRowContext(ctx,
			`INSERT INTO automation_runs (automation_id, status) VALUES ($1, 'running') RETURNING id`,
			automationID,
		).Scan(&id)
	})
	return id, err
}

// FinishAutomationRun marks a run as complete with status, response, and error.
func (s *Store) FinishAutomationRun(ctx context.Context, runID int64, status, response, errMsg string) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE automation_runs SET finished_at=NOW(), status=$1, response=$2, error=$3 WHERE id=$4`,
			status, response, errMsg, runID,
		)
		return err
	})
}

// MarkStaleRunsAsFailed sets any runs still in "running" state to "error".
// Called on startup to clean up runs interrupted by a crash or restart.
func (s *Store) MarkStaleRunsAsFailed(ctx context.Context) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE automation_runs SET finished_at=NOW(), status='error', error='interrupted by restart'
			 WHERE status='running'`,
		)
		return err
	})
}

// UpdateAutomationSchedule records last/next run times after a successful trigger.
func (s *Store) UpdateAutomationSchedule(ctx context.Context, id int64, lastRunAt, nextRunAt time.Time) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE automations SET last_run_at=$1, next_run_at=$2, updated_at=NOW() WHERE id=$3`,
			lastRunAt, nextRunAt, id,
		)
		return err
	})
}

// ListAutomationRuns returns the most recent runs for an automation.
func (s *Store) ListAutomationRuns(ctx context.Context, automationID int64, limit int) ([]AutomationRun, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.DB().QueryContext(ctx,
		`SELECT id, automation_id, started_at, finished_at, status, response, error
		 FROM automation_runs WHERE automation_id=$1 ORDER BY id DESC LIMIT $2`,
		automationID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing automation runs: %w", err)
	}
	defer rows.Close()
	return scanAutomationRuns(rows)
}

// ListAllAutomationRuns returns runs across all automations, newest first.
// If since is non-zero, only runs started at or after that time are returned.
func (s *Store) ListAllAutomationRuns(ctx context.Context, since time.Time, limit int) ([]AutomationRun, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows *sql.Rows
	var err error
	if since.IsZero() {
		rows, err = s.pool.DB().QueryContext(ctx,
			`SELECT id, automation_id, started_at, finished_at, status, response, error
			 FROM automation_runs ORDER BY id DESC LIMIT $1`, limit)
	} else {
		rows, err = s.pool.DB().QueryContext(ctx,
			`SELECT id, automation_id, started_at, finished_at, status, response, error
			 FROM automation_runs WHERE started_at >= $1 ORDER BY id DESC LIMIT $2`,
			since, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("listing all automation runs: %w", err)
	}
	defer rows.Close()
	return scanAutomationRuns(rows)
}

// pgStringArray is a []string that knows how to read/write Postgres TEXT[] literals.
type pgStringArray []string

func (a *pgStringArray) Scan(src any) error {
	if src == nil {
		*a = []string{}
		return nil
	}
	var s string
	switch v := src.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("cannot scan %T into []string", src)
	}
	s = strings.TrimPrefix(strings.TrimSuffix(s, "}"), "{")
	if s == "" {
		*a = []string{}
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		result = append(result, strings.Trim(p, `"`))
	}
	*a = result
	return nil
}

func (a pgStringArray) Value() (driver.Value, error) {
	if len(a) == 0 {
		return "{}", nil
	}
	parts := make([]string, len(a))
	for i, s := range a {
		parts[i] = `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "{" + strings.Join(parts, ",") + "}", nil
}

func scanAutomations(rows *sql.Rows) ([]Automation, error) {
	var list []Automation
	for rows.Next() {
		var a Automation
		var names pgStringArray
		if err := rows.Scan(&a.ID, &a.Name, &a.RRule, &a.StartAt, &a.EndAt, &a.Prompt, &names, &a.StartOffset, &a.EndOffset, &a.Enabled,
			&a.LastRunAt, &a.NextRunAt, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning automation: %w", err)
		}
		a.SkillNames = []string(names)
		if a.SkillNames == nil {
			a.SkillNames = []string{}
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

func skillNamesArg(names []string) any {
	return pgStringArray(names)
}

func scanAutomationRuns(rows *sql.Rows) ([]AutomationRun, error) {
	var runs []AutomationRun
	for rows.Next() {
		var r AutomationRun
		if err := rows.Scan(&r.ID, &r.AutomationID, &r.StartedAt, &r.FinishedAt, &r.Status, &r.Response, &r.Error); err != nil {
			return nil, fmt.Errorf("scanning automation run: %w", err)
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
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

// Persona is a named system prompt with optional display overrides and tags.
type Persona struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Prompt        string    `json:"prompt"`
	Username      string    `json:"username"`
	AvatarURL     string    `json:"avatar_url"`
	Tags          []string  `json:"tags"`
	DiscordRoleID string    `json:"discord_role_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (s *Store) ListPersonas(ctx context.Context) ([]Persona, error) {
	rows, err := s.pool.DB().QueryContext(ctx,
		`SELECT id, name, prompt, username, avatar_url, tags, discord_role_id, created_at, updated_at FROM personas ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Persona
	for rows.Next() {
		var p Persona
		var tags pgStringArray
		if err := rows.Scan(&p.ID, &p.Name, &p.Prompt, &p.Username, &p.AvatarURL, &tags, &p.DiscordRoleID, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Tags = []string(tags)
		if p.Tags == nil {
			p.Tags = []string{}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetPersonaByName(ctx context.Context, name string) (Persona, error) {
	var p Persona
	var tags pgStringArray
	err := s.pool.DB().QueryRowContext(ctx,
		`SELECT id, name, prompt, username, avatar_url, tags, discord_role_id, created_at, updated_at FROM personas WHERE name = $1`, name,
	).Scan(&p.ID, &p.Name, &p.Prompt, &p.Username, &p.AvatarURL, &tags, &p.DiscordRoleID, &p.CreatedAt, &p.UpdatedAt)
	p.Tags = []string(tags)
	if p.Tags == nil {
		p.Tags = []string{}
	}
	return p, err
}

// GetPersonaByUsername returns a persona by its username (the @mention handle).
func (s *Store) GetPersonaByUsername(ctx context.Context, username string) (Persona, error) {
	var p Persona
	var tags pgStringArray
	err := s.pool.DB().QueryRowContext(ctx,
		`SELECT id, name, prompt, username, avatar_url, tags, discord_role_id, created_at, updated_at FROM personas WHERE username = $1`, username,
	).Scan(&p.ID, &p.Name, &p.Prompt, &p.Username, &p.AvatarURL, &tags, &p.DiscordRoleID, &p.CreatedAt, &p.UpdatedAt)
	p.Tags = []string(tags)
	if p.Tags == nil {
		p.Tags = []string{}
	}
	return p, err
}

// GetPersonasByTag returns all personas that have the given tag.
func (s *Store) GetPersonasByTag(ctx context.Context, tag string) ([]Persona, error) {
	rows, err := s.pool.DB().QueryContext(ctx,
		`SELECT id, name, prompt, username, avatar_url, tags, discord_role_id, created_at, updated_at FROM personas WHERE $1 = ANY(tags) ORDER BY name ASC`, tag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Persona
	for rows.Next() {
		var p Persona
		var tags pgStringArray
		if err := rows.Scan(&p.ID, &p.Name, &p.Prompt, &p.Username, &p.AvatarURL, &tags, &p.DiscordRoleID, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Tags = []string(tags)
		if p.Tags == nil {
			p.Tags = []string{}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreatePersona(ctx context.Context, p Persona) (int64, error) {
	var id int64
	if p.Tags == nil {
		p.Tags = []string{}
	}
	err := s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`INSERT INTO personas (name, prompt, username, avatar_url, tags) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			p.Name, p.Prompt, p.Username, p.AvatarURL, pgStringArray(p.Tags),
		).Scan(&id)
	})
	return id, err
}

func (s *Store) UpdatePersona(ctx context.Context, p Persona) error {
	if p.Tags == nil {
		p.Tags = []string{}
	}
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE personas SET name=$1, prompt=$2, username=$3, avatar_url=$4, tags=$5, updated_at=NOW() WHERE id=$6`,
			p.Name, p.Prompt, p.Username, p.AvatarURL, pgStringArray(p.Tags), p.ID)
		return err
	})
}

// SetPersonaDiscordRoleID stores the Discord role ID for a persona.
func (s *Store) SetPersonaDiscordRoleID(ctx context.Context, id int64, roleID string) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE personas SET discord_role_id=$1, updated_at=NOW() WHERE id=$2`, roleID, id)
		return err
	})
}

// GetPersonaByDiscordRoleID returns a persona by its Discord role ID.
func (s *Store) GetPersonaByDiscordRoleID(ctx context.Context, roleID string) (Persona, error) {
	var p Persona
	var tags pgStringArray
	err := s.pool.DB().QueryRowContext(ctx,
		`SELECT id, name, prompt, username, avatar_url, tags, discord_role_id, created_at, updated_at FROM personas WHERE discord_role_id = $1`, roleID,
	).Scan(&p.ID, &p.Name, &p.Prompt, &p.Username, &p.AvatarURL, &tags, &p.DiscordRoleID, &p.CreatedAt, &p.UpdatedAt)
	p.Tags = []string(tags)
	if p.Tags == nil {
		p.Tags = []string{}
	}
	return p, err
}

func (s *Store) DeletePersona(ctx context.Context, id int64) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM personas WHERE id=$1`, id)
		return err
	})
}

// UpsertDiscordTagRole stores a tag → Discord role ID mapping.
func (s *Store) UpsertDiscordTagRole(ctx context.Context, tag, roleID string) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO discord_tag_roles (tag, role_id) VALUES ($1, $2) ON CONFLICT (tag) DO UPDATE SET role_id = $2`,
			tag, roleID)
		return err
	})
}

// GetTagByDiscordRoleID returns the tag name for a Discord role ID.
func (s *Store) GetTagByDiscordRoleID(ctx context.Context, roleID string) (string, error) {
	var tag string
	err := s.pool.DB().QueryRowContext(ctx,
		`SELECT tag FROM discord_tag_roles WHERE role_id = $1`, roleID,
	).Scan(&tag)
	return tag, err
}

// DeleteDiscordTagRole removes a tag → role mapping.
func (s *Store) DeleteDiscordTagRole(ctx context.Context, tag string) (string, error) {
	var roleID string
	err := s.pool.DB().QueryRowContext(ctx,
		`DELETE FROM discord_tag_roles WHERE tag = $1 RETURNING role_id`, tag,
	).Scan(&roleID)
	return roleID, err
}

// ListDiscordTagRoles returns all tag → role ID mappings.
func (s *Store) ListDiscordTagRoles(ctx context.Context) (map[string]string, error) {
	rows, err := s.pool.DB().QueryContext(ctx, `SELECT tag, role_id FROM discord_tag_roles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var tag, roleID string
		if err := rows.Scan(&tag, &roleID); err != nil {
			return nil, err
		}
		out[tag] = roleID
	}
	return out, rows.Err()
}
