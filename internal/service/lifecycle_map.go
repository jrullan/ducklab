package service

import "fmt"

// The lifecycle map (B-461).
//
// A newcomer could not tell where a project stood: intent, requirements,
// specification and plan read as document types rather than stages, accepting
// a document and accepting code used similar words, and nothing said that no
// code exists until the first build lands. The guide (guide.go) answers "what
// is the next click"; this answers "where am I on the whole road", in the
// same words for the desktop strip and for an MCP operator's status.
//
// Deterministic and computed from the same facts ProjectStatus already reads:
// stage progress per document, task counts, accepted-but-unreleased work.

// LifecycleStage is one stop on the road.
type LifecycleStage struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// State is done | decision (a draft waits for the person) | current |
	// pending.
	State string `json:"state"`
}

// Lifecycle is the whole road for one project.
type Lifecycle struct {
	Stages []LifecycleStage `json:"stages"`
	// Current is the id of the first stage that is not done.
	Current string `json:"current"`
	// CodeExists is false until a build or test task has been accepted: every
	// earlier stage produces documents, not code.
	CodeExists     bool `json:"code_exists"`
	TasksAccepted  int  `json:"tasks_accepted"`
	TasksTotal     int  `json:"tasks_total"`
	UnreleasedWork int  `json:"unreleased_work"`
	// Next is one sentence in outcome language: what moves the project to the
	// next stop.
	Next string `json:"next"`
}

// lifecycleOf builds the map from stage progress (stageProgress), task counts
// by status, and accepted-but-unreleased work.
func lifecycleOf(progress map[string]string, taskCounts map[string]int, unreleased int) Lifecycle {
	total := 0
	for _, n := range taskCounts {
		total += n
	}
	accepted := taskCounts["accepted"] + taskCounts["done"]
	l := Lifecycle{TasksAccepted: accepted, TasksTotal: total, UnreleasedWork: unreleased, CodeExists: accepted > 0}

	doc := func(id, label, stage string) LifecycleStage {
		switch progress[stage] {
		case "approved":
			return LifecycleStage{ID: id, Label: label, State: "done"}
		case "proposed":
			return LifecycleStage{ID: id, Label: label, State: "decision"}
		}
		return LifecycleStage{ID: id, Label: label, State: "pending"}
	}
	// Labels match the Documents tabs: two vocabularies for one stage (the
	// first draft said "Describe/Specify" above tabs saying "Requirements/
	// Spec") is the confusion this map exists to remove.
	stages := []LifecycleStage{
		doc("requirements", "Requirements", "intake"),
		doc("spec", "Spec", "spec"),
		doc("plan", "Plan", "plan"),
		{ID: "build", Label: "Build", State: "pending"},
		{ID: "release", Label: "Release", State: "pending"},
	}
	planDone := stages[2].State == "done"
	if planDone && total > 0 && accepted == total {
		stages[3].State = "done"
		if unreleased == 0 {
			stages[4].State = "done"
		}
	}
	for i := range stages {
		if stages[i].State != "done" {
			if stages[i].State == "pending" {
				stages[i].State = "current"
			}
			l.Current = stages[i].ID
			break
		}
	}
	l.Stages = stages

	switch l.Current {
	case "requirements":
		if stages[0].State == "decision" {
			l.Next = "Review the requirements draft: accept it, or send it back with a note. No code is written until a plan is accepted."
		} else {
			l.Next = "Describe what you want to build; Ducklab drafts the requirements for you to approve."
		}
	case "spec":
		if stages[1].State == "decision" {
			l.Next = "Review the specification draft: accept it, or send it back with a note."
		} else {
			l.Next = "Requirements accepted. Next: draft the specification (how it will be built)."
		}
	case "plan":
		if stages[2].State == "decision" {
			l.Next = "Review the plan draft: accepting it turns it into tasks that build the code."
		} else {
			l.Next = "Specification accepted. Next: draft the plan (the tasks that build it)."
		}
	case "build":
		if total == 0 {
			l.Next = "The plan has no tasks yet; extend the plan to add the work."
		} else if accepted == 0 {
			l.Next = fmt.Sprintf("Plan accepted. Next: build the first task; no code exists yet (0 of %d tasks accepted).", total)
		} else {
			l.Next = fmt.Sprintf("Building: %d of %d tasks accepted. Next: build the next task.", accepted, total)
		}
	case "release":
		l.Next = fmt.Sprintf("Every planned task is accepted. Next: cut a release (%d accepted task(s) not yet shipped).", unreleased)
	default:
		l.Next = "Everything planned is built and released. Add an intention to extend the project."
	}
	return l
}
