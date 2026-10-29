package cli

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/jrullan/ducklab/internal/engineclt"
)

// codexLogin runs a subscription sign-in through the engine, which holds the
// tokens: print the page and the code, then wait for the person to approve.
func codexLogin(client *engineclt.Client, id string, out io.Writer) int {
	started, err := client.ProviderLoginStart(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "Sign in to your ChatGPT account to let provider %s use its Codex subscription:\n\n", id)
	fmt.Fprintf(out, "  1. Open  %s\n", str(started["verification_url"]))
	fmt.Fprintf(out, "  2. Enter %s\n\n", str(started["user_code"]))
	fmt.Fprintln(out, "Waiting for the approval (Ctrl+C stops waiting here; the sign-in stays open in the engine)...")

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	defer signal.Stop(interrupt)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	deadline := time.After(16 * time.Minute)
	for {
		select {
		case <-interrupt:
			fmt.Fprintf(out, "\nstopped waiting; check later with: ducklab provider list\n")
			return 130
		case <-deadline:
			fmt.Fprintln(os.Stderr, "error: the sign-in was not approved in time")
			return 1
		case <-tick.C:
		}
		status, err := client.ProviderLoginStatus(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		switch str(status["status"]) {
		case "signed_in":
			signIn, _ := status["sign_in"].(map[string]interface{})
			fmt.Fprintln(out, signInLabel(signIn, id))
			return 0
		case "failed":
			fmt.Fprintf(os.Stderr, "error: %s\n", str(status["error"]))
			return 1
		case "none":
			fmt.Fprintln(os.Stderr, "error: the sign-in was cancelled")
			return 1
		}
	}
}

// signInLabel is the KEY column of `provider list` for a subscription.
func signInLabel(signIn map[string]interface{}, id string) string {
	if ok, _ := signIn["signed_in"].(bool); !ok {
		label := "NOT SIGNED IN (ducklab provider login " + id + ")"
		if problem := str(signIn["problem"]); problem != "" {
			label += ": " + problem
		}
		return label
	}
	return "signed in" + optional(" as ", str(signIn["email"])) + optional(", plan ", str(signIn["plan"]))
}

func optional(prefix, v string) string {
	if v == "" {
		return ""
	}
	return prefix + v
}
