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
const stdVersion = 1

// goVerify is the gate for every Go task here: the tests decide, not a model.
func goVerify() config.Verify {
	return config.Verify{Mode: "tests", Tests: "go test ./...", TimeoutS: 300}
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
