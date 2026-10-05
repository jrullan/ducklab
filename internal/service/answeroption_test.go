package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/tools"
)

// TI-36X T-005 r-20261004-212715-5xxh: the writer asked about imports, a
// statusRow function and a source field, and offered four options. Option 2
// is verbatim from the run; the others are reconstructed in its shape.
var t005Question = "1) Default or named imports? 2) Should the test call a statusRow(state) function or read state fields? 3) Should the test cover a source field?"

var t005Options = []string{
	"Use a default import, read state fields directly, no source field",
	"Use named imports (createState, dispatch, statusRow), use statusRow function, no source field",
	"Use named imports (createState, dispatch), read state fields directly, test the source field",
	"Use named imports (createState, dispatch, statusRow), use statusRow function, test the source field",
}

// One cell per way a person (or the advisor) names an option, and per way an
// answer must NOT be read as a choice (B-498).
func TestResolveOptionAnswer(t *testing.T) {
	lettered := []string{"A) Keep the parser", "B) Replace the parser", "C) Ask the owner"}
	twins := []string{"Use statusRow for the header", "Use statusRow for the header and footer"}
	cases := []struct {
		name    string
		answer  string
		options []string
		want    int // 0: stays as typed
	}{
		// By number.
		{"real case", "Use option 2", t005Options, 2},
		{"real case with period", "Use option 2.", t005Options, 2},
		{"bare number", "2", t005Options, 2},
		{"bare number with label punctuation", "(3)", t005Options, 3},
		{"option word", "Option 4", t005Options, 4},
		{"hash", "#1", t005Options, 1},
		{"option hash", "option #2", t005Options, 2},
		{"go with", "go with option 3", t005Options, 3},
		{"the option", "pick the option 1", t005Options, 1},
		{"spanish opción", "opción 2", t005Options, 2},
		{"spanish no accent", "opcion 2", t005Options, 2},
		{"spanish usa la opción", "Usa la opción 2", t005Options, 2},
		{"spanish la 2", "la 2", t005Options, 2},
		{"spanish elijo", "Elijo la opción 3", t005Options, 3},
		// By letter, only when the options are lettered.
		{"bare letter", "B", lettered, 2},
		{"bare letter a", "a", lettered, 1},
		{"option letter", "option c", lettered, 3},
		{"spanish letter", "Opción B", lettered, 2},
		{"letter on unlettered options", "b", t005Options, 0},
		// By echo.
		{"exact echo", t005Options[1], t005Options, 2},
		{"echo case and punctuation", "use named imports createState dispatch statusRow use statusRow function no source field", t005Options, 2},
		{"echo with option label", "Option 2: " + t005Options[1], t005Options, 2},
		{"echo with numbered label", "2. " + t005Options[1], t005Options, 2},
		{"echo of lettered option without its letter", "Replace the parser", lettered, 2},
		{"echo cut short", "Use named imports (createState, dispatch, statusRow), use statusRow function, no source", t005Options, 2},
		// Ambiguous or not a choice: stays as typed.
		{"number beyond the list", "Use option 5", t005Options, 0},
		{"option zero", "option 0", t005Options, 0},
		{"short prefix of two options", "Use named imports", t005Options, 0},
		{"short prefix of one option", "Use a default import", t005Options, 0},
		{"cut-short copy of two options", "Use statusRow for the head", []string{"Use statusRow for the header", "Use statusRow for the header row"}, 0},
		{"text matching two options", "Use statusRow for the header", append(twins, twins[0]), 0},
		{"label disagrees with text", "Option 3: " + t005Options[1], t005Options, 0},
		{"negated", "not option 2", t005Options, 0},
		{"anything but", "anything but option 2", t005Options, 0},
		{"choice with more to say", "Option 2, but test the source field too", t005Options, 0},
		{"free text", "Use named imports and skip statusRow entirely", t005Options, 0},
		{"no options offered", "option 2", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, n := resolveOptionAnswer(tc.answer, tc.options)
			if n != tc.want {
				t.Fatalf("resolveOptionAnswer(%q) chose option %d, want %d", tc.answer, n, tc.want)
			}
			want := tc.answer
			if tc.want > 0 {
				want = tc.options[tc.want-1]
			}
			if got != want {
				t.Errorf("resolveOptionAnswer(%q) = %q, want %q", tc.answer, got, want)
			}
		})
	}
}

// pauseOnT005 parks a recovered run on the T-005 question. Options arrive as
// []interface{} after a state file round trip, and as []string from a live
// pause; both must resolve.
func pauseOnT005(t *testing.T, s *Service, id string, options interface{}) *runState {
	t.Helper()
	rs := s.runs[id]
	rs.run.Status = "paused"
	rs.run.PendingKind = "question"
	rs.run.PendingData = map[string]interface{}{
		"question_id": tools.QuestionID(t005Question),
		"question":    t005Question,
		"options":     options,
	}
	// The answer event is what these tests read; make resume reject before
	// it starts a background strategy against t.TempDir.
	rs.run.Stage = "spec"
	return rs
}

func answerEvent(t *testing.T, rs *runState, eventType, key, value string) map[string]interface{} {
	t.Helper()
	events, err := runlog.ReadEvents(rs.runDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type == eventType && e.Data[key] == value {
			return e.Data
		}
	}
	t.Fatalf("no %s event with %s=%q", eventType, key, value)
	return nil
}

// TI-36X T-005 (B-498): "Use option 2" must reach the next prompt as the
// option's full text, with the offered options beside it, and the event must
// still say what the person typed.
func TestRunAnswerRecordsTheChosenOptionsText(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	for _, id := range []string{"r-live", "r-rehydrated", "r-free", "r-yolo"} {
		writeRun(t, entry.Path, projectID, id, "running")
	}
	s.RecoverRuns(context.Background())
	qid := tools.QuestionID(t005Question)
	asInterfaces := make([]interface{}, len(t005Options))
	for i, o := range t005Options {
		asInterfaces[i] = o
	}

	for _, tc := range []struct {
		id      string
		options interface{}
	}{{"r-live", t005Options}, {"r-rehydrated", asInterfaces}} {
		rs := pauseOnT005(t, s, tc.id, tc.options)
		_ = s.RunAnswer(context.Background(), tc.id, "", "Use option 2")

		if got := rs.answers()[qid]; got != t005Options[1] {
			t.Errorf("%s: stored answer = %q, want the full option-2 text", tc.id, got)
		}
		decisions := rs.answeredDecisions()
		for _, want := range []string{
			"A: " + t005Options[1],
			"Options offered:",
			"1. " + t005Options[0],
			"4. " + t005Options[3],
		} {
			if !strings.Contains(decisions, want) {
				t.Errorf("%s: the replayed prompt is missing %q:\n%s", tc.id, want, decisions)
			}
		}
		if strings.Contains(decisions, "A: Use option 2") {
			t.Errorf("%s: the replayed prompt still carries the bare reference:\n%s", tc.id, decisions)
		}
		ev := answerEvent(t, rs, "human", "action", "answer")
		if ev["answer"] != t005Options[1] || ev["answer_raw"] != "Use option 2" || ev["option"] != float64(2) {
			t.Errorf("%s: answer event = %#v, want answer=option 2 text, answer_raw=typed, option=2", tc.id, ev)
		}
	}

	// An answer that is not a choice stays exactly as typed, with no raw
	// duplicate, and the options still ride beside it for the model to read.
	rs := pauseOnT005(t, s, "r-free", t005Options)
	free := "Option 2, but test the source field too"
	_ = s.RunAnswer(context.Background(), "r-free", "", free)
	if got := rs.answers()[qid]; got != free {
		t.Errorf("free answer stored as %q, want it as typed", got)
	}
	if d := rs.answeredDecisions(); !strings.Contains(d, "A: "+free) || !strings.Contains(d, "2. "+t005Options[1]) {
		t.Errorf("free answer's decisions block lost the answer or the options:\n%s", d)
	}
	if ev := answerEvent(t, rs, "human", "action", "answer"); ev["answer_raw"] != nil || ev["option"] != nil {
		t.Errorf("an unmatched answer recorded a choice: %#v", ev)
	}

	// The advisor's yolo draft takes the same path, and its attention
	// notification shows the expanded answer too.
	rs = pauseOnT005(t, s, "r-yolo", t005Options)
	_ = s.runAnswer(context.Background(), "r-yolo", qid, "Option 2.", "advisor:k3 (yolo)", "")
	if got := rs.answers()[qid]; got != t005Options[1] {
		t.Errorf("advisor answer stored as %q", got)
	}
	if n := answerEvent(t, rs, "notification", "kind", "advisor_auto_answer"); n["answer"] != t005Options[1] {
		t.Errorf("advisor notification answer = %#v", n["answer"])
	}
}

// The oracle dispute compares the stored answer with its option text; a
// person who picks "1" has chosen to let the implementer correct the test.
func TestRunAnswerByNumberDecidesAnOracleDispute(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	writeRun(t, entry.Path, projectID, "r-oracle", "running")
	s.RecoverRuns(context.Background())
	rs := s.runs["r-oracle"]
	rs.run.Status = "paused"
	rs.run.PendingKind = "question"
	qid := tools.OracleQuestionPrefix + "t"
	rs.run.PendingData = map[string]interface{}{
		"question_id": qid, "question": "Is the test wrong?",
		"options": []interface{}{tools.OracleCorrectAnswer, tools.OracleKeepAnswer},
	}
	rs.run.Stage = "spec"
	_ = s.RunAnswer(context.Background(), "r-oracle", "", "1")
	if got := rs.answers()[qid]; got != tools.OracleCorrectAnswer {
		t.Errorf("oracle answer stored as %q, want %q", got, tools.OracleCorrectAnswer)
	}
}
