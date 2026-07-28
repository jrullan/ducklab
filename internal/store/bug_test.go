package store

import (
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "ducklab.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestBugRoundTrips(t *testing.T) {
	db := openTestDB(t)
	in := &Bug{ID: "B-001", Title: "Login loops", Body: "steps...",
		Severity: "high", Status: "open", Source: "manual", Reporter: "jose"}
	if err := db.CreateBug(in); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetBug("B-001")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != in.Title || got.Severity != "high" || got.Reporter != "jose" {
		t.Errorf("read back %+v", got)
	}
	if got.CreatedAt == "" || got.UpdatedAt == "" {
		t.Error("timestamps were not set")
	}
}

// duplicate_of and task_id REFERENCE other rows, and "" is not a row. Writing
// the empty string would leave a reference to a bug that does not exist and
// make every later join quietly wrong.
func TestABugWithNoDuplicateStoresNullNotEmptyString(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateBug(&Bug{ID: "B-001", Title: "x", Severity: "normal", Status: "open", Source: "manual"}); err != nil {
		t.Fatalf("a bug with no duplicate could not be stored: %v", err)
	}
	var isNull bool
	if err := db.db.QueryRow(`SELECT duplicate_of IS NULL AND task_id IS NULL FROM bug WHERE id = 'B-001'`).Scan(&isNull); err != nil {
		t.Fatal(err)
	}
	if !isNull {
		t.Error("empty foreign keys were written as strings")
	}
	got, err := db.GetBug("B-001")
	if err != nil || got.DuplicateOf != "" || got.TaskID != "" {
		t.Errorf("null read back as %+v (%v)", got, err)
	}
}

func TestUpdateBugChangesStatusAndLinks(t *testing.T) {
	db := openTestDB(t)
	db.CreateBug(&Bug{ID: "B-001", Title: "x", Severity: "normal", Status: "open", Source: "manual"})
	db.CreateBug(&Bug{ID: "B-002", Title: "y", Severity: "normal", Status: "open", Source: "manual"})

	b, _ := db.GetBug("B-002")
	b.Status = "duplicate"
	b.DuplicateOf = "B-001"
	if err := db.UpdateBug(b); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetBug("B-002")
	if got.Status != "duplicate" || got.DuplicateOf != "B-001" {
		t.Errorf("update did not stick: %+v", got)
	}
}

func TestListBugsIsStable(t *testing.T) {
	db := openTestDB(t)
	for _, id := range []string{"B-003", "B-001", "B-002"} {
		db.CreateBug(&Bug{ID: id, Title: id, Severity: "normal", Status: "open", Source: "manual"})
	}
	got, err := db.ListBugs()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != "B-001" || got[2].ID != "B-003" {
		t.Errorf("order = %v", got)
	}
}
