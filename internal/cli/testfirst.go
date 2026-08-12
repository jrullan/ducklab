package cli

import (
	"fmt"
	"os"
	"strings"
)

// testFirstCmd is `ducklab test <task-id>`.
//
// It writes the test that will judge the task, and stops for a person to read
// it. The implementation is a separate run — by a different duckling if you
// name one — judged by a gate it did not author.
func testFirstCmd(args []string, repo string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ducklab test <task-id> [--duckling ID]")
		fmt.Fprintln(os.Stderr, "  writes the failing test first; `ducklab run <task-id>` implements it")
		return 2
	}
	taskID := args[0]
	duckling := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--duckling" && i+1 < len(args) {
			duckling = args[i+1]
			i++
			continue
		}
		fmt.Fprintf(os.Stderr, "unknown flag: %s\n", args[i])
		return 2
	}

	client, projectID, code := project(repo)
	if code != 0 {
		return code
	}
	run, err := client.TestStart(projectID, taskID, duckling, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	runID := str(run["id"])
	fmt.Printf("writing the test for %s (run %s)\n", taskID, runID)
	code = followRun(client, runID)

	// Printed, not merely referred to.
	//
	// This said "read it, then accept" and showed nothing, so the first person
	// to use it — me — accepted a test without reading it. The test contained
	// a table case demanding a total of 100 cents from an empty slice, and a
	// second case that spread the remainder the opposite way from the task and
	// from its own earlier case. A correct implementation was then judged
	// FAILED by it.
	//
	// A failing test becomes the specification of the next run. Asking someone
	// to approve a specification without putting it in front of them is asking
	// for a rubber stamp.
	diff, _, _, derr := client.RunDiff(runID)
	if derr == nil && strings.TrimSpace(diff) != "" {
		fmt.Printf("\n%s\n", strings.Repeat("─", 60))
		fmt.Println("This is what will judge the implementation. Read it before accepting:")
		fmt.Printf("%s\n%s", strings.Repeat("─", 60), diff)
		if !strings.HasSuffix(diff, "\n") {
			fmt.Println()
		}
		fmt.Println(strings.Repeat("─", 60))
		// The two things a person is actually checking for, named. Both were
		// wrong in the first real test this produced.
		fmt.Println("Does every case agree with the task, and with the other cases?")
	}
	fmt.Printf("\naccept it:      ducklab run accept %s\n", runID)
	fmt.Printf("then build it:  ducklab run %s\n", taskID)
	// Rejecting anywhere in ducklab means leaving the work alone rather than
	// destroying it, so the file stays. Said here because the consequence is
	// specific: the gate is red until it goes, and the next `ducklab test`
	// cannot tell its own new failure from this leftover one.
	fmt.Printf("or reject it:   ducklab run reject %s\n", runID)
	fmt.Println("                (the file stays on disk — delete it before trying again)")
	return code
}
