package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tui"
)

// driveResult is what runCrawl returns to the caller. PanicSeq is
// the input sequence that triggered the panic (empty on success);
// Stack is the recovered panic + stack trace.
type driveResult struct {
	Iters     int
	PanicSeq  []Input
	PanicIter int    // index in the sequence where the panic happened
	Stack     string // multiline stack trace via runtime/debug.Stack()
	Panic     any    // the recover() value, formatted
}

// generator is one strategy for producing the next Input. Mode-specific
// commands install different generators (tui mixes everything, form
// focuses on text-input keys, resize is all WindowSizeMsg).
type generator func(iter int) Input

// runCrawl is the work-horse loop. It builds a fresh App, calls
// Init() once, then feeds it `iters` inputs from `gen` while
// catching any panic in either the model's Update or its View.
// Returns a driveResult; the caller decides whether to write a
// crash report.
//
// Why we also call View(): bubbletea panics in real life often come
// from the View() call (lipgloss style applied to a degenerate
// width, for example), not Update. We exercise both each iter.
func runCrawl(iters int, gen generator, viewWidth, viewHeight int) (res driveResult) {
	return runCrawlWithPreamble(iters, nil, gen, viewWidth, viewHeight)
}

// runCrawlWithPreamble first sends each Input in `preamble` to the
// model (preamble inputs are NOT counted in the iter budget and
// don't appear in the panic-sequence if the panic happens during
// random play). This is the hatch the form mode uses to navigate
// into the modal before random-fuzzing begins.
func runCrawlWithPreamble(iters int, preamble []Input, gen generator, viewWidth, viewHeight int) (res driveResult) {
	cfg, _ := config.Load()
	// Skip the first-run tour: with Tour.Shown=false the tour overlay
	// auto-opens and swallows every key, so the random walk never
	// reaches any of the seven screens. Locally the dev's config
	// already has the tour completed, which masks this in development
	// but trips CI and the form/all-screens-random preambles.
	cfg.Tour.Shown = true
	app := tui.New(cfg, "crawl")
	_ = app.Init() // returns a Cmd; we don't execute it (would touch real tmux)

	res.Iters = iters
	seq := make([]Input, 0, iters+len(preamble))

	// Seed with an initial size — without it, view() would render
	// against zero dimensions and trip layout code that assumes
	// positive bounds.
	var model tea.Model = app
	model, _ = model.Update(tea.WindowSizeMsg{Width: viewWidth, Height: viewHeight})

	// Preamble: drive the model into the right state before random
	// play. Preamble panics ARE captured (with seq prefix) because a
	// panic during preamble is also a real bug — but we mark them as
	// such in the sequence header by including the preamble inputs.
	defer func() {
		if r := recover(); r != nil {
			res.PanicSeq = seq
			res.PanicIter = len(seq) - 1
			res.Panic = r
			res.Stack = string(debug.Stack())
		}
	}()

	for _, in := range preamble {
		seq = append(seq, in)
		var cmd tea.Cmd
		model, cmd = model.Update(in.Msg)
		_ = cmd
		_ = model.View()
	}
	for i := 0; i < iters; i++ {
		in := gen(i)
		seq = append(seq, in)
		var cmd tea.Cmd
		model, cmd = model.Update(in.Msg)
		_ = cmd
		_ = model.View()
	}
	return res
}

// reportCrash writes the panic details to a markdown file under
// docs/03_Agent_Logs/ (falling back to /tmp if the docs dir isn't
// available — e.g. when running from somewhere outside the repo).
// Returns the path written, or "" on failure.
func reportCrash(mode string, res driveResult) string {
	dir := filepath.Join("docs", "03_Agent_Logs")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		dir = "/tmp"
	}
	runID := time.Now().UnixMilli()
	stamp := time.Now().UTC().Format("2006-01-02")
	path := filepath.Join(dir, fmt.Sprintf("crawl-%s-%s-%d.md", stamp, mode, runID))

	var body []byte
	add := func(format string, a ...any) {
		body = append(body, []byte(fmt.Sprintf(format, a...))...)
	}
	add("# Crawl crash: mode=%s (runid=%d)\n\n", mode, runID)
	add("- Date: %s\n", time.Now().UTC().Format(time.RFC3339))
	add("- Iterations completed: %d / %d\n", res.PanicIter, res.Iters)
	add("- Panic: `%v`\n\n", res.Panic)
	add("## Input sequence (replay these in order)\n\n")
	add("```\n%s```\n\n", formatSequence(res.PanicSeq))
	add("## Stack\n\n```\n%s```\n", res.Stack)

	if err := os.WriteFile(path, body, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "couldn't write crash report: %v\n", err)
		return ""
	}
	return path
}
