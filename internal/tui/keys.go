package tui

import "github.com/charmbracelet/bubbles/key"

// Keymap is the complete set of bindings used across the TUI.
// Screen-specific bindings live alongside the screen; this is the global set.
type Keymap struct {
	// Navigation between screens
	Dashboard key.Binding
	Sessions  key.Binding
	Projects  key.Binding
	Notes     key.Binding
	Claude    key.Binding
	Settings  key.Binding

	// In-screen
	Up      key.Binding
	Down    key.Binding
	Left    key.Binding
	Right   key.Binding
	Enter   key.Binding
	Back    key.Binding
	Help    key.Binding
	Quit    key.Binding
	Refresh key.Binding

	// Session actions
	NewItem    key.Binding
	Kill       key.Binding
	Rename     key.Binding
	KeepAwake  key.Binding
	Snapshot   key.Binding
	OpenInApp  key.Binding
	EditInEd   key.Binding
}

// DefaultKeymap returns ccmux's canonical bindings. Vim-style and arrow keys
// both work. F-keys jump between screens.
func DefaultKeymap() Keymap {
	return Keymap{
		Dashboard: key.NewBinding(key.WithKeys("1", "f1"), key.WithHelp("1", "dashboard")),
		Sessions:  key.NewBinding(key.WithKeys("2", "f2"), key.WithHelp("2", "sessions")),
		Projects:  key.NewBinding(key.WithKeys("3", "f3"), key.WithHelp("3", "projects")),
		Notes:     key.NewBinding(key.WithKeys("4", "f4"), key.WithHelp("4", "notes")),
		Claude:    key.NewBinding(key.WithKeys("5", "f5"), key.WithHelp("5", "claude")),
		Settings:  key.NewBinding(key.WithKeys("6", "f6"), key.WithHelp("6", "settings")),

		Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Left:    key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "left")),
		Right:   key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "right")),
		Enter:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select")),
		Back:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:    key.NewBinding(key.WithKeys("ctrl+c", "q"), key.WithHelp("q", "quit")),
		Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),

		NewItem:   key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new")),
		Kill:      key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "kill")),
		Rename:    key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "rename")),
		KeepAwake: key.NewBinding(key.WithKeys("k"), key.WithHelp("k", "keep-awake")),
		Snapshot:  key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "snapshot")),
		OpenInApp: key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open")),
		EditInEd:  key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
	}
}
