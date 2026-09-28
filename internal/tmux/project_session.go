package tmux

import (
	"context"
	"path/filepath"
)

// A project's session is named after its directory's basename
// (SessionNameForPath), so two projects in different directories with
// the same basename — ~/Projects/api and ~/work/api, a --projects
// override root next to the default one — both map to c-api. Naming by
// basename alone meant opening the second project attached you to the
// first project's session, running in the wrong directory.
//
// ProjectSessionName settles that without renaming anyone's session:
// the plain name stays with whichever directory already has it, and a
// project whose plain name is taken by a session running somewhere else
// gets the plain name plus a tag of its full path
// (c-api-<five letters>). Every place that turns a project directory
// into a session name — the CLI's attach/new/kill, the TUI's project
// open and new-session, the daemon's POST /v1/sessions and
// /v1/projects, scaffold.StartSession — resolves through here, so they
// all agree on which session is which project's.

// ProjectSessionName returns the tmux session for the project in dir,
// given the sessions on the tmux server (List's result). running
// reports whether that session already exists and runs in dir — attach
// to it — rather than being the name to create one under.
//
// In order:
//
//  1. the plain name (SessionNameForPath), when it runs in dir: the
//     usual case, and every session made before path tags existed;
//  2. a path-tagged session (plain + "-" + five letters) running in
//     dir, so a project that got a tagged session keeps attaching to
//     it after the plain name frees up;
//  3. the plain name, when no session has it;
//  4. the path-tagged name (PathTaggedSessionName), when the plain name
//     belongs to a session in another directory.
//
// A session "runs in dir" when its session_path is the same directory
// (SamePath: symlinks resolved). For the plain name, a session whose
// path tmux didn't report counts too: nothing shows it is another
// project's, so it keeps the name-only behaviour ccmux always had.
//
// In case 4 the tagged name can itself be taken by a session somewhere
// else (a user renamed a session to it, or two paths' tags collide);
// running is false, and creating it fails with tmux's "duplicate
// session" — the daemon answers 409 — rather than attaching to a
// stranger's session.
func ProjectSessionName(sessions []Session, dir string) (name string, running bool) {
	plain := SessionNameForPath(dir)
	tagged := PathTaggedSessionName(dir)
	var plainTaken bool
	var taggedHere, otherTaggedHere string
	for _, s := range sessions {
		switch {
		case s.Name == plain:
			if s.Path == "" || SamePath(s.Path, dir) {
				return plain, true
			}
			plainTaken = true
		case !isPathTagged(s.Name, plain):
		case s.Path == "" || !SamePath(s.Path, dir):
			// Only a session known to run in dir can be its tagged one.
		case s.Name == tagged:
			taggedHere = tagged
		case otherTaggedHere == "":
			// Tagged from another spelling of the same directory (a
			// symlinked root, a path that didn't resolve then).
			otherTaggedHere = s.Name
		}
	}
	switch {
	case taggedHere != "":
		return taggedHere, true
	case otherTaggedHere != "":
		return otherTaggedHere, true
	case !plainTaken:
		return plain, false
	}
	return tagged, false
}

// ResolveProjectSession is ProjectSessionName against the live tmux
// server. An error means tmux couldn't list its sessions (no server at
// all is not an error: every name is free).
func ResolveProjectSession(ctx context.Context, dir string) (name string, running bool, err error) {
	sessions, err := List(ctx)
	if err != nil {
		return "", false, err
	}
	name, running = ProjectSessionName(sessions, dir)
	return name, running, nil
}

// PathTaggedSessionName is the session name a project in dir gets when
// its plain name (SessionNameForPath) is held by a session running in
// another directory: the plain name plus "-" and a five-letter tag of
// dir's full path (symlinks resolved when dir exists, so every
// spelling of one directory gets one tag). Letters only, like
// sessionNameTag's other use, so it never reads as the numeric -2, -3
// ccmux gives a project's additional sessions.
func PathTaggedSessionName(dir string) string {
	return SessionNameForPath(dir) + "-" + sessionNameTag(canonicalDir(dir))
}

// IsPathTagged reports whether name has the shape of a path-tagged
// session of the project whose plain session name is plain: plain +
// "-" + five lowercase letters. Whether it is that project's depends
// on the directory it runs in (ProjectSessionName checks both).
func IsPathTagged(name, plain string) bool { return isPathTagged(name, plain) }

// isPathTagged is IsPathTagged.
func isPathTagged(name, plain string) bool {
	if len(name) != len(plain)+6 || name[:len(plain)] != plain || name[len(plain)] != '-' {
		return false
	}
	for i := len(plain) + 1; i < len(name); i++ {
		if name[i] < 'a' || name[i] > 'z' {
			return false
		}
	}
	return true
}

// canonicalDir is dir as an absolute, cleaned path with symlinks
// resolved. A directory that doesn't exist yet (`ccmux new` names the
// session it is about to create) has its deepest existing ancestor
// resolved and the rest appended, so its tag doesn't change once it
// exists — /tmp/x/api is /private/tmp/x/api on macOS either way.
func canonicalDir(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	dir = filepath.Clean(dir)
	rest := ""
	for p := dir; ; {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return dir
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// SamePath reports whether two directory paths name the same directory.
// tmux records a session's start directory as it was given, so the
// same directory can come back spelled another way (a symlinked
// projects root, macOS's /tmp → /private/tmp).
func SamePath(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
