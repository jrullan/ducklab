package bench

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// "Structurally reproducible" (AC-60) is a property of the grid's order, not
// of the numbers. Models vary; a bench whose numbers repeated exactly would be
// measuring nothing.
func TestCellsAreOrderedTheSameEveryTime(t *testing.T) {
	s := Std()
	a := s.Cells([]string{"pato-b", "pato-a"}, []string{"pair", "solo"})
	b := s.Cells([]string{"pato-a", "pato-b"}, []string{"solo", "pair"})

	if len(a) != len(s.Tasks)*4 {
		t.Fatalf("got %d cells, want %d", len(a), len(s.Tasks)*4)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("cell %d differs with input order: %+v vs %+v", i, a[i], b[i])
		}
	}
	// And it is the grid, not a subset.
	seen := map[string]bool{}
	for _, c := range a {
		seen[c.Task+"|"+c.Duckling+"|"+c.Mode] = true
	}
	if len(seen) != len(a) {
		t.Errorf("the grid has duplicates: %d cells, %d distinct", len(a), len(seen))
	}
}

// UNVERIFIED is not a pass. Nothing ran, so nothing is known.
func TestUnverifiedIsNotAPass(t *testing.T) {
	for verdict, want := range map[string]bool{
		"PASSED": true, "FAILED": false, "UNVERIFIED": false, "": false,
	} {
		if got := (Cell{Verdict: verdict}).Passed(); got != want {
			t.Errorf("Cell{%q}.Passed() = %v", verdict, got)
		}
	}
}

// A harness that could not run a cell and a model that could not solve the
// task are different findings. Folding them together blames the model for our
// bug.
func TestErrorsAreCountedSeparatelyFromFailures(t *testing.T) {
	cells := []Cell{
		{Task: "B-001", Duckling: "a", Mode: "solo", Verdict: "PASSED"},
		{Task: "B-002", Duckling: "a", Mode: "solo", Verdict: "FAILED"},
		{Task: "B-003", Duckling: "a", Mode: "solo", Error: "engine died"},
	}
	rows := Aggregate(cells, "mode")
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Runs != 3 || rows[0].Passed != 1 || rows[0].Errors != 1 {
		t.Errorf("row = %+v, want 3 runs, 1 passed, 1 error", rows[0])
	}

	out := Render(Result{Suite: "std", Cells: cells})
	if !strings.Contains(out, "could not be run") {
		t.Errorf("an unrunnable cell was silently a failure:\n%s", out)
	}
}

// The comparison the suite exists for.
func TestRenderComparesEveryModeToSolo(t *testing.T) {
	cells := []Cell{
		{Task: "B-001", Duckling: "a", Mode: "solo", Verdict: "PASSED"},
		{Task: "B-002", Duckling: "a", Mode: "solo", Verdict: "FAILED"},
		{Task: "B-001", Duckling: "a", Mode: "pair", Verdict: "PASSED"},
		{Task: "B-002", Duckling: "a", Mode: "pair", Verdict: "PASSED"},
	}
	out := Render(Result{Suite: "std", SuiteVersion: 1, Cells: cells})
	for _, want := range []string{"solo baseline: 50.0% passed", "pair:", "+50.0 pts"} {
		if !strings.Contains(out, want) {
			t.Errorf("Render missing %q:\n%s", want, out)
		}
	}
}

// Without a solo row every other number is a measurement of nothing in
// particular (05 §4.1), and saying so beats printing a table that looks
// meaningful.
func TestRenderSaysWhenThereIsNoBaseline(t *testing.T) {
	out := Render(Result{Suite: "std", Cells: []Cell{{Mode: "pair", Verdict: "PASSED"}}})
	if !strings.Contains(out, "no solo cells") {
		t.Errorf("a baseline-free bench printed a comparison anyway:\n%s", out)
	}
}

func TestEstimatedTokensAreMarked(t *testing.T) {
	out := Render(Result{Cells: []Cell{{Mode: "solo", Verdict: "PASSED", Tokens: 5000, Estimated: true}}})
	if !strings.Contains(out, "~") {
		t.Errorf("estimated tokens printed as measured:\n%s", out)
	}
}

// Every std task must be self-contained: its own module, its own gate, and a
// test that decides it. A task whose gate a model could satisfy without doing
// the work measures nothing.
func TestStdTasksAreSelfContained(t *testing.T) {
	for _, task := range Std().Tasks {
		if task.Verify.Mode == "" || task.Verify.Tests == "" {
			t.Errorf("%s has no gate: %+v", task.ID, task.Verify)
		}
		if task.Files["go.mod"] == "" {
			t.Errorf("%s has no go.mod, so it is not self-contained", task.ID)
		}
		hasTest := false
		for path := range task.Files {
			if strings.HasSuffix(path, "_test.go") {
				hasTest = true
			}
		}
		if !hasTest && !strings.Contains(task.Body, "test") {
			t.Errorf("%s ships no test and does not ask for one", task.ID)
		}
		if strings.TrimSpace(task.Body) == "" || strings.TrimSpace(task.Title) == "" {
			t.Errorf("%s has no title or body", task.ID)
		}
	}
}

// A result file is read by a person and by a later version of this program.
func TestResultRoundTripsThroughJSON(t *testing.T) {
	in := Result{
		Suite: "std", SuiteVersion: 1, StartedAt: "2026-07-29T00:00:00Z",
		Ducklings: []string{"a"}, Modes: []string{"solo"},
		Cells: []Cell{{Task: "B-001", Duckling: "a", Mode: "solo", Verdict: "PASSED", Tokens: 10}},
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Result
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.SuiteVersion != 1 || len(out.Cells) != 1 || out.Cells[0].Verdict != "PASSED" {
		t.Errorf("round trip lost data: %+v", out)
	}
}

func TestUnknownSuiteNamesTheOneThatExists(t *testing.T) {
	if _, err := Get("nope"); err == nil || !strings.Contains(err.Error(), "std") {
		t.Errorf("err = %v", err)
	}
	if s, err := Get(""); err != nil || s.Name != "std" {
		t.Errorf("the default suite is std: %v %v", s.Name, err)
	}
}

func TestPathIsUnderTheDataDir(t *testing.T) {
	// The argument is xplat.DataDir(), which already ends in "ducklab".
	got := Path("/home/x/.local/share/ducklab", "std", "20260729-120000")
	if got != "/home/x/.local/share/ducklab/bench/std/20260729-120000.json" {
		t.Errorf("Path = %q", got)
	}
}

// Every task must fail its own gate before anything is done to it.
//
// A task whose starting tree is already green measures nothing: a model that
// does absolutely nothing passes it, and the cell reads as a success. This is
// the check that the suite is a suite.
func TestEveryStdTaskStartsRed(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles Go in a temp dir")
	}
	for _, task := range Std().Tasks {
		t.Run(task.ID, func(t *testing.T) {
			dir := t.TempDir()
			for path, content := range task.Files {
				full := filepath.Join(dir, path)
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("go", "test", "./...")
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Errorf("%s starts green, so it measures nothing:\n%s", task.ID, out)
			}
		})
	}
}

// referenceSolutions are the smallest correct answer to each task.
//
// They live in the test, never in the Suite: a solution shipped alongside the
// task would end up in the tree the model reads.
var referenceSolutions = map[string]map[string]string{
	"B-001": {"mathutil.go": "package bench\n\nfunc Double(n int) int { return n * 2 }\n"},
	"B-002": {"strutil.go": "package bench\n\nimport \"strings\"\n\n" +
		"func Initials(name string) string {\n\tvar b strings.Builder\n" +
		"\tfor _, w := range strings.Fields(name) {\n\t\tb.WriteString(strings.ToUpper(w[:1]))\n\t}\n" +
		"\treturn b.String()\n}\n"},
	"B-003": {"chunk.go": "package bench\n\nfunc Chunk(xs []int, size int) [][]int {\n" +
		"\tif size < 1 {\n\t\treturn nil\n\t}\n\tvar out [][]int\n" +
		"\tfor i := 0; i < len(xs); i += size {\n\t\tend := i + size\n" +
		"\t\tif end > len(xs) {\n\t\t\tend = len(xs)\n\t\t}\n\t\tout = append(out, xs[i:end])\n\t}\n\treturn out\n}\n"},
	"B-004": {
		"store.go": "package bench\n\nfunc Get(m map[string]int, k string) int { return m[k] }\n",
		"cache.go": "package bench\n\nfunc Warm(m map[string]int, keys []string) int {\n" +
			"\ttotal := 0\n\tfor _, k := range keys {\n\t\ttotal += Get(m, k)\n\t}\n\treturn total\n}\n",
	},
	"B-005": {"round.go": "package bench\n\nfunc RoundDown(n int) int { return n / 10 * 10 }\n"},
	"B-006": {"lru.go": "package bench\n\nimport \"container/list\"\n\n" +
		"type entry struct {\n\tkey string\n\tvalue int\n}\n\n" +
		"type LRU struct {\n\tcap int\n\tll *list.List\n\titems map[string]*list.Element\n}\n\n" +
		"func NewLRU(capacity int) *LRU {\n" +
		"\treturn &LRU{cap: capacity, ll: list.New(), items: map[string]*list.Element{}}\n}\n\n" +
		"func (c *LRU) Get(key string) (int, bool) {\n" +
		"\tel, ok := c.items[key]\n\tif !ok {\n\t\treturn 0, false\n\t}\n" +
		"\tc.ll.MoveToFront(el)\n\treturn el.Value.(*entry).value, true\n}\n\n" +
		"func (c *LRU) Put(key string, value int) {\n" +
		"\tif el, ok := c.items[key]; ok {\n" +
		"\t\tel.Value.(*entry).value = value\n\t\tc.ll.MoveToFront(el)\n\t\treturn\n\t}\n" +
		"\tc.items[key] = c.ll.PushFront(&entry{key, value})\n" +
		"\tif c.cap > 0 && c.ll.Len() > c.cap {\n" +
		"\t\tback := c.ll.Back()\n\t\tc.ll.Remove(back)\n\t\tdelete(c.items, back.Value.(*entry).key)\n\t}\n}\n"},
	"B-007": {"intervals.go": "package bench\n\nimport \"sort\"\n\n" +
		"func Merge(in [][2]int) [][2]int {\n" +
		"\tif len(in) == 0 {\n\t\treturn nil\n\t}\n" +
		"\txs := make([][2]int, len(in))\n\tcopy(xs, in)\n" +
		"\tsort.Slice(xs, func(i, j int) bool { return xs[i][0] < xs[j][0] })\n" +
		"\tout := [][2]int{xs[0]}\n" +
		"\tfor _, iv := range xs[1:] {\n\t\tlast := &out[len(out)-1]\n" +
		"\t\tif iv[0] <= last[1] {\n\t\t\tif iv[1] > last[1] {\n\t\t\t\tlast[1] = iv[1]\n\t\t\t}\n" +
		"\t\t\tcontinue\n\t\t}\n\t\tout = append(out, iv)\n\t}\n\treturn out\n}\n"},
	"B-008": {"pool.go": "package bench\n\nimport \"sync\"\n\n" +
		"func Map(in []int, workers int, f func(int) int) []int {\n" +
		"\tout := make([]int, len(in))\n" +
		"\tif workers < 1 {\n\t\tworkers = 1\n\t}\n" +
		"\tjobs := make(chan int)\n\tvar wg sync.WaitGroup\n" +
		"\tfor w := 0; w < workers; w++ {\n\t\twg.Add(1)\n" +
		"\t\tgo func() {\n\t\t\tdefer wg.Done()\n" +
		"\t\t\tfor i := range jobs {\n\t\t\t\tout[i] = f(in[i])\n\t\t\t}\n\t\t}()\n\t}\n" +
		"\tfor i := range in {\n\t\tjobs <- i\n\t}\n\tclose(jobs)\n\twg.Wait()\n\treturn out\n}\n"},
	"B-009": {
		"record.go": "package bench\n\n" +
			"type Record struct {\n\tName string\n\tAge  int\n\tCity string\n}\n\n" +
			"var saved []Record\n\nfunc Save(r Record) { saved = append(saved, r) }\n\n" +
			"func Saved() []Record { return saved }\n\nfunc Reset() { saved = nil }\n",
		"importer.go": "package bench\n\nfunc ImportOne() { Save(Record{Name: \"ada\", Age: 36, City: \"london\"}) }\n",
		"seed.go": "package bench\n\nfunc SeedTwo() {\n" +
			"\tSave(Record{Name: \"grace\", Age: 45, City: \"arlington\"})\n" +
			"\tSave(Record{Name: \"edsger\", Age: 42, City: \"austin\"})\n}\n",
	},
}

// A task nobody can pass measures as little as one everybody passes.
//
// This applies the smallest correct answer and asserts the gate goes green —
// so the suite is proved solvable by construction, not by hoping a model
// manages it.
func TestEveryStdTaskIsSolvable(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles Go in a temp dir")
	}
	for _, task := range Std().Tasks {
		t.Run(task.ID, func(t *testing.T) {
			solution, ok := referenceSolutions[task.ID]
			if !ok {
				t.Fatalf("%s has no reference solution, so nothing proves it can be passed", task.ID)
			}
			dir := t.TempDir()
			write := func(files map[string]string) {
				for path, content := range files {
					full := filepath.Join(dir, path)
					if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			write(task.Files)
			write(solution)

			// The task's own gate, not a hardcoded one: B-008 is decided by
			// the race detector, and running it without would pass a solution
			// the real cell would fail.
			cmd := exec.Command("sh", "-c", task.Verify.Tests)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%s is not solvable by its own reference answer:\n%s", task.ID, out)
			}
		})
	}
}
