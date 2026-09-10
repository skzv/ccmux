package agent

import (
	"time"

	"github.com/skzv/ccmux/internal/muse"
)

// Muse is Meta's terminal coding agent. Native settings and credentials stay
// owned by Muse; ccmux supplies only the executable and session selection.
type Muse struct{}

func (Muse) ID() ID              { return IDMuse }
func (Muse) DisplayName() string { return "Muse Code" }
func (Muse) Binary() string      { return "muse" }
func (Muse) LaunchCmd(continued bool) string {
	return launchCmdWithBinary(Muse{}, "muse", continued, Commands{})
}
func (Muse) ConfigRoot(home string) string      { return muse.ConfigRoot(home) }
func (Muse) TranscriptsRoot(home string) string { return muse.SessionsRoot(home) }
func (Muse) InitialPrompt(name, description string) string {
	return agentsMdInitialPrompt(name, description)
}
func (Muse) Classify(pane string, changed time.Time, idle time.Duration) State {
	return engineClassify(IDMuse, pane, "", changed, idle)
}
func (Muse) ClassifyWithTitle(pane, title string, changed time.Time, idle time.Duration) State {
	return engineClassify(IDMuse, pane, title, changed, idle)
}
