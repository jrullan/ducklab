package conv

import "testing"

func mustCompile(t *testing.T, src string) *Expr {
	t.Helper()
	e, err := Compile(src)
	if err != nil {
		t.Fatalf("Compile(%q): %v", src, err)
	}
	return e
}

func evalOK(t *testing.T, src string, s *State) bool {
	t.Helper()
	got, err := mustCompile(t, src).Eval(s)
	if err != nil {
		t.Fatalf("Eval(%q): %v", src, err)
	}
	return got
}

// AC-22: the canonical pair expression parses and evaluates.
func TestPairUntilExpression(t *testing.T) {
	const src = `gate == "green" and verdict == "approve"`
	cases := []struct {
		gate, verdict string
		want          bool
	}{
		{"green", "approve", true},
		{"green", "request-changes", false},
		{"red", "approve", false},
		{"red", "request-changes", false},
		{"none", "approve", false},
	}
	for _, c := range cases {
		got := evalOK(t, src, &State{Gate: c.gate, Verdict: c.verdict})
		if got != c.want {
			t.Errorf("gate=%q verdict=%q: got %v, want %v", c.gate, c.verdict, got, c.want)
		}
	}
}

func TestSoloUntilExpression(t *testing.T) {
	if !evalOK(t, `gate == "green"`, &State{Gate: "green"}) {
		t.Error("green gate should satisfy solo's Until")
	}
	if evalOK(t, `gate == "green"`, &State{Gate: "red"}) {
		t.Error("red gate satisfied solo's Until")
	}
}

func TestTournamentChoiceExpression(t *testing.T) {
	if !evalOK(t, `choice != "none"`, &State{Choice: "A"}) {
		t.Error("a chosen candidate should satisfy the expression")
	}
	if evalOK(t, `choice != "none"`, &State{Choice: "none"}) {
		t.Error(`choice "none" satisfied choice != "none"`)
	}
}

func TestNotAndParentheses(t *testing.T) {
	s := &State{Gate: "red", Changed: true}
	if !evalOK(t, `not gate == "green"`, s) {
		t.Error("not applied incorrectly")
	}
	if !evalOK(t, `(gate == "red" or gate == "none") and changed`, s) {
		t.Error("parenthesised grouping evaluated incorrectly")
	}
}

func TestBareIdentifierTruthiness(t *testing.T) {
	if !evalOK(t, `changed`, &State{Changed: true}) {
		t.Error("bare bool should be its value")
	}
	if evalOK(t, `changed`, &State{Changed: false}) {
		t.Error("false bool was truthy")
	}
	if !evalOK(t, `verdict`, &State{Verdict: "approve"}) {
		t.Error("non-empty string should be truthy")
	}
	if evalOK(t, `verdict`, &State{Verdict: ""}) {
		t.Error("empty string was truthy")
	}
	if !evalOK(t, `round`, &State{Round: 2}) {
		t.Error("non-zero int should be truthy")
	}
}

func TestRoundComparison(t *testing.T) {
	if !evalOK(t, `round == 3`, &State{Round: 3}) {
		t.Error("round == 3 failed at round 3")
	}
	if evalOK(t, `round == 3`, &State{Round: 2}) {
		t.Error("round == 3 matched at round 2")
	}
}

// The whole point of a closed identifier set: no arbitrary evaluation.
func TestNoArbitraryCodeEvaluation(t *testing.T) {
	bad := []string{
		`os.Exit(1)`,
		`exec("rm -rf /")`,
		`gate == "green"; os.Exit(1)`,
		"`rm -rf /`",
		`$(whoami)`,
		`gate || true`,
		`gate & changed`,
	}
	for _, src := range bad {
		if _, err := Compile(src); err == nil {
			t.Errorf("Compile(%q) succeeded; the expression language must reject it", src)
		}
	}
}

func TestUnknownIdentifierIsLoadTimeError(t *testing.T) {
	_, err := Compile(`wizard == "gandalf"`)
	if err == nil {
		t.Fatal("unknown identifier accepted")
	}
	if !contains(err.Error(), "unknown identifier") || !contains(err.Error(), "gate") {
		t.Errorf("error should name the problem and list the allowed set, got: %v", err)
	}
}

// Mixing and/or unparenthesised is ambiguous under the spec's flat grammar,
// so it is rejected rather than silently grouped left-to-right.
func TestMixingAndOrRequiresParentheses(t *testing.T) {
	if _, err := Compile(`gate == "green" or changed and verdict == "approve"`); err == nil {
		t.Fatal("unparenthesised and/or mix accepted")
	}
	if _, err := Compile(`(gate == "green" or changed) and verdict == "approve"`); err != nil {
		t.Errorf("parenthesised form rejected: %v", err)
	}
}

func TestTypeMismatchInComparison(t *testing.T) {
	bad := []string{
		`gate == 3`,        // string ident, int literal
		`round == "three"`, // int ident, string literal
		`changed == "yes"`, // bool ident, string literal
	}
	for _, src := range bad {
		if _, err := Compile(src); err == nil {
			t.Errorf("Compile(%q) accepted a type mismatch", src)
		}
	}
}

func TestMalformedExpressions(t *testing.T) {
	bad := []string{
		`gate ==`,
		`== "green"`,
		`(gate == "green"`,
		`gate == "green")`,
		`gate = "green"`,
		`"unterminated`,
		`and gate`,
	}
	for _, src := range bad {
		if _, err := Compile(src); err == nil {
			t.Errorf("Compile(%q) accepted malformed input", src)
		}
	}
}

func TestEmptyExpressionNeverTerminates(t *testing.T) {
	e := mustCompile(t, "")
	got, err := e.Eval(&State{Gate: "green", Verdict: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("an empty Until terminated the loop; only the round cap should bound it")
	}
}

func TestNilExprIsSafe(t *testing.T) {
	var e *Expr
	got, err := e.Eval(&State{})
	if err != nil || got {
		t.Errorf("nil Expr: got %v, %v; want false, nil", got, err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestNestedParentheses(t *testing.T) {
	s := &State{Gate: "green", Verdict: "approve", Changed: true, Round: 2}
	if !evalOK(t, `((gate == "green" and verdict == "approve") or round == 9) and changed`, s) {
		t.Error("nested parentheses evaluated incorrectly")
	}
	if evalOK(t, `((gate == "red" and verdict == "approve") or round == 9) and changed`, s) {
		t.Error("nested parentheses returned true for a false expression")
	}
}
