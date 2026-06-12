package claudemodels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// claudeCLIPrompt is the user-message we send to `claude -p` to elicit
// a structured model list. Kept short and directive so Claude doesn't
// preamble before producing the JSON — billable tokens scale with the
// output, and we don't want to pay for reasoning text we throw away.
//
// The schema we pass on the CLI side guarantees a `models` key
// containing an array of strings; this prompt just clarifies *what*
// to put there.
const claudeCLIPrompt = `Output JSON only: the list of Claude model IDs you support. ` +
	`Use full IDs (e.g. "claude-opus-4-8"), not aliases. No explanation, no commentary.`

// claudeCLISchema constrains `claude -p`'s output to a known shape.
// `--json-schema` plus `--output-format json` makes the model produce
// a `structured_output` field on the envelope that we can decode
// directly — no regex over free-form text, no parse heuristics.
const claudeCLISchema = `{"type":"object","properties":` +
	`{"models":{"type":"array","items":{"type":"string"}}},` +
	`"required":["models"]}`

// ClaudeCLIFetcher discovers the model catalog by shelling out to
// `claude -p` with a JSON-schema-constrained prompt. Works for any
// authenticated `claude` install — subscription users (OAuth via
// `claude auth login`) or API users (ANTHROPIC_API_KEY) — because
// `claude` itself handles auth. That's the win over Fetcher: no
// per-user API key on the ccmux side.
//
// Costs ~$0.024 per call (LLM-billed). Cheap enough for weekly
// refresh; that's how the daemon paces it.
type ClaudeCLIFetcher struct {
	// Binary is the path to the `claude` executable. Empty defaults
	// to "claude" (resolved via PATH at exec time). Override for
	// tests with a fake script.
	Binary string
	// Model is the Claude model used to answer the prompt. Defaults
	// to "haiku" — the cheapest model in the family, plenty for a
	// one-shot structured output. Picker rendering of the *returned*
	// IDs is unaffected; this only chooses who's serving the query.
	Model string
	// Run is exec.CommandContext indirection so tests can swap in a
	// fake exec without touching PATH or running a real binary.
	Run func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// claudeCLIResult is the relevant subset of `claude -p --output-format json`'s
// response envelope. We only read the two fields the discovery
// needs; the rest (timings, cache stats, cost) is for the user's
// information, not ours.
type claudeCLIResult struct {
	IsError          bool `json:"is_error"`
	StructuredOutput struct {
		Models []string `json:"models"`
	} `json:"structured_output"`
	Result string `json:"result"` // free-form fallback if structured_output is empty
}

// Fetch invokes `claude -p` and returns the discovered models. The
// returned slice carries Source: SourceClaudeCLI on every entry.
//
// Two failure modes are explicitly recognised and returned as
// ErrClaudeCLIUnavailable so the Service's discovery chain can fall
// through to the next source without logging a scary error:
//   - claude binary not on PATH (exec.LookPath fails)
//   - claude returned `is_error: true` with a "Not logged in" message
//
// Any other failure (network, parse, exit code) is returned as a
// distinct error the caller can log.
func (f ClaudeCLIFetcher) Fetch(ctx context.Context) ([]Model, error) {
	binary := f.Binary
	if binary == "" {
		binary = "claude"
	}
	// Resolve up-front so a missing binary doesn't surface as a
	// generic "exec: file not found" — we want to distinguish it
	// from a real failure so the caller can chain.
	if _, err := exec.LookPath(binary); err != nil && f.Run == nil {
		return nil, ErrClaudeCLIUnavailable
	}

	model := f.Model
	if model == "" {
		model = "haiku"
	}

	args := []string{
		"-p",             // print mode — non-interactive
		"--model", model, // cheapest in-family for this throwaway query
		"--output-format", "json", // structured envelope we can parse
		"--json-schema", claudeCLISchema, // constrain output shape
		"--max-budget-usd", "0.10", // safety cap; one call is ~$0.025
	}

	run := f.Run
	if run == nil {
		run = exec.CommandContext
	}
	cmd := run(ctx, binary, args...)
	cmd.Stdin = strings.NewReader(claudeCLIPrompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run %s: %w (stderr: %s)", binary, err, strings.TrimSpace(stderr.String()))
	}

	var res claudeCLIResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		return nil, fmt.Errorf("decode claude -p response: %w", err)
	}
	if res.IsError {
		// "Not logged in", "API key invalid", etc. — fall through to
		// the next source rather than fail-fast. The user gets the
		// curated list in the picker; their `claude` still works.
		if strings.Contains(strings.ToLower(res.Result), "logged in") ||
			strings.Contains(strings.ToLower(res.Result), "log in") ||
			strings.Contains(strings.ToLower(res.Result), "/login") {
			return nil, ErrClaudeCLIUnavailable
		}
		return nil, fmt.Errorf("claude -p reported error: %s", res.Result)
	}
	if len(res.StructuredOutput.Models) == 0 {
		return nil, errors.New("claude -p returned no models")
	}

	out := make([]Model, 0, len(res.StructuredOutput.Models))
	for _, id := range res.StructuredOutput.Models {
		out = append(out, Model{
			ID:     strings.TrimSpace(id),
			Family: familyOf(id),
			Source: SourceClaudeCLI,
		})
	}
	return out, nil
}
