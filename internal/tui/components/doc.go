// Package components contains the shared render helpers every primary
// navigation screen routes its frame through: Header (top bar),
// HelpBar (footer key hints), and List (selectable rows).
//
// Dependency rule: components → styles, ONLY. No screen or daemon or
// project import. The helpers are stateless pure functions that take a
// styles.Styles value and a props struct, and return a string. Screens
// own selection state and call into these helpers each render frame.
//
// This package together with internal/tui/styles/ is the only place
// where inline lipgloss style construction is allowed inside the TUI;
// every other file under internal/tui/ MUST consume from these two
// packages — see TestNoInlineStyleLiteralsInScreens.
package components
