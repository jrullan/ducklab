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

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
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
	digest      string
}

// refCacheEntry is what .ducklab/refs-cache stores: a digest is paid for
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
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("reference %q: %w", f, err)
		}
		text := string(raw)
		d := refDigest{Path: f, Chars: len(text)}
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
func renderRefDigests(digests []refDigest, totalChars int, stageName string) string {
	var b strings.Builder
	b.WriteString("\n\n## Reference documents (digests)\n\n")
	b.WriteString(refGuidance(stageName))
	fmt.Fprintf(&b, "The corpus (%d documents, %d chars) does not fit your context, so each "+
		"document appears as a digest. The FULL text of every document is available: call "+
		"the ref_read tool with the document's path whenever a section needs its exact "+
		"detail — identifiers, rules, numbers. Do not guess detail a digest elides.\n",
		len(digests), totalChars)
	for _, d := range digests {
		fmt.Fprintf(&b, "\n### Digest: %s (full text: %d chars — ref_read for detail)\n\n%s\n", d.Path, d.Chars, d.digest)
	}
	return b.String()
}

// stageReferences loads a stage's references in whichever mode fits the
// architect seat, emits the run's reference events, and arms ref_read.
// Returns the rendered section to append to the seed.
func (s *Service) stageReferences(ctx context.Context, rs *runState, projCfg *config.Project, stageName string, refPaths []string, architect config.DucklingID) (string, error) {
	rendered, loaded, dropped, err := loadReferences(refPaths, projCfg.References, stageName)
	if err != nil {
		return "", err
	}
	files, err := collectRefFiles(refPaths)
	if err != nil {
		return "", err
	}
	mode := "inline"
	archCtx := 0
	if d, derr := s.ducklings.Get(architect); derr == nil {
		archCtx = d.Caps.ContextTokens
	}
	// The digest decision reads the FULL corpus, not the capped render: the
	// point of the mode is that caps stop deciding what the model considers.
	totalChars := 0
	for _, f := range files {
		if info, serr := os.Stat(f); serr == nil {
			totalChars += int(info.Size())
		}
	}
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
		cacheDir := filepath.Join(rs.projectPath, ".ducklab", "refs-cache")
		digests, derr2 := digestReferences(ctx, files, digestChunkChars(archCtx), d.Model, cacheDir, summarize, func(dg refDigest) {
			rs.writer.AppendEvent("reference_digested", map[string]interface{}{
				"path": dg.Path, "chars": dg.Chars, "digest_chars": dg.DigestChars, "cached": dg.Cached,
			})
		})
		if derr2 != nil {
			return "", derr2
		}
		rendered = renderRefDigests(digests, totalChars, stageName)
		loaded = loaded[:0]
		for _, dg := range digests {
			loaded = append(loaded, refFile{Path: dg.Path, Chars: dg.Chars})
		}
		dropped = nil
	}
	rs.writer.AppendEvent("references_loaded", map[string]interface{}{
		"files": loaded, "dropped": dropped, "mode": mode,
	})
	rs.armRefs(files, mode)
	return rendered, nil
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
	resp, err := oneShotChat(ctx, p, d, "You distill reference documents for a software team. Return only the digest.", prompt, 700)
	if err != nil {
		s.logFailedOneShot(rs, seat, d, "librarian", prompt, err)
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
