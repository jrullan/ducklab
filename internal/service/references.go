package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/capability"
	"github.com/jrullan/ducklab/internal/config"
)

// Reference documents for a document stage.
//
// An adopt reads the code as its primary truth, but a project that has lived
// a while usually has prose the code cannot carry — a wiki of module docs,
// decision history, a compliance report. Those live OUTSIDE the project
// root, where the ducklings' fs tools rightly cannot reach, so the one
// honest channel is the prompt: the person names paths, the engine loads
// them bounded, and the record says exactly what was included and what the
// caps dropped — silent truncation reads as "covered" when it was not.

const (
	refPerFileChars = 12_000
	refTotalChars   = 80_000
	refMaxFiles     = 40
)

// resolveRefCaps fills a project's unset reference caps with the defaults.
func resolveRefCaps(caps config.References) config.References {
	if caps.PerFileChars == 0 {
		caps.PerFileChars = refPerFileChars
	}
	if caps.TotalChars == 0 {
		caps.TotalChars = refTotalChars
	}
	if caps.MaxFiles == 0 {
		caps.MaxFiles = refMaxFiles
	}
	return caps
}

// refFile is one loaded reference, for the record.
type refFile struct {
	Path      string `json:"path"`
	Chars     int    `json:"chars"`
	Truncated bool   `json:"truncated,omitempty"`
}

// loadReferences expands the named paths (files, or directories searched
// recursively for .md and .txt), loads them under the caps, and renders the
// section a stage prompt carries. dropped names what the caps excluded.
// collectRefFiles expands the named paths — files, or directories searched
// recursively for .md/.txt — into the sorted file list both modes share.
func collectRefFiles(paths []string) ([]string, error) {
	var files []string
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "~/") {
			if home, herr := os.UserHomeDir(); herr == nil {
				p = filepath.Join(home, p[2:])
			}
		}
		info, serr := os.Stat(p)
		if serr != nil {
			return nil, fmt.Errorf("reference %q: %w", p, serr)
		}
		if !info.IsDir() {
			files = append(files, p)
			continue
		}
		werr := filepath.WalkDir(p, func(path string, d os.DirEntry, werr error) error {
			if werr != nil || d.IsDir() {
				return werr
			}
			switch strings.ToLower(filepath.Ext(path)) {
			case ".md", ".txt", ".json":
				files = append(files, path)
			}
			return nil
		})
		if werr != nil {
			return nil, fmt.Errorf("reference %q: %w", p, werr)
		}
	}
	sort.Strings(files)
	return files, nil
}

func loadReferenceContracts(files []string) ([]capability.ReferenceContract, error) {
	var refs []capability.StructuredReference
	for _, path := range files {
		if strings.ToLower(filepath.Ext(path)) != ".json" {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("structured reference %q: %w", path, err)
		}
		if capability.DeclaresReferenceContract(raw) {
			refs = append(refs, capability.StructuredReference{Source: path, Content: raw})
		}
	}
	return capability.NormalizeReferenceContracts(refs)
}

func renderReferenceContractInstructions(contracts []capability.ReferenceContract) string {
	if len(contracts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Executable reference contracts\n\nThese shapes were declared by structured references and are checked mechanically before semantic review. Ducklab, not the model, adds the following machine-owned region to the final specification. Use it as authoritative design context; do not copy, edit, or restate it as control metadata. Typed descriptions elsewhere remain documentation only.\n\n")
	b.WriteString(capability.RenderReferenceContractRegion(contracts))
	b.WriteString("\n")
	return b.String()
}

func loadReferences(paths []string, caps config.References, stageName string) (rendered string, loaded []refFile, dropped []string, err error) {
	caps = resolveRefCaps(caps)
	files, err := collectRefFiles(paths)
	if err != nil {
		return "", nil, nil, err
	}

	var b strings.Builder
	total := 0
	for _, f := range files {
		if len(loaded) >= caps.MaxFiles || total >= caps.TotalChars {
			dropped = append(dropped, f)
			continue
		}
		raw, rerr := os.ReadFile(f)
		if rerr != nil {
			dropped = append(dropped, f)
			continue
		}
		text := string(raw)
		truncated := false
		if len(text) > caps.PerFileChars {
			text = text[:caps.PerFileChars]
			truncated = true
		}
		if total+len(text) > caps.TotalChars {
			text = text[:caps.TotalChars-total]
			truncated = true
		}
		fmt.Fprintf(&b, "\n### Reference: %s\n\n%s\n", f, strings.TrimSpace(text))
		if truncated {
			b.WriteString("\n[truncated — the full document exceeds the reference budget]\n")
		}
		total += len(text)
		loaded = append(loaded, refFile{Path: f, Chars: len(text), Truncated: truncated})
	}
	if len(loaded) == 0 {
		return "", loaded, dropped, nil
	}
	head := "\n\n## Reference documents\n\n" + refGuidance(stageName)
	return head + b.String(), loaded, dropped, nil
}

// refGuidance is stage-shaped. Intake's hardline ("never derive
// requirements") exists because a bug-inbox snapshot once became fifteen
// requirements — but read in a SPEC it told the architect to leave the
// wiki's architecture, RBAC and audit detail out of the document, the
// opposite of why the person attached it.
func refGuidance(stageName string) string {
	if stageName == "spec" || stageName == "plan" {
		return "Provided by the person as design context. USE them: ground your sections in the " +
			"detail they carry — architecture decisions, domain rules, exact identifiers, " +
			"workflows — rather than restating the requirements more vaguely. Your document is " +
			"judged against this detail: a section vaguer than the reference it draws on is " +
			"unfinished. Two limits: the " +
			"approved requirements define scope, so a reference's plans, wish lists or open " +
			"problems never add sections; and where a reference and the code disagree, the code " +
			"is the truth for as-built claims — note the disagreement instead of copying the claim.\n"
	}
	return "Provided by the person as BACKGROUND, not as scope. Where a reference and the code " +
		"disagree, the code is the truth for as-built claims — note the disagreement instead of " +
		"copying the claim. A reference that lists open problems, pending feedback, plans or " +
		"wishes describes WORK, not the product: never derive requirements from it — the first " +
		"survey of MiEmpresa turned a bug-inbox snapshot into fifteen requirements about its " +
		"fourteen bugs. Requirements state what the system IS.\n"
}
