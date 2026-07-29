package bench

import (
	"fmt"

	"github.com/jrullan/ducklab/internal/config"
)

// stdVersion is bumped whenever a task is added, removed or reworded.
//
// Results carry it, so a comparison across versions is visible rather than
// silent. Changing a task's wording changes what is being measured even when
// the code is identical.
const stdVersion = 2

// goVerify is the gate for every Go task here: the tests decide, not a model.
func goVerify() config.Verify {
	return config.Verify{Mode: "tests", Tests: "go test ./...", TimeoutS: 300}
}

// raceVerify adds the race detector.
//
// Used only where the task is about concurrency. A solution that produces the
// right answer by luck is not a solution, and without -race the gate cannot
// tell the difference.
func raceVerify() config.Verify {
	return config.Verify{Mode: "tests", Tests: "go test -race ./...", TimeoutS: 600}
}

// goMod is the module every std task starts from.
const goMod = "module bench\n\ngo 1.24\n"

// Std is the default suite (03 §3.10).
//
// Five tasks, chosen to be small enough that a 30B local model can finish one
// in a minute, and different enough from each other that a model cannot pass
// them all by being good at one thing:
//
//   - B-001 reads a failing test and fixes the code. The commonest real task.
//   - B-002 writes new code against a written spec with no test to lean on.
//   - B-003 is a bug that only shows at a boundary, so a model that pattern
//     matches without reading the test will get it wrong.
//   - B-004 requires touching two files consistently, which is where a model
//     that edits the first thing it finds fails.
//   - B-005 has a deliberately misleading comment. The code is right and the
//     comment is wrong; a model that trusts prose over tests will "fix" the
//     working code and go red.
//
// None of them need the network. A suite task that did would be measuring the
// network.
func Std() Suite {
	return Suite{
		Name:    "std",
		Version: stdVersion,
		Tasks: []Task{
			{
				ID:    "B-001",
				Title: "Make the failing test pass",
				Body: "`go test ./...` fails. Fix `mathutil.go` so it passes.\n" +
					"Do not change the test.",
				Verify: goVerify(),
				Files: map[string]string{
					"go.mod": goMod,
					"mathutil.go": "package bench\n\n" +
						"// Double returns twice its argument.\nfunc Double(n int) int {\n\treturn n + 2\n}\n",
					"mathutil_test.go": "package bench\n\nimport \"testing\"\n\n" +
						"func TestDouble(t *testing.T) {\n" +
						"\tfor _, c := range []struct{ in, want int }{{0, 0}, {3, 6}, {-4, -8}} {\n" +
						"\t\tif got := Double(c.in); got != c.want {\n" +
						"\t\t\tt.Errorf(\"Double(%d) = %d, want %d\", c.in, got, c.want)\n\t\t}\n\t}\n}\n",
				},
			},
			{
				ID:    "B-002",
				Title: "Write a function from a description",
				Body: "Add `strutil.go` with an exported function `Initials(name string) string`.\n\n" +
					"It returns the uppercased first letter of each space-separated word, with\n" +
					"no separator: `\"ada lovelace\"` becomes `\"AL\"`. An empty string returns an\n" +
					"empty string, and runs of spaces do not produce empty initials.\n\n" +
					"The file does not exist yet.",
				Verify: goVerify(),
				Files: map[string]string{
					"go.mod": goMod,
					// The test ships with the task. An earlier draft asked the
					// model to write its own and shipped a placeholder — which
					// made the starting tree green, so a model that did
					// nothing at all scored a pass.
					"strutil_test.go": "package bench\n\nimport \"testing\"\n\n" +
						"func TestInitials(t *testing.T) {\n" +
						"\tfor _, c := range []struct{ in, want string }{\n" +
						"\t\t{\"ada lovelace\", \"AL\"},\n\t\t{\"\", \"\"},\n" +
						"\t\t{\"grace  brewster  hopper\", \"GBH\"},\n\t\t{\"  edsger  \", \"E\"},\n\t} {\n" +
						"\t\tif got := Initials(c.in); got != c.want {\n" +
						"\t\t\tt.Errorf(\"Initials(%q) = %q, want %q\", c.in, got, c.want)\n\t\t}\n\t}\n}\n",
				},
			},
			{
				ID:    "B-003",
				Title: "Fix a boundary the test names",
				Body: "`go test ./...` fails on one case. Fix `chunk.go`.\n" +
					"Read the failing case before changing anything.",
				Verify: goVerify(),
				Files: map[string]string{
					"go.mod": goMod,
					// The loop drops the final partial chunk. It looks right,
					// and every case where the length divides evenly passes —
					// which is what makes it a boundary rather than a typo.
					"chunk.go": "package bench\n\n" +
						"// Chunk splits xs into slices of at most size elements.\n" +
						"func Chunk(xs []int, size int) [][]int {\n" +
						"\tif size < 1 {\n\t\treturn nil\n\t}\n" +
						"\tvar out [][]int\n" +
						"\tfor i := 0; i+size <= len(xs); i += size {\n" +
						"\t\tout = append(out, xs[i:i+size])\n\t}\n" +
						"\treturn out\n}\n",
					"chunk_test.go": "package bench\n\nimport \"reflect\"\nimport \"testing\"\n\n" +
						"func TestChunk(t *testing.T) {\n" +
						"\tif got := Chunk([]int{1, 2, 3, 4}, 2); !reflect.DeepEqual(got, [][]int{{1, 2}, {3, 4}}) {\n" +
						"\t\tt.Errorf(\"even split = %v\", got)\n\t}\n" +
						"\t// The last chunk is short. This is the case that fails.\n" +
						"\tif got := Chunk([]int{1, 2, 3, 4, 5}, 2); !reflect.DeepEqual(got, [][]int{{1, 2}, {3, 4}, {5}}) {\n" +
						"\t\tt.Errorf(\"short final chunk = %v, want [[1 2] [3 4] [5]]\", got)\n\t}\n}\n",
				},
			},
			{
				ID:    "B-004",
				Title: "Rename across two files",
				Body: "Rename the exported function `Fetch` to `Get`, everywhere, so the tests\n" +
					"pass. Both files use it.",
				Verify: goVerify(),
				Files: map[string]string{
					"go.mod": goMod,
					"store.go": "package bench\n\n" +
						"// Fetch returns the value for a key.\nfunc Fetch(m map[string]int, k string) int {\n\treturn m[k]\n}\n",
					"cache.go": "package bench\n\n" +
						"// Warm reads every key once.\nfunc Warm(m map[string]int, keys []string) int {\n" +
						"\ttotal := 0\n\tfor _, k := range keys {\n\t\ttotal += Fetch(m, k)\n\t}\n\treturn total\n}\n",
					"store_test.go": "package bench\n\nimport \"testing\"\n\n" +
						"func TestGet(t *testing.T) {\n\tm := map[string]int{\"a\": 1}\n" +
						"\tif got := Get(m, \"a\"); got != 1 {\n\t\tt.Errorf(\"Get = %d\", got)\n\t}\n" +
						"\tif got := Warm(m, []string{\"a\", \"a\"}); got != 2 {\n\t\tt.Errorf(\"Warm = %d\", got)\n\t}\n}\n",
				},
			},
			{
				ID:    "B-005",
				Title: "Trust the test, not the comment",
				Body: "`go test ./...` fails. Fix it.\n" +
					"Exactly one of the comment and the test describes what this function\n" +
					"should do.",
				Verify: goVerify(),
				Files: map[string]string{
					"go.mod": goMod,
					"round.go": "package bench\n\n" +
						"// RoundDown returns n rounded down to the nearest multiple of 10.\n" +
						"func RoundDown(n int) int {\n\treturn n / 10\n}\n",
					"round_test.go": "package bench\n\nimport \"testing\"\n\n" +
						"func TestRoundDown(t *testing.T) {\n" +
						"\tfor _, c := range []struct{ in, want int }{{0, 0}, {7, 0}, {10, 10}, {19, 10}, {90, 90}} {\n" +
						"\t\tif got := RoundDown(c.in); got != c.want {\n" +
						"\t\t\tt.Errorf(\"RoundDown(%d) = %d, want %d\", c.in, got, c.want)\n\t\t}\n\t}\n}\n",
				},
			},
			// The five above are a floor: a model that fails them cannot be
			// used for anything. They do not discriminate — the first real
			// bench had two very different ducklings both score 5/5, which
			// says the suite was measuring below both their ceilings.
			//
			// These four are where the ceiling is. Each has a correct answer
			// that is short, and a plausible answer that passes some cases.
			{
				ID:    "B-006",
				Title: "Implement an LRU cache",
				Body: "Add `lru.go` with `NewLRU(capacity int) *LRU` and methods\n" +
					"`Get(key string) (int, bool)` and `Put(key string, value int)`.\n\n" +
					"When the cache is full, the least *recently used* entry is evicted. A\n" +
					"Get counts as a use. Putting a key that is already present updates it\n" +
					"and counts as a use rather than adding an entry.",
				Verify: goVerify(),
				Files: map[string]string{
					"go.mod": goMod,
					// The tests separate recency from insertion order, which is
					// the whole difficulty: a queue passes the easy cases.
					"lru_test.go": "package bench\n\nimport \"testing\"\n\n" +
						"func TestLRUEvictsLeastRecentlyUsed(t *testing.T) {\n" +
						"\tc := NewLRU(2)\n\tc.Put(\"a\", 1)\n\tc.Put(\"b\", 2)\n" +
						"\tif v, ok := c.Get(\"a\"); !ok || v != 1 {\n\t\tt.Fatalf(\"Get(a) = %v %v\", v, ok)\n\t}\n" +
						"\tc.Put(\"c\", 3) // b is now least recently used, not a\n" +
						"\tif _, ok := c.Get(\"b\"); ok {\n\t\tt.Error(\"b should have been evicted\")\n\t}\n" +
						"\tif _, ok := c.Get(\"a\"); !ok {\n\t\tt.Error(\"a was used most recently and must survive\")\n\t}\n}\n\n" +
						"func TestLRUUpdateCountsAsUse(t *testing.T) {\n" +
						"\tc := NewLRU(2)\n\tc.Put(\"a\", 1)\n\tc.Put(\"b\", 2)\n\tc.Put(\"a\", 10)\n" +
						"\tc.Put(\"c\", 3)\n" +
						"\tif _, ok := c.Get(\"b\"); ok {\n\t\tt.Error(\"b should have been evicted\")\n\t}\n" +
						"\tif v, _ := c.Get(\"a\"); v != 10 {\n\t\tt.Errorf(\"a = %d, want the updated value\", v)\n\t}\n}\n\n" +
						"func TestLRUCapacityOne(t *testing.T) {\n" +
						"\tc := NewLRU(1)\n\tc.Put(\"a\", 1)\n\tc.Put(\"b\", 2)\n" +
						"\tif _, ok := c.Get(\"a\"); ok {\n\t\tt.Error(\"a should have been evicted\")\n\t}\n" +
						"\tif v, ok := c.Get(\"b\"); !ok || v != 2 {\n\t\tt.Errorf(\"b = %v %v\", v, ok)\n\t}\n}\n",
				},
			},
			{
				ID:    "B-007",
				Title: "Merge overlapping intervals",
				Body: "Add `intervals.go` with `Merge(in [][2]int) [][2]int`.\n\n" +
					"It returns the input intervals merged and sorted by start. Intervals\n" +
					"that merely touch — `[1,2]` and `[2,3]` — are one interval. The input\n" +
					"is not sorted and must not be modified.",
				Verify: goVerify(),
				Files: map[string]string{
					"go.mod": goMod,
					"intervals_test.go": "package bench\n\nimport \"reflect\"\nimport \"testing\"\n\n" +
						"func TestMerge(t *testing.T) {\n" +
						"\tfor _, c := range []struct {\n\t\tin, want [][2]int\n\t}{\n" +
						"\t\t{[][2]int{{1, 3}, {2, 6}, {8, 10}}, [][2]int{{1, 6}, {8, 10}}},\n" +
						"\t\t{[][2]int{{8, 10}, {1, 3}}, [][2]int{{1, 3}, {8, 10}}},\n" +
						"\t\t// Touching, not overlapping. This is the case that separates\n" +
						"\t\t// a correct answer from a nearly correct one.\n" +
						"\t\t{[][2]int{{1, 2}, {2, 3}}, [][2]int{{1, 3}}},\n" +
						"\t\t// Fully contained.\n" +
						"\t\t{[][2]int{{1, 10}, {2, 3}}, [][2]int{{1, 10}}},\n" +
						"\t\t{nil, nil},\n\t} {\n" +
						"\t\tif got := Merge(c.in); len(got) != 0 || len(c.want) != 0 {\n" +
						"\t\t\tif !reflect.DeepEqual(got, c.want) {\n" +
						"\t\t\t\tt.Errorf(\"Merge(%v) = %v, want %v\", c.in, got, c.want)\n\t\t\t}\n\t\t}\n\t}\n}\n\n" +
						"func TestMergeDoesNotModifyItsInput(t *testing.T) {\n" +
						"\tin := [][2]int{{8, 10}, {1, 3}}\n\tMerge(in)\n" +
						"\tif in[0] != [2]int{8, 10} {\n\t\tt.Errorf(\"input was reordered: %v\", in)\n\t}\n}\n",
				},
			},
			{
				ID:    "B-008",
				Title: "Run work in parallel and keep the order",
				Body: "Add `pool.go` with `Map(in []int, workers int, f func(int) int) []int`.\n\n" +
					"It applies f to every element using at most `workers` goroutines, and\n" +
					"returns the results in the same order as the input. `workers` may be\n" +
					"larger than the input.\n\n" +
					"The gate runs with the race detector.",
				Verify: raceVerify(),
				Files: map[string]string{
					"go.mod": goMod,
					"pool_test.go": "package bench\n\nimport \"sync/atomic\"\nimport \"testing\"\n\n" +
						"func TestMapKeepsOrder(t *testing.T) {\n" +
						"\tin := make([]int, 100)\n\tfor i := range in {\n\t\tin[i] = i\n\t}\n" +
						"\tgot := Map(in, 8, func(n int) int { return n * 2 })\n" +
						"\tif len(got) != len(in) {\n\t\tt.Fatalf(\"len = %d, want %d\", len(got), len(in))\n\t}\n" +
						"\tfor i, v := range got {\n\t\tif v != i*2 {\n\t\t\tt.Fatalf(\"got[%d] = %d, want %d\", i, v, i*2)\n\t\t}\n\t}\n}\n\n" +
						"func TestMapUsesMoreThanOneGoroutine(t *testing.T) {\n" +
						"\tvar concurrent, peak int64\n" +
						"\tin := make([]int, 50)\n" +
						"\tdone := make(chan struct{})\n" +
						"\tgo func() {\n\t\tMap(in, 4, func(n int) int {\n" +
						"\t\t\tc := atomic.AddInt64(&concurrent, 1)\n" +
						"\t\t\tfor {\n\t\t\t\tp := atomic.LoadInt64(&peak)\n" +
						"\t\t\t\tif c <= p || atomic.CompareAndSwapInt64(&peak, p, c) {\n\t\t\t\t\tbreak\n\t\t\t\t}\n\t\t\t}\n" +
						"\t\t\tfor i := 0; i < 1000; i++ {\n\t\t\t\t_ = i\n\t\t\t}\n" +
						"\t\t\tatomic.AddInt64(&concurrent, -1)\n\t\t\treturn n\n\t\t})\n" +
						"\t\tclose(done)\n\t}()\n\t<-done\n" +
						"\tif atomic.LoadInt64(&peak) < 2 {\n" +
						"\t\tt.Errorf(\"peak concurrency %d: the work ran sequentially\", peak)\n\t}\n}\n\n" +
						"func TestMapHandlesMoreWorkersThanWork(t *testing.T) {\n" +
						"\tgot := Map([]int{1, 2}, 16, func(n int) int { return n + 1 })\n" +
						"\tif len(got) != 2 || got[0] != 2 || got[1] != 3 {\n\t\tt.Errorf(\"got %v\", got)\n\t}\n}\n",
				},
			},
			{
				ID:    "B-009",
				Title: "Change a signature everywhere it is used",
				Body: "`Save` must take a `Record` rather than three loose arguments, so a\n" +
					"caller cannot transpose them. Change `Record` usage across the package\n" +
					"so the tests pass. Three files call it.",
				Verify: goVerify(),
				Files: map[string]string{
					"go.mod": goMod,
					"record.go": "package bench\n\n" +
						"type Record struct {\n\tName string\n\tAge  int\n\tCity string\n}\n\n" +
						"var saved []Record\n\n" +
						"// Save stores a record.\nfunc Save(name string, age int, city string) {\n" +
						"\tsaved = append(saved, Record{Name: name, Age: age, City: city})\n}\n\n" +
						"func Saved() []Record { return saved }\n\nfunc Reset() { saved = nil }\n",
					"importer.go": "package bench\n\n" +
						"// ImportOne saves a single row.\nfunc ImportOne() {\n\tSave(\"ada\", 36, \"london\")\n}\n",
					"seed.go": "package bench\n\n" +
						"// SeedTwo saves two rows.\nfunc SeedTwo() {\n" +
						"\tSave(\"grace\", 45, \"arlington\")\n\tSave(\"edsger\", 42, \"austin\")\n}\n",
					"record_test.go": "package bench\n\nimport \"testing\"\n\n" +
						"func TestSaveTakesARecord(t *testing.T) {\n\tReset()\n" +
						"\tSave(Record{Name: \"ada\", Age: 36, City: \"london\"})\n" +
						"\tif got := Saved(); len(got) != 1 || got[0].Name != \"ada\" || got[0].City != \"london\" {\n" +
						"\t\tt.Fatalf(\"Saved = %v\", got)\n\t}\n}\n\n" +
						"func TestEveryCallerWasUpdated(t *testing.T) {\n\tReset()\n\tImportOne()\n\tSeedTwo()\n" +
						"\tif got := Saved(); len(got) != 3 {\n\t\tt.Fatalf(\"Saved = %v, want 3 records\", got)\n\t}\n}\n",
				},
			},
		},
	}
}

// Get returns a suite by name.
func Get(name string) (Suite, error) {
	if name == "" || name == "std" {
		return Std(), nil
	}
	return Suite{}, fmt.Errorf("unknown suite %q; the only suite is \"std\"", name)
}
