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
}

// CreateTask creates a task.
func (d *DB) CreateTask(t *Task) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.db.Exec(`INSERT INTO task (id, title, body, milestone_id, status, complexity, role_hint, branch, depends_on, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Title, t.Body, t.MilestoneID, t.Status, t.Complexity, t.RoleHint, t.Branch, t.DependsOn, now, now)
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
