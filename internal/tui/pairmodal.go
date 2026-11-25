package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	qrterminal "github.com/mdp/qrterminal/v3"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// pairModal is the `P`-key mobile pairing QR modal — the TUI mirror of
// `ccmux pair`. Asks the local ccmuxd for a one-time pairing token via
// /v1/pair-token and renders a half-block QR code + deep-link URL.
type pairModal struct {
	open    bool
	loading bool
	url     string
	qr      string
	err     string
}

type pairTokenLoadedMsg struct {
	URL string
	QR  string
	Err string
}

func (m *pairModal) Open() tea.Cmd {
	m.open = true
	m.loading = true
	m.url = ""
	m.qr = ""
	m.err = ""
	return fetchPairTokenCmd()
}

func (m *pairModal) Close() {
	*m = pairModal{}
}

func (m pairModal) IsOpen() bool { return m.open }

func (m *pairModal) Apply(msg pairTokenLoadedMsg) {
	if !m.open {
		return
	}
	m.loading = false
	m.url = msg.URL
	m.qr = msg.QR
	m.err = msg.Err
}

// fetchPairTokenFn is a test seam; production dials the local ccmuxd.
var fetchPairTokenFn = func(ctx context.Context) (string, error) {
	cli, err := daemon.LocalClient()
	if err != nil {
		return "", err
	}
	resp, err := cli.CreatePairToken(ctx)
	if err != nil {
		return "", err
	}
	return resp.URL, nil
}

func fetchPairTokenCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		url, err := fetchPairTokenFn(ctx)
		if err != nil {
			return pairTokenLoadedMsg{Err: err.Error()}
		}
		var qr strings.Builder
		qrterminal.GenerateHalfBlock(url, qrterminal.L, &qr)
		return pairTokenLoadedMsg{
			URL: url,
			QR:  strings.TrimRight(qr.String(), "\n"),
		}
	}
}

func (m pairModal) View(st styles.Styles, width, height int) string {
	lines := []string{
		st.Emphasis.Render(tr("Pair mobile device")),
		st.Subtitle.Render(tr("Scan with a mobile client (expires in 5 minutes; requires listen_tailnet = true).")),
		"",
	}
	switch {
	case m.loading:
		lines = append(lines, st.Muted.Render(tr("Generating pairing token…")))
	case m.err != "" && m.url == "":
		lines = append(lines, st.StatusError.Render("⚠ "+m.err))
	default:
		if m.qr != "" {
			lines = append(lines, m.qr, "")
		}
		if m.url != "" {
			lines = append(lines, st.Emphasis.Render(m.url))
		}
	}
	lines = append(lines, "", st.Muted.Render(tr("press P or esc to close")))

	modalW := maxInt(32, minInt(84, width-4))
	body := strings.Join(lines, "\n")
	if height > 6 {
		body = clampLines(body, height-6)
	}
	modal := st.PaneFocused.Width(modalW).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}
