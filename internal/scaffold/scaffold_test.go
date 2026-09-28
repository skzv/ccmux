package scaffold

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tmux"
)

// hermeticHome redirects $HOME so nothing reads or writes real settings.
func hermeticHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestPrepareDir_NameRequired(t *testing.T) {
	if _, err := PrepareDir(Options{}); err == nil {
		t.Fatal("expected error for empty Name+Dir, got nil")
	}
}

// TestPrepareDir_CreatesDirectoryOnly is the core contract of the
// scaffolding removal: PrepareDir makes the project directory and
// NOTHING else — no docs/ tree, no CLAUDE.md, no README.md, no
// .gitignore, no git repo. Bootstrapping a project is the user's job.
func TestPrepareDir_CreatesDirectoryOnly(t *testing.T) {
	hermeticHome(t)
	target := filepath.Join(t.TempDir(), "myproj")
	dir, err := PrepareDir(Options{Name: "myproj", Dir: target})
	if err != nil {
		t.Fatalf("PrepareDir: %v", err)
	}
	if dir != target {
		t.Errorf("PrepareDir returned %q, want %q", dir, target)
	}
	if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
		t.Fatalf("project dir not created: %v", err)
	}
	// Nothing else may be written. These are exactly the files/dirs the
	// old scaffolder produced — their absence is the whole point.
	for _, forbidden := range []string{
		"CLAUDE.md", "README.md", ".gitignore", ".git",
		"docs", filepath.Join("docs", "01_Specs"),
		filepath.Join("docs", "02_Architecture"), filepath.Join("docs", "03_Agent_Logs"),
	} {
		if _, err := os.Stat(filepath.Join(target, forbidden)); err == nil {
			t.Errorf("PrepareDir created %q — project scaffolding should be fully removed", forbidden)
		}
	}
}

func TestPrepareDir_RelativeNameResolvesToCwd(t *testing.T) {
	hermeticHome(t)
	cwd := t.TempDir()
	t.Chdir(cwd)
	dir, err := PrepareDir(Options{Name: "relsub"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cwd, "relsub")
	if dir != want {
		t.Errorf("resolved %q, want %q", dir, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("did not create dir under cwd: %v", err)
	}
}

func TestPrepareDir_Idempotent(t *testing.T) {
	hermeticHome(t)
	target := filepath.Join(t.TempDir(), "p")
	if _, err := PrepareDir(Options{Name: "p", Dir: target}); err != nil {
		t.Fatal(err)
	}
	// A file the user put in the dir must survive a second PrepareDir.
	marker := filepath.Join(target, "user-file.txt")
	if err := os.WriteFile(marker, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareDir(Options{Name: "p", Dir: target}); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(marker); string(body) != "mine" {
		t.Errorf("PrepareDir disturbed an existing file: %q", body)
	}
}

// TestPrepareDir_KeepsExistingProjectsAgent — `ccmux new <existing>
// --agent X` rewrote the project's .ccmux/agent sidecar, silently and
// permanently switching the project's agent. An existing sidecar is
// left alone; a directory with none yet gets the chosen agent.
func TestPrepareDir_KeepsExistingProjectsAgent(t *testing.T) {
	hermeticHome(t)
	target := filepath.Join(t.TempDir(), "p")
	if err := project.SetAgent(target, agent.IDCodex); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareDir(Options{Name: "p", Dir: target, Agent: agent.IDClaude}); err != nil {
		t.Fatal(err)
	}
	if got := project.ReadAgent(target); got != agent.IDCodex {
		t.Errorf("existing codex project switched to %q", got)
	}

	// Existing directory, no recorded agent yet: the choice is recorded.
	bare := filepath.Join(t.TempDir(), "existing-repo")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareDir(Options{Name: "existing-repo", Dir: bare, Agent: agent.IDCodex}); err != nil {
		t.Fatal(err)
	}
	if got := project.ReadAgent(bare); got != agent.IDCodex {
		t.Errorf("unrecorded dir: ReadAgent = %q, want codex", got)
	}
}

// started is one session StartSession created through fakeTmux.
type started struct {
	session, launch, tag string
	dir                  string
}

// fakeTmux swaps StartSession's tmux calls for the duration of a test,
// recording every session it creates with its launch command and agent
// tag. The session list StartSession names against holds the sessions
// created so far, in their directories.
func fakeTmux(t *testing.T, newErr error) *[]started {
	t.Helper()
	var created []started
	origNew, origList := newSession, listSessions
	t.Cleanup(func() { newSession, listSessions = origNew, origList })
	newSession = func(_ context.Context, session, dir, launch, tag string) error {
		if newErr == nil {
			created = append(created, started{session: session, launch: launch, tag: tag, dir: dir})
		}
		return newErr
	}
	listSessions = func(context.Context) ([]tmux.Session, error) {
		out := make([]tmux.Session, 0, len(created))
		for _, c := range created {
			out = append(out, tmux.Session{Name: c.session, Path: c.dir})
		}
		return out, nil
	}
	return &created
}

// TestStartSession_SameFolderNameGetsItsOwnSession — two projects whose
// folders share a name both got c-api, so starting the second failed
// with "duplicate session" (or, through attach, landed in the first
// project's session). The first keeps c-api; the second gets a
// path-tagged session; starting either again reports it already runs,
// without touching its agent.
func TestStartSession_SameFolderNameGetsItsOwnSession(t *testing.T) {
	hermeticHome(t)
	created := fakeTmux(t, nil)
	base := t.TempDir()
	first, second := filepath.Join(base, "Projects", "api"), filepath.Join(base, "work", "api")

	s1, err := StartSession(context.Background(), Options{Name: "api", Dir: first})
	if err != nil || s1 != "c-api" {
		t.Fatalf("first project: %q, %v; want c-api", s1, err)
	}
	s2, err := StartSession(context.Background(), Options{Name: "api", Dir: second, Agent: agent.IDCodex})
	if err != nil || s2 != tmux.PathTaggedSessionName(second) {
		t.Fatalf("second project: %q, %v; want %s", s2, err, tmux.PathTaggedSessionName(second))
	}
	if len(*created) != 2 || (*created)[1].dir != second {
		t.Fatalf("sessions = %+v", *created)
	}

	for _, dir := range []string{first, second} {
		_, err := StartSession(context.Background(), Options{Name: "api", Dir: dir, Agent: agent.IDKimi})
		if !errors.Is(err, ErrAlreadyRunning) {
			t.Errorf("%s again: err = %v, want ErrAlreadyRunning", dir, err)
		}
	}
	if len(*created) != 2 {
		t.Errorf("a running project was started again: %+v", *created)
	}
	if got := project.ReadAgent(first); got != agent.IDClaude {
		t.Errorf("first project's agent = %q after a refused start, want claude (no sidecar)", got)
	}
	if got := project.ReadAgent(second); got != agent.IDCodex {
		t.Errorf("second project's agent = %q after a refused start, want codex", got)
	}

	// A caller that resolved the name itself (the daemon) gets it as is.
	s3, err := StartSession(context.Background(), Options{Name: "x", Dir: filepath.Join(base, "x"), Session: "given"})
	if err != nil || s3 != "given" {
		t.Errorf("explicit session: %q, %v; want given", s3, err)
	}
}

// TestStartSession_FailedStartLeavesAgentUnchanged — a sidecar recorded
// for this start is rolled back when the tmux session can't be created,
// so a failed `ccmux new` doesn't re-assign the project's agent.
func TestStartSession_FailedStartLeavesAgentUnchanged(t *testing.T) {
	hermeticHome(t)
	fakeTmux(t, errors.New("tmux: server exited"))
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := StartSession(context.Background(), Options{Name: "repo", Dir: dir, Agent: agent.IDCodex}); err == nil {
		t.Fatal("StartSession succeeded with a failing tmux")
	}
	if _, err := os.Stat(project.AgentSidecarPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("sidecar left behind after a failed start (err=%v); ReadAgent = %q", err, project.ReadAgent(dir))
	}

	// A pre-existing choice survives a failed start untouched.
	if err := project.SetAgent(dir, agent.IDKimi); err != nil {
		t.Fatal(err)
	}
	if _, err := StartSession(context.Background(), Options{Name: "repo", Dir: dir, Agent: agent.IDCodex}); err == nil {
		t.Fatal("StartSession succeeded with a failing tmux")
	}
	if got := project.ReadAgent(dir); got != agent.IDKimi {
		t.Errorf("ReadAgent after failed start = %q, want kimi", got)
	}
}

// TestStartSession_TagsSessionWithTheAgentItRuns — the project keeps
// its recorded agent, so a session started with a different one is
// pinned to what it runs (tmux @ccmux_agent) for the daemon's
// classifier; a session running the project's own agent is pinned to
// it too, so switching the project's agent later doesn't change how
// the running session is read.
func TestStartSession_TagsSessionWithTheAgentItRuns(t *testing.T) {
	hermeticHome(t)
	created := fakeTmux(t, nil)
	dir := filepath.Join(t.TempDir(), "proj")
	if err := project.SetAgent(dir, agent.IDCodex); err != nil {
		t.Fatal(err)
	}
	session, err := StartSession(context.Background(), Options{Name: "proj", Dir: dir, Agent: agent.IDClaude})
	if err != nil {
		t.Fatal(err)
	}
	if got := project.ReadAgent(dir); got != agent.IDCodex {
		t.Errorf("project agent switched to %q", got)
	}
	if len(*created) != 1 || (*created)[0].session != session || (*created)[0].tag != "claude" {
		t.Errorf("sessions = %+v, want %s tagged claude", *created, session)
	}

	*created = nil
	fresh := filepath.Join(t.TempDir(), "fresh")
	if _, err := StartSession(context.Background(), Options{Name: "fresh", Dir: fresh, Agent: agent.IDCodex}); err != nil {
		t.Fatal(err)
	}
	if got := project.ReadAgent(fresh); got != agent.IDCodex {
		t.Errorf("new project ReadAgent = %q, want codex", got)
	}
	if len(*created) != 1 || (*created)[0].tag != "codex" {
		t.Errorf("sessions = %+v, want one tagged codex", *created)
	}
}

// TestStartSession_EmptyAgentRunsTheProjectsAgent — with no agent
// named, StartSession launched Claude even in an existing project whose
// .ccmux/agent sidecar records another agent, so POST /v1/projects for
// an existing Codex project with no agent in the request (or `ccmux
// new` with no --agent and no default) ran Claude. The project's own
// agent must run, tagged as such; Claude only when there is no sidecar.
func TestStartSession_EmptyAgentRunsTheProjectsAgent(t *testing.T) {
	hermeticHome(t)
	created := fakeTmux(t, nil)
	codexProj := filepath.Join(t.TempDir(), "codexproj")
	if err := project.SetAgent(codexProj, agent.IDCodex); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(t.TempDir(), "plain")

	for _, tc := range []struct {
		dir  string
		want agent.ID
	}{{codexProj, agent.IDCodex}, {plain, agent.IDClaude}} {
		*created = nil
		opts := Options{Name: filepath.Base(tc.dir), Dir: tc.dir}
		if got := SessionAgent(opts, tc.dir); got != tc.want {
			t.Errorf("%s: SessionAgent = %q, want %q", tc.dir, got, tc.want)
		}
		if _, err := StartSession(context.Background(), opts); err != nil {
			t.Fatal(err)
		}
		wantLaunch := LaunchCmd(Options{Agent: tc.want})
		if len(*created) != 1 || (*created)[0].launch != wantLaunch || (*created)[0].tag != string(tc.want) {
			t.Errorf("%s: sessions = %+v, want launch %q tagged %s", tc.dir, *created, wantLaunch, tc.want)
		}
		if got := project.ReadAgent(tc.dir); got != tc.want {
			t.Errorf("%s: ReadAgent = %q, want %q", tc.dir, got, tc.want)
		}
	}
}

// TestPrepareDir_WritesAgentSidecar — PrepareDir records the chosen
// agent in ccmux's own .ccmux/agent sidecar (infrastructure, not
// project scaffolding) so the dashboard and attach path launch the
// right agent. An empty or bogus agent leaves no sidecar, and
// project.ReadAgent then falls back to Claude.
func TestPrepareDir_WritesAgentSidecar(t *testing.T) {
	cases := []struct {
		name string
		in   agent.ID
		want agent.ID
	}{
		{"claude", agent.IDClaude, agent.IDClaude},
		{"codex", agent.IDCodex, agent.IDCodex},
		{"antigravity", agent.IDAntigravity, agent.IDAntigravity},
		{"empty falls back to claude", "", agent.IDClaude},
		{"unknown falls back to claude", agent.ID("imaginary"), agent.IDClaude},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hermeticHome(t)
			target := filepath.Join(t.TempDir(), "p")
			if _, err := PrepareDir(Options{Name: "p", Dir: target, Agent: tc.in}); err != nil {
				t.Fatal(err)
			}
			if got := project.ReadAgent(target); got != tc.want {
				t.Errorf("ReadAgent = %q, want %q", got, tc.want)
			}
		})
	}
}
