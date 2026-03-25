package memory

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
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

// MemoryEntry represents a key-value memory record.
type MemoryEntry struct {
	ID        int64     `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO memory (tenant_id, key, value)
			 VALUES ($1, $2, $3)
			 ON CONFLICT(tenant_id, key) DO UPDATE SET
			   value = EXCLUDED.value,
			   updated_at = NOW()`,
			entry.TenantID, entry.Key, entry.Value,
		)
		return err
	})
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

// Person is a named system prompt with optional display overrides and tags.
type Person struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Prompt         string    `json:"prompt"`
	Username       string    `json:"username"`
	AvatarURL      string    `json:"avatar_url"`
	AvatarPosition string    `json:"avatar_position"`
	Tags           []string  `json:"tags"`
	DiscordRoleID  string    `json:"discord_role_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (s *Store) ListPeople(ctx context.Context) ([]Person, error) {
	rows, err := s.pool.DB().QueryContext(ctx,
		`SELECT id, name, prompt, username, avatar_url, avatar_position, tags, discord_role_id, created_at, updated_at FROM people ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Person
	for rows.Next() {
		var p Person
		var tags pgStringArray
		if err := rows.Scan(&p.ID, &p.Name, &p.Prompt, &p.Username, &p.AvatarURL, &p.AvatarPosition, &tags, &p.DiscordRoleID, &p.CreatedAt, &p.UpdatedAt); err != nil {
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

func (s *Store) GetPersonByName(ctx context.Context, name string) (Person, error) {
	var p Person
	var tags pgStringArray
	err := s.pool.DB().QueryRowContext(ctx,
		`SELECT id, name, prompt, username, avatar_url, avatar_position, tags, discord_role_id, created_at, updated_at FROM people WHERE name = $1`, name,
	).Scan(&p.ID, &p.Name, &p.Prompt, &p.Username, &p.AvatarURL, &p.AvatarPosition, &tags, &p.DiscordRoleID, &p.CreatedAt, &p.UpdatedAt)
	p.Tags = []string(tags)
	if p.Tags == nil {
		p.Tags = []string{}
	}
	return p, err
}

// GetPersonByUsername returns a person by their username (the @mention handle).
func (s *Store) GetPersonByUsername(ctx context.Context, username string) (Person, error) {
	var p Person
	var tags pgStringArray
	err := s.pool.DB().QueryRowContext(ctx,
		`SELECT id, name, prompt, username, avatar_url, avatar_position, tags, discord_role_id, created_at, updated_at FROM people WHERE username = $1`, username,
	).Scan(&p.ID, &p.Name, &p.Prompt, &p.Username, &p.AvatarURL, &p.AvatarPosition, &tags, &p.DiscordRoleID, &p.CreatedAt, &p.UpdatedAt)
	p.Tags = []string(tags)
	if p.Tags == nil {
		p.Tags = []string{}
	}
	return p, err
}

// GetPeopleByTag returns all people that have the given tag.
func (s *Store) GetPeopleByTag(ctx context.Context, tag string) ([]Person, error) {
	rows, err := s.pool.DB().QueryContext(ctx,
		`SELECT id, name, prompt, username, avatar_url, avatar_position, tags, discord_role_id, created_at, updated_at FROM people WHERE $1 = ANY(tags) ORDER BY name ASC`, tag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Person
	for rows.Next() {
		var p Person
		var tags pgStringArray
		if err := rows.Scan(&p.ID, &p.Name, &p.Prompt, &p.Username, &p.AvatarURL, &p.AvatarPosition, &tags, &p.DiscordRoleID, &p.CreatedAt, &p.UpdatedAt); err != nil {
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

func (s *Store) CreatePerson(ctx context.Context, p Person) (int64, error) {
	var id int64
	if p.Tags == nil {
		p.Tags = []string{}
	}
	err := s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`INSERT INTO people (name, prompt, username, avatar_url, avatar_position, tags)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 RETURNING id`,
			p.Name, p.Prompt, p.Username, p.AvatarURL, p.AvatarPosition, pgStringArray(p.Tags),
		).Scan(&id)
	})
	return id, err
}

func (s *Store) UpdatePerson(ctx context.Context, p Person) error {
	if p.Tags == nil {
		p.Tags = []string{}
	}
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE people SET name=$1, prompt=$2, username=$3, avatar_url=$4, avatar_position=$5, tags=$6, updated_at=NOW() WHERE id=$7`,
			p.Name, p.Prompt, p.Username, p.AvatarURL, p.AvatarPosition, pgStringArray(p.Tags), p.ID)
		return err
	})
}

// SetPersonDiscordRoleID stores the Discord role ID for a person.
func (s *Store) SetPersonDiscordRoleID(ctx context.Context, id int64, roleID string) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE people SET discord_role_id=$1, updated_at=NOW() WHERE id=$2`, roleID, id)
		return err
	})
}

// GetPersonByDiscordRoleID returns a person by their Discord role ID.
func (s *Store) GetPersonByDiscordRoleID(ctx context.Context, roleID string) (Person, error) {
	var p Person
	var tags pgStringArray
	err := s.pool.DB().QueryRowContext(ctx,
		`SELECT id, name, prompt, username, avatar_url, avatar_position, tags, discord_role_id, created_at, updated_at FROM people WHERE discord_role_id = $1`, roleID,
	).Scan(&p.ID, &p.Name, &p.Prompt, &p.Username, &p.AvatarURL, &p.AvatarPosition, &tags, &p.DiscordRoleID, &p.CreatedAt, &p.UpdatedAt)
	p.Tags = []string(tags)
	if p.Tags == nil {
		p.Tags = []string{}
	}
	return p, err
}

func (s *Store) DeletePerson(ctx context.Context, id int64) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM people WHERE id=$1`, id)
		return err
	})
}

// GetSetting returns a value from the settings table. Returns empty string if not found.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.pool.DB().QueryRowContext(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// SetSetting upserts a key-value pair in the settings table.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = $2`,
			key, value)
		return err
	})
}

// Convenience wrappers for backward compat.
func (s *Store) GetSystemPrompt(ctx context.Context) (string, error) { return s.GetSetting(ctx, "system_prompt") }
func (s *Store) SetSystemPrompt(ctx context.Context, prompt string) error { return s.SetSetting(ctx, "system_prompt", prompt) }

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

// GetChannelBinding returns the tag bound to a Discord channel, or "" if none.
func (s *Store) GetChannelBinding(ctx context.Context, channelID string) (string, error) {
	var tag string
	err := s.pool.DB().QueryRowContext(ctx,
		`SELECT tag FROM channel_bindings WHERE channel_id = $1`, channelID,
	).Scan(&tag)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return tag, err
}

// SetChannelBinding binds a Discord channel to a tag.
func (s *Store) SetChannelBinding(ctx context.Context, channelID, tag string) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO channel_bindings (channel_id, tag) VALUES ($1, $2) ON CONFLICT (channel_id) DO UPDATE SET tag = $2`,
			channelID, tag)
		return err
	})
}

// DeleteChannelBinding removes a channel's tag binding.
func (s *Store) DeleteChannelBinding(ctx context.Context, channelID string) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM channel_bindings WHERE channel_id = $1`, channelID)
		return err
	})
}

// ModeKeys represents the per-mode configuration as JSON (API key overrides + default model).
type ModeKeys struct {
	Anthropic  string `json:"anthropic,omitempty"`
	OpenAI     string `json:"openai,omitempty"`
	Gemini     string `json:"gemini,omitempty"`
	OpenRouter string `json:"openrouter,omitempty"`
	Model      string `json:"model,omitempty"`
}

// GetMode returns the keys JSON for a mode.
func (s *Store) GetMode(ctx context.Context, name string) (ModeKeys, error) {
	var raw string
	err := s.pool.DB().QueryRowContext(ctx,
		`SELECT keys FROM modes WHERE name = $1`, name,
	).Scan(&raw)
	if err != nil {
		return ModeKeys{}, err
	}
	var keys ModeKeys
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return ModeKeys{}, err
	}
	return keys, nil
}

// SetMode upserts a mode's keys.
func (s *Store) SetMode(ctx context.Context, name string, keys ModeKeys) error {
	raw, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO modes (name, keys) VALUES ($1, $2) ON CONFLICT (name) DO UPDATE SET keys = $2`,
			name, string(raw))
		return err
	})
}

// DeleteMode removes a custom mode. Built-in modes (default, chat, execute) cannot be deleted.
func (s *Store) DeleteMode(ctx context.Context, name string) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM modes WHERE name = $1 AND name NOT IN ('default', 'chat', 'execute')`, name)
		return err
	})
}

// ListModes returns all mode names and their keys.
func (s *Store) ListModes(ctx context.Context) (map[string]ModeKeys, error) {
	rows, err := s.pool.DB().QueryContext(ctx, `SELECT name, keys FROM modes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]ModeKeys)
	for rows.Next() {
		var name, raw string
		if err := rows.Scan(&name, &raw); err != nil {
			return nil, err
		}
		var keys ModeKeys
		_ = json.Unmarshal([]byte(raw), &keys)
		out[name] = keys
	}
	return out, rows.Err()
}

// GetActiveMode returns the current active mode name.
func (s *Store) GetActiveMode(ctx context.Context) string {
	v, _ := s.GetSetting(ctx, "active_mode")
	if v == "" {
		return "default"
	}
	return v
}

// SetActiveMode sets the current active mode.
func (s *Store) SetActiveMode(ctx context.Context, mode string) error {
	return s.SetSetting(ctx, "active_mode", mode)
}

// UsageRecord represents a single LLM call for cost tracking.
type UsageRecord struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Mode         string `json:"mode"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
}

// SaveUsage inserts a usage record.
func (s *Store) SaveUsage(ctx context.Context, rec UsageRecord) error {
	return s.pool.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO usage_log (provider, model, mode, input_tokens, output_tokens) VALUES ($1, $2, $3, $4, $5)`,
			rec.Provider, rec.Model, rec.Mode, rec.InputTokens, rec.OutputTokens,
		)
		return err
	})
}

// UsageSummary is an aggregated usage row.
type UsageSummary struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Mode         string `json:"mode"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
}

// GetUsageSummary returns usage aggregated by provider, model, and mode.
// If since is non-zero, only records after that time are included.
func (s *Store) GetUsageSummary(ctx context.Context, since time.Time) ([]UsageSummary, error) {
	var rows *sql.Rows
	var err error
	if since.IsZero() {
		rows, err = s.pool.DB().QueryContext(ctx,
			`SELECT provider, model, mode, SUM(input_tokens), SUM(output_tokens)
			 FROM usage_log GROUP BY provider, model, mode ORDER BY provider, model, mode`)
	} else {
		rows, err = s.pool.DB().QueryContext(ctx,
			`SELECT provider, model, mode, SUM(input_tokens), SUM(output_tokens)
			 FROM usage_log WHERE created_at >= $1 GROUP BY provider, model, mode ORDER BY provider, model, mode`, since)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []UsageSummary
	for rows.Next() {
		var r UsageSummary
		if err := rows.Scan(&r.Provider, &r.Model, &r.Mode, &r.InputTokens, &r.OutputTokens); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
