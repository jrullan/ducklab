package service

// Reference scaling. A stage's reference corpus must never be bounded by the
// architect's context window — raising the ceiling of small local models is
// the project's thesis, and inline injection alone made "how much wiki can I
// attach" a property of the seat. When the rendered references would crowd
// the seat's context, the run switches to digest mode: every document is
// read in chunks that fit, the architect's own model distills each into a
// bounded digest (digest mode fires exactly when that seat is small, and the
// small seats are the free local ones), and the prompt carries the digests
// plus an index. The full text stays reachable through the ref_read tool —
// the digest is the floor, tool use is the ceiling — and the proposal card
// names the documents that were never opened, because "considered" must be
// observable, never assumed.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/reftext"
)

const (
	// refDigestTargetChars bounds one document's digest.
	refDigestTargetChars = 2_000
	// refDigestTriggerPct: references alone above this share of the
	// architect's context switch the run to digest mode — the same line the
	// context-fit preflight warns at for the whole prompt.
	refDigestTriggerPct = 40
)

// refsNeedDigest decides the mode. An undeclared window stays inline: the
// preflight already warns on those, and guessing small would silently digest
// for a seat that may be huge.
func refsNeedDigest(refChars, contextTokens int) bool {
	if contextTokens <= 0 {
		return false
	}
	return (refChars/4)*100/contextTokens >= refDigestTriggerPct
}

// digestChunkChars sizes a summarization chunk to the digesting seat: ~30%
// of its window per call, bounded so tiny declarations still make progress
// and huge ones do not stall a single call.
func digestChunkChars(contextTokens int) int {
	c := contextTokens * 6 / 5
	if c < 8_000 {
		c = 8_000
	}
	if c > 60_000 {
		c = 60_000
	}
	return c
}

// splitRefChunks cuts text into pieces of at most size chars, snapping to a
// newline where one is near so a chunk does not open mid-sentence.
func splitRefChunks(text string, size int) []string {
	var chunks []string
	for len(text) > size {
		cut := size
		if nl := strings.LastIndexByte(text[:size], '\n'); nl > size/2 {
			cut = nl + 1
		}
		chunks = append(chunks, text[:cut])
		text = text[cut:]
	}
	if len(text) > 0 {
		chunks = append(chunks, text)
	}
	return chunks
}

// refSummarizer is the seam for tests: one prompt in, one distillation out.
type refSummarizer func(ctx context.Context, prompt string) (string, error)

type refDigest struct {
	Path        string `json:"path"`
	Chars       int    `json:"chars"`
	DigestChars int    `json:"digest_chars"`
	Cached      bool   `json:"cached"`
	// Unreadable is the notice that stands in for a binary reference that
	// could not be extracted; such a document is never summarized (B-519).
	Unreadable string `json:"unreadable,omitempty"`
	digest     string
}

// refCacheEntry is what the reference cache (reftext.CacheDir) stores: a digest is paid for
// once per document version per model, not once per run.
type refCacheEntry struct {
	Path   string `json:"path"`
	Model  string `json:"model"`
	Chars  int    `json:"chars"`
	Digest string `json:"digest"`
}

func refCacheKey(text, model string) string {
	sum := sha256.Sum256([]byte(text + "\x00" + model + "\x00digest-v1"))
	return hex.EncodeToString(sum[:16])
}

// digestOneRef distills a document, reading it in chunks that fit the
// digesting seat and merging when it took more than one pass.
func digestOneRef(ctx context.Context, path, text string, chunkChars int, summarize refSummarizer) (string, error) {
	base := filepath.Base(path)
	var parts []string
	for i, chunk := range splitRefChunks(text, chunkChars) {
		prompt := fmt.Sprintf(
			"Digest this section (%d) of the reference document %q in at most 200 words. "+
				"Capture what it DEFINES — architecture decisions, domain rules, workflows, "+
				"exact identifiers and codes — so a reader knows what detail the full text "+
				"holds. No preamble, no commentary.\n\n%s", i+1, base, chunk)
		out, err := summarize(ctx, prompt)
		if err != nil {
			return "", fmt.Errorf("digest %s: %w", base, err)
		}
		parts = append(parts, strings.TrimSpace(out))
	}
	joined := strings.Join(parts, "\n")
	if len(parts) > 1 && len(joined) > refDigestTargetChars {
		prompt := fmt.Sprintf(
			"Combine these partial digests of the reference document %q into ONE digest of "+
				"at most 250 words. Keep exact identifiers and codes. No preamble.\n\n%s",
			base, joined)
		merged, err := summarize(ctx, prompt)
		if err != nil {
			return "", fmt.Errorf("merge digest %s: %w", base, err)
		}
		joined = strings.TrimSpace(merged)
	}
	if len(joined) > refDigestTargetChars {
		joined = joined[:refDigestTargetChars] + "…"
	}
	return joined, nil
}

// digestReferences runs the pipeline over the collected files. cacheDir may
// be "" to disable caching (tests). onFile reports each finished document —
// digestion of a big corpus on a local seat takes real time and silence
// reads as a hang.
func digestReferences(ctx context.Context, files []string, chunkChars int, model, cacheDir string, summarize refSummarizer, onFile func(refDigest)) ([]refDigest, error) {
	var out []refDigest
	for _, f := range files {
		// The extraction shares the digest's cache directory (B-519): a PDF
		// is summarized from its text, and a binary that cannot be read is
		// never summarized — a model asked to digest object syntax returns a
		// confident digest of nothing.
		doc, err := reftext.Load(ctx, f, cacheDir)
		if err != nil {
			return nil, fmt.Errorf("reference %q: %w", f, err)
		}
		if !doc.Readable() {
			d := refDigest{Path: f, Unreadable: doc.Notice(), digest: doc.Notice()}
			d.DigestChars = len(d.digest)
			if onFile != nil {
				onFile(d)
			}
			out = append(out, d)
			continue
		}
		text := doc.Text
		d := refDigest{Path: f, Chars: utf8.RuneCountInString(text)}
		key := refCacheKey(text, model)
		cachePath := ""
		if cacheDir != "" {
			cachePath = filepath.Join(cacheDir, key+".json")
			if data, rerr := os.ReadFile(cachePath); rerr == nil {
				var entry refCacheEntry
				if json.Unmarshal(data, &entry) == nil && entry.Digest != "" {
					d.digest, d.Cached = entry.Digest, true
				}
			}
		}
		if d.digest == "" {
			digest, derr := digestOneRef(ctx, f, text, chunkChars, summarize)
			if derr != nil {
				return nil, derr
			}
			d.digest = digest
			if cachePath != "" {
				if os.MkdirAll(cacheDir, 0o755) == nil {
					if data, merr := json.Marshal(refCacheEntry{Path: f, Model: model, Chars: len(text), Digest: digest}); merr == nil {
						_ = os.WriteFile(cachePath, data, 0o644)
					}
				}
			}
		}
		d.DigestChars = len(d.digest)
		if onFile != nil {
			onFile(d)
		}
		out = append(out, d)
	}
	return out, nil
}

// renderRefDigests is the digest-mode reference section of a stage prompt.
func renderRefDigests(digests []refDigest, totalChars int, stageName string, roleOf func(string) string) string {
	if roleOf == nil {
		roleOf = func(string) string { return RefRoleBackground }
	}
	anyScope := false
	for _, d := range digests {
		if roleOf(d.Path) == RefRoleScope {
			anyScope = true
		}
	}
	var b strings.Builder
	b.WriteString("\n\n## Reference documents (digests)\n\n")
	b.WriteString(refGuidance(stageName, anyScope))
	fmt.Fprintf(&b, "The corpus (%d documents, %d chars) does not fit your context, so each "+
		"document appears as a digest. The FULL text of every document is available: call "+
		"the ref_read tool with the document's path whenever a section needs its exact "+
		"detail — identifiers, rules, numbers. Do not guess detail a digest elides.\n",
		len(digests), totalChars)
	for _, d := range digests {
		label := refRoleLabel(roleOf(d.Path), anyScope)
		if d.Unreadable != "" {
			fmt.Fprintf(&b, "\n### Reference: %s%s (unreadable)\n\n%s\n", d.Path, label, d.Unreadable)
			continue
		}
		fmt.Fprintf(&b, "\n### Digest: %s%s (full text: %d chars — ref_read for detail)\n\n%s\n", d.Path, label, d.Chars, d.digest)
	}
	return b.String()
}

// stageReferences loads a stage's references in whichever mode fits the
// architect seat, emits the run's reference events, and arms ref_read.
// Returns the rendered section to append to the seed.
func (s *Service) stageReferences(ctx context.Context, rs *runState, projCfg *config.Project, stageName string, refs []StageRef, architect config.DucklingID) (string, error) {
	// Out of the worktree (review of #173): extractions and digests are
	// plaintext of the person's private references, and under .ducklab they
	// were untracked files for `git add -A` and every clean-tree guard.
	cacheDir := refCacheDir()
	rendered, loaded, dropped, err := loadReferencesCached(ctx, refs, projCfg.References, stageName, cacheDir)
	if err != nil {
		return "", err
	}
	roleOf := refRoleOf(refs)
	files, err := collectRefFiles(refPaths(refs))
	if err != nil {
		return "", err
	}
	// Sized by the text the model would read, not the file's bytes: a PDF
	// of 684 KB is 78 KB of text, and a binary it cannot read is nothing.
	totalChars := 0
	var unreadable []refUnreadable
	for _, f := range files {
		doc, lerr := reftext.Load(ctx, f, cacheDir)
		if lerr != nil {
			continue
		}
		if !doc.Readable() {
			unreadable = append(unreadable, refUnreadable{
				Path: f, Type: doc.Type, Bytes: doc.Size, Reason: doc.Reason, Notice: doc.Notice(),
			})
			continue
		}
		totalChars += utf8.RuneCountInString(doc.Text)
	}
	mode := "inline"
	archCtx := 0
	if d, derr := s.ducklings.Get(architect); derr == nil {
		archCtx = d.Caps.ContextTokens
	}
	// The digest decision reads the FULL corpus, not the capped render: the
	// point of the mode is that caps stop deciding what the model considers.
	if refsNeedDigest(totalChars, archCtx) {
		mode = "digest"
		d, derr := s.ducklings.Get(architect)
		if derr != nil {
			return "", derr
		}
		p, perr := s.ducklings.Provider(architect)
		if perr != nil {
			return "", perr
		}
		summarize := func(sctx context.Context, prompt string) (string, error) {
			return s.refDigestCall(sctx, rs, architect, d.Model, d.Provider, d.Cost, p, prompt)
		}
		digests, derr2 := digestReferences(ctx, files, digestChunkChars(archCtx), d.Model, cacheDir, summarize, func(dg refDigest) {
			rs.writer.AppendEvent("reference_digested", map[string]interface{}{
				"path": dg.Path, "chars": dg.Chars, "digest_chars": dg.DigestChars, "cached": dg.Cached,
				"unreadable": dg.Unreadable,
			})
		})
		if derr2 != nil {
			return "", derr2
		}
		rendered = renderRefDigests(digests, totalChars, stageName, roleOf)
		loaded = loaded[:0]
		for _, dg := range digests {
			rf := refFile{Path: dg.Path, Chars: dg.Chars, Unreadable: dg.Unreadable, text: dg.digest}
			if dg.Unreadable != "" {
				rf.text = dg.Unreadable
			}
			if roleOf(dg.Path) == RefRoleScope {
				rf.Role = RefRoleScope
			}
			loaded = append(loaded, rf)
		}
		dropped = nil
	}
	unreadable = includedUnreadable(unreadable, loaded)
	rs.writer.AppendEvent("references_loaded", map[string]interface{}{
		"files": loaded, "dropped": dropped, "mode": mode, "unreadable": unreadable,
	})
	if len(unreadable) > 0 {
		warnUnreadableRefs(rs, unreadable)
	}
	rs.armRefs(files, mode)
	rs.setRefEvidence(loaded)
	return rendered, nil
}

// refCacheDir is the reference cache shared by inline loading, digests and
// ref_read: outside every project (see reftext.CacheDir).
func refCacheDir() string { return reftext.CacheDir() }

// includedUnreadable keeps the unreadable references the prompt actually
// carried as a notice. A file the inline caps dropped never reached the model
// at all, and warning that "the model saw only a notice" for it was false
// (review of #173); it is reported as dropped instead. Digest mode loads the
// whole corpus, so there every unreadable file is included.
func includedUnreadable(unreadable []refUnreadable, loaded []refFile) []refUnreadable {
	shown := map[string]bool{}
	for _, f := range loaded {
		if f.Unreadable != "" {
			shown[f.Path] = true
		}
	}
	var out []refUnreadable
	for _, u := range unreadable {
		if shown[u.Path] {
			out = append(out, u)
		}
	}
	return out
}

// warnUnreadableRefs tells the person — on the run (Now, the run page, MCP
// run_get) and in the event stream (the conversation lane) — that a
// reference reached the model only as a notice. The model is told too, in
// its prompt; before B-519 neither was, and an architect that never read a
// word of a guidebook wrote a proposal as if it had.
func warnUnreadableRefs(rs *runState, unreadable []refUnreadable) {
	notices := make([]string, 0, len(unreadable))
	for _, u := range unreadable {
		notices = append(notices, u.Notice)
	}
	noun := "reference"
	if len(unreadable) > 1 {
		noun = "references"
	}
	detail := fmt.Sprintf("%d %s could not be read — the model saw only a notice: %s",
		len(unreadable), noun, strings.Join(notices, " | "))
	rs.wmu.Lock()
	if rs.run.Warning == "" {
		rs.run.Warning = detail
	} else {
		rs.run.Warning += " · " + detail
	}
	rs.wmu.Unlock()
	rs.writer.WriteState()
	rs.writer.AppendEvent("warning", map[string]interface{}{
		"detail": detail, "kind": "unreadable_reference", "references": unreadable,
	})
}

// refDigestCall is one distillation call, accounted like every other call
// this run caused: on the tracker and in llm.jsonl.
func (s *Service) refDigestCall(ctx context.Context, rs *runState, seat config.DucklingID, model string, providerID config.ProviderID, cost config.Cost, p provider.Provider, prompt string) (string, error) {
	d, derr := s.ducklings.Get(seat)
	if derr != nil {
		return "", derr
	}
	// Through the shared one-shot path: a raw ChatRequest here had the same
	// latent B-123 bug — a disable_thinking seat reasoning into the cap.
	// The cap follows the same rule as the advisor's: 700 is room for a
	// digest only when the seat is verifiably not reasoning (B-479), and it
	// always fits beside the prompt in the seat's context window.
	caps := s.effectiveCaps(ctx, seat, false)
	const system = "You distill reference documents for a software team. Return only the digest."
	resp, err := s.oneShot(ctx, p, d, caps, system, prompt, 700)
	if err != nil {
		s.logFailedOneShot(rs, seat, d, "librarian", prompt, err, bestResponse(resp))
		return "", err
	}
	calc := provider.CostCalculator{InputPerMTok: cost.InputPerMTok, OutputPerMTok: cost.OutputPerMTok}
	usd := calc.Cost(resp.Usage)
	if rs.tracker != nil {
		rs.tracker.Record(resp.Usage.PromptTokens, resp.Usage.CompletionTokens, usd)
	}
	if w := s.llmWriter(rs, rs.tracker); w != nil {
		w.AppendLLM(&agent.LLMCallRecord{
			Duckling: string(seat), Provider: string(providerID), Model: model,
			Role:         "librarian",
			Request:      map[string]interface{}{"digest": firstN(prompt, 400)},
			Response:     map[string]interface{}{"content": firstN(answerText(resp), 2000)},
			CostUSD:      usd,
			FinishReason: resp.FinishReason,
		})
	}
	return answerText(resp), nil
}
