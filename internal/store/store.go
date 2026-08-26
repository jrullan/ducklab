// Package store manages the SQLite database for a ducklab project.
// Migrations are numbered Go files applied in order inside one transaction.
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite database.
type DB struct {
	db *sql.DB
}

// Open opens or creates the database at the given path.
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return &DB{db: db}, nil
}

// Close closes the database.
func (d *DB) Close() error {
	return d.db.Close()
}

// Migrate runs all pending migrations.
func (d *DB) Migrate() error {
	// Create migrations table
	_, err := d.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	// Get current version
	var version int
	err = d.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version)
	if err != nil {
		return fmt.Errorf("get migration version: %w", err)
	}

	// Apply migrations in order
	migrations := []Migration{
		{Version: 1, Name: "001_init", SQL: migration001},
		{Version: 2, Name: "002_triage_findings", SQL: migration002},
		{Version: 3, Name: "003_test_strategy", SQL: migration003},
		{Version: 4, Name: "004_triage_deliverables", SQL: migration004},
		{Version: 5, Name: "005_triage_proposal", SQL: migration005},
	}

	for _, m := range migrations {
		if m.Version <= version {
			continue
		}
		tx, err := d.db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.Version, err)
		}
		if _, err := tx.Exec(m.SQL); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d (%s): %w", m.Version, m.Name, err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)",
			m.Version, time.Now().UTC().Format(time.RFC3339)); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.Version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.Version, err)
		}
	}
	return nil
}

// Migration is a database migration.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

const migration001 = `
CREATE TABLE project (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  created_at  TEXT NOT NULL
);

CREATE TABLE requirement (
  id          TEXT PRIMARY KEY,
  title       TEXT NOT NULL,
  body        TEXT NOT NULL,
  acceptance  TEXT NOT NULL DEFAULT '',
  priority    TEXT NOT NULL DEFAULT 'should',
  status      TEXT NOT NULL DEFAULT 'draft',
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

CREATE TABLE spec_section (
  id          TEXT PRIMARY KEY,
  title       TEXT NOT NULL,
  body        TEXT NOT NULL,
  status      TEXT NOT NULL DEFAULT 'draft',
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

CREATE TABLE traceability (
  from_kind   TEXT NOT NULL,
  from_id     TEXT NOT NULL,
  to_kind     TEXT NOT NULL,
  to_id       TEXT NOT NULL,
  PRIMARY KEY (from_kind, from_id, to_kind, to_id)
);

CREATE TABLE milestone (
  id          TEXT PRIMARY KEY,
  title       TEXT NOT NULL,
  body        TEXT NOT NULL DEFAULT '',
  position    INTEGER NOT NULL,
  status      TEXT NOT NULL DEFAULT 'open'
);

CREATE TABLE task (
  id           TEXT PRIMARY KEY,
  title        TEXT NOT NULL,
  body         TEXT NOT NULL DEFAULT '',
  milestone_id TEXT REFERENCES milestone(id),
  status       TEXT NOT NULL DEFAULT 'todo',
  complexity   TEXT NOT NULL DEFAULT 'medium',
  role_hint    TEXT NOT NULL DEFAULT '',
  branch       TEXT NOT NULL DEFAULT '',
  depends_on   TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);

CREATE TABLE bug (
  id           TEXT PRIMARY KEY,
  title        TEXT NOT NULL,
  body         TEXT NOT NULL,
  severity     TEXT NOT NULL DEFAULT 'normal',
  status       TEXT NOT NULL DEFAULT 'open',
  duplicate_of TEXT REFERENCES bug(id),
  task_id      TEXT REFERENCES task(id),
  source       TEXT NOT NULL DEFAULT 'manual',
  reporter     TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);

CREATE TABLE run (
  id           TEXT PRIMARY KEY,
  stage        TEXT NOT NULL,
  mode         TEXT NOT NULL,
  task_id      TEXT REFERENCES task(id),
  roster_json  TEXT NOT NULL,
  gate         TEXT NOT NULL,
  status       TEXT NOT NULL,
  verdict      TEXT NOT NULL DEFAULT '',
  accepted     INTEGER NOT NULL DEFAULT 0,
  commit_sha   TEXT NOT NULL DEFAULT '',
  started_at   TEXT NOT NULL,
  ended_at     TEXT NOT NULL DEFAULT '',
  wallclock_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE llm_call (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id        TEXT NOT NULL REFERENCES run(id),
  seq           INTEGER NOT NULL,
  duckling      TEXT NOT NULL,
  role          TEXT NOT NULL,
  provider      TEXT NOT NULL,
  model         TEXT NOT NULL,
  prompt_tokens     INTEGER NOT NULL DEFAULT 0,
  completion_tokens INTEGER NOT NULL DEFAULT 0,
  cost_usd      REAL NOT NULL DEFAULT 0,
  latency_ms    INTEGER NOT NULL DEFAULT 0,
  finish_reason TEXT NOT NULL DEFAULT '',
  error         TEXT NOT NULL DEFAULT ''
);

CREATE TABLE release (
  version     TEXT PRIMARY KEY,
  notes       TEXT NOT NULL DEFAULT '',
  commit_sha  TEXT NOT NULL DEFAULT '',
  deployed_at TEXT NOT NULL DEFAULT '',
  channel     TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_task_status ON task(status);
CREATE INDEX idx_bug_status  ON bug(status);
CREATE INDEX idx_run_task    ON run(task_id);
CREATE INDEX idx_llm_run     ON llm_call(run_id, seq);
CREATE INDEX idx_trace_to    ON traceability(to_kind, to_id);
`

// NextSequence returns the next sequence number for a prefix (e.g. "REQ", "T", "B").
func (d *DB) NextSequence(table, prefix string) (int, error) {
	var maxNum int
	err := d.db.QueryRow(fmt.Sprintf(
		"SELECT COALESCE(MAX(CAST(SUBSTR(id, ?) AS INTEGER)), 0) FROM %s WHERE id LIKE ?",
		table), len(prefix)+2, prefix+"-%").Scan(&maxNum)
	if err != nil {
		return 0, fmt.Errorf("next sequence for %s: %w", prefix, err)
	}
	return maxNum + 1, nil
}

// Project represents a project row.
type Project struct {
	ID        string
	Name      string
	CreatedAt string
}

// CreateProject creates the project row.
func (d *DB) CreateProject(p *Project) error {
	_, err := d.db.Exec("INSERT OR REPLACE INTO project (id, name, created_at) VALUES (?, ?, ?)",
		p.ID, p.Name, p.CreatedAt)
	return err
}

// GetProject returns the project row.
func (d *DB) GetProject() (*Project, error) {
	var p Project
	err := d.db.QueryRow("SELECT id, name, created_at FROM project LIMIT 1").Scan(&p.ID, &p.Name, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Task represents a task row.
type Task struct {
	ID          string
	Title       string
	Body        string
	MilestoneID string
	Status      string
	Complexity  string
	RoleHint    string
	Branch      string
	DependsOn   string
	CreatedAt   string
	UpdatedAt   string
	// What the triage worked out. Kept on the bug because promoting it into a
	// task is a separate act, often days later, and the classification is the
	// half of the answer that says where to look.
	Component      string
	SuspectedFiles string // newline-separated, in the order the triager gave them
	TaskTitle      string
	TriageReason   string
	// The triager's judgment on the honest verification for the fix:
	// "test-first" | "build-only" | "" (no recommendation).
	TestStrategy string
	TestReason   string
	// Deliverables is the triager's proposed work contract for the fix
	// task, newline-separated; promotedTaskBody renders it as the
	// **Deliverables:** checklist the implementer reports against.
	Deliverables string
}

// CreateTask creates a task.
func (d *DB) CreateTask(t *Task) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.db.Exec(`INSERT INTO task (id, title, body, milestone_id, status, complexity, role_hint, branch, depends_on, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Title, t.Body, t.MilestoneID, t.Status, t.Complexity, t.RoleHint, t.Branch, t.DependsOn, now, now)
	return err
}

// SetTaskStatus records task completion needed by bug promotion's all-portions gate.
func (d *DB) SetTaskStatus(id, status string) error {
	_, err := d.db.Exec(`UPDATE task SET status = ?, updated_at = ? WHERE id = ?`, status, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// GetTask returns a task by ID.
func (d *DB) GetTask(id string) (*Task, error) {
	var t Task
	err := d.db.QueryRow(`SELECT id, title, body, milestone_id, status, complexity, role_hint, branch, depends_on, created_at, updated_at
		FROM task WHERE id = ?`, id).Scan(
		&t.ID, &t.Title, &t.Body, &t.MilestoneID, &t.Status, &t.Complexity, &t.RoleHint, &t.Branch, &t.DependsOn, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTasks returns all tasks.
func (d *DB) ListTasks() ([]*Task, error) {
	rows, err := d.db.Query(`SELECT id, title, body, milestone_id, status, complexity, role_hint, branch, depends_on, created_at, updated_at
		FROM task ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []*Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Body, &t.MilestoneID, &t.Status, &t.Complexity, &t.RoleHint, &t.Branch, &t.DependsOn, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		tasks = append(tasks, &t)
	}
	return tasks, rows.Err()
}

// Run represents a run row.
type Run struct {
	ID          string
	Stage       string
	Mode        string
	TaskID      string
	RosterJSON  string
	Gate        string
	Status      string
	Verdict     string
	Accepted    bool
	CommitSHA   string
	StartedAt   string
	EndedAt     string
	WallclockMs int64
}

// CreateRun creates a run.
func (d *DB) CreateRun(r *Run) error {
	_, err := d.db.Exec(`INSERT INTO run (id, stage, mode, task_id, roster_json, gate, status, verdict, accepted, commit_sha, started_at, ended_at, wallclock_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Stage, r.Mode, r.TaskID, r.RosterJSON, r.Gate, r.Status, r.Verdict, r.Accepted, r.CommitSHA, r.StartedAt, r.EndedAt, r.WallclockMs)
	return err
}

// UpdateRun updates a run.
func (d *DB) UpdateRun(r *Run) error {
	_, err := d.db.Exec(`UPDATE run SET status = ?, verdict = ?, accepted = ?, commit_sha = ?, ended_at = ?, wallclock_ms = ? WHERE id = ?`,
		r.Status, r.Verdict, r.Accepted, r.CommitSHA, r.EndedAt, r.WallclockMs, r.ID)
	return err
}

// GetRun returns a run by ID.
func (d *DB) GetRun(id string) (*Run, error) {
	var r Run
	err := d.db.QueryRow(`SELECT id, stage, mode, task_id, roster_json, gate, status, verdict, accepted, commit_sha, started_at, ended_at, wallclock_ms
		FROM run WHERE id = ?`, id).Scan(
		&r.ID, &r.Stage, &r.Mode, &r.TaskID, &r.RosterJSON, &r.Gate, &r.Status, &r.Verdict, &r.Accepted, &r.CommitSHA, &r.StartedAt, &r.EndedAt, &r.WallclockMs)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRuns returns all runs.
func (d *DB) ListRuns() ([]*Run, error) {
	rows, err := d.db.Query(`SELECT id, stage, mode, task_id, roster_json, gate, status, verdict, accepted, commit_sha, started_at, ended_at, wallclock_ms
		FROM run ORDER BY started_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []*Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.Stage, &r.Mode, &r.TaskID, &r.RosterJSON, &r.Gate, &r.Status, &r.Verdict, &r.Accepted, &r.CommitSHA, &r.StartedAt, &r.EndedAt, &r.WallclockMs); err != nil {
			return nil, err
		}
		runs = append(runs, &r)
	}
	return runs, rows.Err()
}

// Bug is one report in the operate loop (05 §6).
type Bug struct {
	ID          string
	Title       string
	Body        string
	Severity    string
	Status      string
	DuplicateOf string
	TaskID      string
	Source      string
	Reporter    string
	CreatedAt   string
	UpdatedAt   string
	// What the triage worked out. Kept on the bug because promoting it into a
	// task is a separate act, often days later, and the classification is the
	// half of the answer that says where to look.
	Component      string
	SuspectedFiles string // newline-separated, in the order the triager gave them
	TaskTitle      string
	TriageReason   string
	// The triager's judgment on the honest verification for the fix:
	// "test-first" | "build-only" | "" (no recommendation).
	TestStrategy string
	TestReason   string
	// Deliverables is the triager's proposed work contract for the fix
	// task, newline-separated; promotedTaskBody renders it as the
	// **Deliverables:** checklist the implementer reports against.
	Deliverables string
	// Proposal is the triager's JSON split recommendation, applied only by promote.
	Proposal string
}

// CreateBug inserts a bug.
func (d *DB) CreateBug(b *Bug) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if b.CreatedAt == "" {
		b.CreatedAt = now
	}
	b.UpdatedAt = now
	_, err := d.db.Exec(`INSERT INTO bug (id, title, body, severity, status, duplicate_of, task_id, source, reporter, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.Title, b.Body, b.Severity, b.Status,
		nullable(b.DuplicateOf), nullable(b.TaskID), b.Source, b.Reporter, b.CreatedAt, b.UpdatedAt)
	return err
}

// nullable keeps an empty foreign key out of the column.
//
// duplicate_of and task_id REFERENCE other rows, and "" is not a row. Writing
// the empty string would leave a reference to a bug that does not exist and
// make every later join quietly wrong.
func nullable(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// GetBug returns one bug.
func (d *DB) GetBug(id string) (*Bug, error) {
	var b Bug
	var dup, task sql.NullString
	err := d.db.QueryRow(`SELECT id, title, body, severity, status, duplicate_of, task_id, source, reporter, created_at, updated_at, component, suspected_files, task_title, triage_reason, test_strategy, test_reason, deliverables, proposal
		FROM bug WHERE id = ?`, id).Scan(
		&b.ID, &b.Title, &b.Body, &b.Severity, &b.Status, &dup, &task, &b.Source, &b.Reporter, &b.CreatedAt, &b.UpdatedAt,
		&b.Component, &b.SuspectedFiles, &b.TaskTitle, &b.TriageReason, &b.TestStrategy, &b.TestReason, &b.Deliverables, &b.Proposal)
	if err != nil {
		return nil, err
	}
	b.DuplicateOf, b.TaskID = dup.String, task.String
	return &b, nil
}

// ListBugs returns every bug, oldest first. Ordering for a person is the
// caller's job: urgency is a product decision, not a storage one.
func (d *DB) ListBugs() ([]*Bug, error) {
	rows, err := d.db.Query(`SELECT id, title, body, severity, status, duplicate_of, task_id, source, reporter, created_at, updated_at, component, suspected_files, task_title, triage_reason, test_strategy, test_reason, deliverables, proposal
		FROM bug ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Bug
	for rows.Next() {
		var b Bug
		var dup, task sql.NullString
		if err := rows.Scan(&b.ID, &b.Title, &b.Body, &b.Severity, &b.Status,
			&dup, &task, &b.Source, &b.Reporter, &b.CreatedAt, &b.UpdatedAt,
			&b.Component, &b.SuspectedFiles, &b.TaskTitle, &b.TriageReason, &b.TestStrategy, &b.TestReason, &b.Deliverables, &b.Proposal); err != nil {
			return nil, err
		}
		b.DuplicateOf, b.TaskID = dup.String, task.String
		out = append(out, &b)
	}
	return out, rows.Err()
}

// UpdateBug writes a bug's mutable fields.
func (d *DB) UpdateBug(b *Bug) error {
	b.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	_, err := d.db.Exec(`UPDATE bug SET title = ?, body = ?, severity = ?, status = ?,
		duplicate_of = ?, task_id = ?, updated_at = ?,
		component = ?, suspected_files = ?, task_title = ?, triage_reason = ?, test_strategy = ?, test_reason = ?, deliverables = ?, proposal = ?
		WHERE id = ?`,
		b.Title, b.Body, b.Severity, b.Status,
		nullable(b.DuplicateOf), nullable(b.TaskID), b.UpdatedAt,
		b.Component, b.SuspectedFiles, b.TaskTitle, b.TriageReason, b.TestStrategy, b.TestReason, b.Deliverables, b.Proposal, b.ID)
	return err
}

// AddTrace records an edge in the traceability graph.
//
// Idempotent: the primary key is the whole edge, so recording the same link
// twice is a no-op rather than an error. Promoting a bug that was already
// promoted should not fail on bookkeeping.
func (d *DB) AddTrace(fromKind, fromID, toKind, toID string) error {
	_, err := d.db.Exec(`INSERT OR IGNORE INTO traceability (from_kind, from_id, to_kind, to_id)
		VALUES (?, ?, ?, ?)`, fromKind, fromID, toKind, toID)
	return err
}

// TracesFrom lists the edges leaving a node.
func (d *DB) TracesFrom(kind, id string) ([]string, error) {
	rows, err := d.db.Query(`SELECT to_kind || ':' || to_id FROM traceability
		WHERE from_kind = ? AND from_id = ? ORDER BY to_kind, to_id`, kind, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// What a triage worked out, kept.
//
// The classification lived only in the run's event stream, so promoting a bug
// into a task carried the reporter's prose and nothing else: no component, no
// suspected files, not even the title the triager had proposed. An implementer
// started from "the left edge label does not update" and a 1361-line file, with
// the answer to "where" already computed and thrown away.
const migration002 = `
ALTER TABLE bug ADD COLUMN component TEXT NOT NULL DEFAULT '';
ALTER TABLE bug ADD COLUMN suspected_files TEXT NOT NULL DEFAULT '';
ALTER TABLE bug ADD COLUMN task_title TEXT NOT NULL DEFAULT '';
ALTER TABLE bug ADD COLUMN triage_reason TEXT NOT NULL DEFAULT '';
`

// The triager's judgment on the honest verification for the fix — a forced
// gate test on a visual bug degenerates into grepping the source, and one
// such run burned 8.7M tokens on a datepicker default.
const migration003 = `
ALTER TABLE bug ADD COLUMN test_strategy TEXT NOT NULL DEFAULT '';
ALTER TABLE bug ADD COLUMN test_reason TEXT NOT NULL DEFAULT '';
`

// The triager's proposed work contract for the fix task. Promoted bugs were
// the one door into the build loop whose tasks carried no **Deliverables:**
// checklist — their implementers reported "1/1" on the task as a whole.
const migration004 = `
ALTER TABLE bug ADD COLUMN deliverables TEXT NOT NULL DEFAULT '';
`

const migration005 = `
ALTER TABLE bug ADD COLUMN proposal TEXT NOT NULL DEFAULT '';
`

// DeleteTask removes a task and the traceability edges that name it.
//
// The edges go too: an edge to a task that no longer exists is a break the
// spine check would report forever, against something nobody can fix.
func (d *DB) DeleteTask(id string) error {
	if _, err := d.db.Exec(`DELETE FROM traceability WHERE (from_kind = 'task' AND from_id = ?) OR (to_kind = 'task' AND to_id = ?)`, id, id); err != nil {
		return err
	}
	_, err := d.db.Exec(`DELETE FROM task WHERE id = ?`, id)
	return err
}
