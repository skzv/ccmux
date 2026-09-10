package agent

import (
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/gemini"
)

// Gemini CLI remains supported for Code Assist Standard/Enterprise and paid
// API users. It is distinct from Antigravity in executable, settings and history.
type Gemini struct{}

func (Gemini) ID() ID              { return IDGemini }
func (Gemini) DisplayName() string { return "Gemini CLI" }
func (Gemini) Binary() string      { return "gemini" }
func (Gemini) LaunchCmd(continued bool) string {
	return launchCmdWithBinary(Gemini{}, "gemini", continued, Commands{})
}
func (Gemini) ConfigRoot(home string) string      { return gemini.ConfigRoot(home) }
func (Gemini) TranscriptsRoot(home string) string { return gemini.SessionsRoot(home) }
func (Gemini) InitialPrompt(name, description string) string {
	return strings.ReplaceAll(agentsMdInitialPrompt(name, description), "write AGENTS.md", "write GEMINI.md")
}
func (Gemini) Classify(pane string, changed time.Time, idle time.Duration) State {
	return engineClassify(IDGemini, pane, "", changed, idle)
}
func (Gemini) ClassifyWithTitle(pane, title string, changed time.Time, idle time.Duration) State {
	return engineClassify(IDGemini, pane, title, changed, idle)
}
