package cli

import (
	"fmt"
	"os"
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
	run, err := client.TestStart(projectID, taskID, duckling)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	runID := str(run["id"])
	fmt.Printf("writing the test for %s (run %s)\n", taskID, runID)
	code = followRun(client, runID)
	// Said whichever way it went: the next step is the same command either
	// way, and someone who just read a test should not have to look it up.
	fmt.Printf("\nread it, then:  ducklab run accept %s\n", runID)
	fmt.Printf("then build it:  ducklab run %s\n", taskID)
	return code
}
