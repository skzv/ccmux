package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/i18n"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// valueColumn is where the value starts on a "label   value" detail
// row: the display column after the label and the spaces following it.
func valueColumn(row, label string) int {
	rest := strings.TrimPrefix(row, label)
	return ansi.StringWidth(label) + (len(rest) - len(strings.TrimLeft(rest, " ")))
}

// TestProjectInfo_LabelsAlignInEveryLanguage — the project-info
// overlay's Identity rows used a fixed 10-cell label column, too narrow
// for several translations ("が検出されました" pushed its value right).
func TestProjectInfo_LabelsAlignInEveryLanguage(t *testing.T) {
	withLang(t, "en")
	p := project.Project{Name: "demo", Path: t.TempDir(), Host: "remote-box", Modified: time.Now().Add(-time.Hour)}
	for _, code := range i18n.Codes() {
		t.Run(code, func(t *testing.T) {
			i18n.SetLanguage(code)
			defer i18n.SetLanguage("en")
			out := ansi.Strip(projectInfoOverlay{}.View(styles.Default(), p, nil, 120, 60))
			col := -1
			for _, label := range []string{tr("session"), tr("agent"), tr("detected"), tr("modified")} {
				found := false
				for _, line := range strings.Split(out, "\n") {
					row := strings.TrimLeft(strings.TrimPrefix(strings.TrimSpace(line), "│"), " ")
					if !strings.HasPrefix(row, label) || row == label {
						continue
					}
					found = true
					c := valueColumn(row, label)
					if c == ansi.StringWidth(label) {
						t.Errorf("%q runs into its value: %q", label, row)
					}
					if col < 0 {
						col = c
					} else if c != col {
						t.Errorf("%q value starts at column %d, others at %d:\n%s", label, c, col, out)
					}
					break
				}
				if !found {
					t.Fatalf("row %q missing:\n%s", label, out)
				}
			}
		})
	}
}

// TestSessionDetail_LabelsAlignInEveryLanguage — the Sessions detail
// pane put a hard-coded number of spaces after each label, right for
// the English words only, so in other languages the values zig-zagged.
// The values must start in one column whatever the labels' widths.
func TestSessionDetail_LabelsAlignInEveryLanguage(t *testing.T) {
	// The narrow layout asks tmux for the prefix key; keep it off the
	// user's tmux server.
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	t.Setenv("TMUX", "")
	withLang(t, "en")
	for _, code := range i18n.Codes() {
		for _, narrow := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/narrow=%v", code, narrow), func(t *testing.T) {
				i18n.SetLanguage(code)
				defer i18n.SetLanguage("en")
				m := newSessions(styles.Default(), DefaultKeymap())
				m.SetSessions([]daemon.SessionState{{
					Name: "c-demo", Host: "local", State: "idle", Project: "demo",
					Path: "/srv/demo", Attached: true, Created: time.Now().Add(-time.Hour),
				}})
				labels := []string{tr("state"), tr("path"), tr("attached"), tr("created")}
				if narrow {
					labels = []string{tr("state"), tr("project"), tr("attached")}
				}
				out := ansi.Strip(m.renderDetail(100, narrow))
				col := -1
				for _, label := range labels {
					found := false
					for _, line := range strings.Split(out, "\n") {
						row := strings.TrimLeft(strings.TrimPrefix(strings.TrimSpace(line), "│"), " ")
						if !strings.HasPrefix(row, label+" ") {
							continue
						}
						found = true
						c := valueColumn(row, label)
						if col < 0 {
							col = c
						} else if c != col {
							t.Errorf("%q value starts at column %d, others at %d:\n%s", label, c, col, out)
						}
						break
					}
					if !found {
						t.Fatalf("row %q missing:\n%s", label, out)
					}
				}
			})
		}
	}
}
