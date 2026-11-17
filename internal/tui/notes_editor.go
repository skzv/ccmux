package tui

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/notes"
)

// createAndOpenNote writes the new file under the project root and
// opens it in $EDITOR. The body is `# {title}\n\n` when a title is
// supplied; otherwise the file is created empty. Errors (collision,
// permission, etc.) surface as toasts; success chains into
// openInEditor, which itself dispatches notesReloadMsg on editor
// close so the new file shows up in the list. created is the new note's
// vault-relative path ("" when nothing was created), so the listing
// after the editor closes can select it.
func (m notesModel) createAndOpenNote(filename, title string) (cmd tea.Cmd, created string) {
	if m.project == nil {
		return func() tea.Msg {
			return toastMsg{Text: tr("no project selected"), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}, ""
	}
	rel, err := notes.CleanRel(filename)
	if err != nil {
		msg := err.Error()
		return func() tea.Msg {
			return toastMsg{Text: msg, Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}, ""
	}
	full := filepath.Join(m.project.Path, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		msg := tr("mkdir failed: ") + err.Error()
		return func() tea.Msg {
			return toastMsg{Text: msg, Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}, ""
	}
	body := ""
	if title != "" {
		body = "# " + title + "\n\n"
	}
	// O_EXCL: creating and the already-exists check are one step, so a
	// note written in between (sync, another editor) is never clobbered.
	if err := writeNewFile(full, []byte(body)); err != nil {
		msg := tr("write failed: ") + err.Error()
		if errors.Is(err, os.ErrExist) {
			msg = tr("file already exists: ") + filename
		}
		return func() tea.Msg {
			return toastMsg{Text: msg, Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}, ""
	}
	return openInEditor(m.editor, full), rel
}

// writeNewFile creates path with data, failing with os.ErrExist if it
// already exists.
func writeNewFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// openInEditor returns a tea.Cmd that opens `path` in `editor` via
// tea.ExecProcess. The TUI is suspended for the editor's lifetime.
// Thin wrapper kept for callers in this file; the actual work lives
// in helpers.openEditorCmd so notes/claudeconfig/app all share one
// implementation.
func openInEditor(editor, path string) tea.Cmd {
	return openEditorCmd(editor, path, notesReloadMsg{})
}
