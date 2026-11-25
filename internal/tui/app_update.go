package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/ccusage"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tmux"
)

// Update routes messages to the active screen and handles global keys.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.matrix.SetSize(msg.Width, msg.Height)
		a.notes.SetSize(msg.Width, msg.Height)
		// The Agents screen's embedded browsers hold persistent
		// viewports; push the body rectangle they'll be rendered
		// into so scrolling clamps against the real pane size.
		a.agentsM.SetSize(msg.Width, a.screenBodyHeight())
		return a, nil

	case matrixTickMsg:
		// Drive the easter-egg animation. When the matrix has been
		// dismissed since the last frame, swallow the message without
		// scheduling another tick.
		if !a.matrix.Active() {
			return a, nil
		}
		var cmd tea.Cmd
		a.matrix, cmd = a.matrix.Update(msg)
		return a, cmd

	case openSSHWizardMsg:
		// The Network screen ('s' key) and the post-attach probe
		// emit this to ask the App to open the wizard for a
		// specific target. We instantiate-on-demand because the
		// wizard model is heavyish and zero apps need it on first
		// frame.
		if a.sshWizard == nil {
			a.sshWizard = newSSHWizard(a.styles)
		}
		a.sshWizard.Open(msg.target, msg.resume)
		return a, nil

	case wizardProgressMsg, wizardInstallDoneMsg, wizardEnumerateDoneMsg, wizardProbeDoneMsg:
		// Internal wizard machinery — route to the wizard, ignore
		// when no wizard is alive (defensive against stale Cmds).
		if a.sshWizard == nil {
			return a, nil
		}
		var cmd tea.Cmd
		a.sshWizard, cmd = a.sshWizard.Update(msg)
		return a, cmd

	case wizardCompletedMsg:
		// Wizard finished successfully. Save a corrected user / SSH
		// port back to the host it ran for, then persist any
		// user@host pairs the user selected on the enumerate step.
		a = persistWizardCorrection(a, msg.original, msg.target)
		if len(msg.added) > 0 {
			a = persistWizardAdded(a, msg.target, msg.added)
		}
		// If the user reached the wizard while trying to GET INTO a
		// device (Network-tab Enter / `s`), finish the job: drop
		// them into the ssh shell now that auth is set up. Without
		// this the wizard installed the key and then stranded the
		// user on the Network screen.
		if target, ok := shouldOpenShell(msg.resume, msg.target); ok {
			return a, sshShellExec(target)
		}
		until := time.Now().Add(4 * time.Second)
		return a, func() tea.Msg {
			return toastMsg{
				Text:  tr("SSH ready for ") + msg.target.String(),
				Kind:  toastInfo,
				Until: until,
			}
		}

	case wizardCancelledMsg:
		// User bailed — no toast needed unless we were
		// auto-triggered by an attach failure (resume != nil).
		// The wizard is already closed by the time we get here.
		return a, nil

	case tickMsg:
		cmds := []tea.Cmd{tickEvery(2 * time.Second)}
		// Don't stack refreshes: with a slow host a refresh can outlive
		// the 2s tick, and issuing another every tick piled them up.
		// Skip while the previous tick's refresh is pending — unless
		// it's overdue, so a lost result can't stall polling for good.
		if a.sessionsTickGen == 0 || time.Since(a.sessionsTickAt) > sessionsTickStaleAfter {
			cmds = append(cmds, a.refreshSessionsCmd())
			a.sessionsTickGen = a.sessionsLoadGen
			a.sessionsTickAt = time.Now()
		}
		// Keep the Settings screen's moshi status fresh while it's
		// focused — async, so the 30s refresh never blocks the UI
		// goroutine the way the old inline moshi.Detect did.
		if a.screen == ScreenSettings && a.settings.MoshiStale() {
			cmds = append(cmds, detectMoshiCmd())
		}
		return a, tea.Batch(cmds...)

	case usageTickMsg:
		return a, tea.Batch(a.refreshUsageCmd(), usageTick())

	case openConversationsForProjectMsg:
		a.screen = ScreenConversations
		a.conversationsM.SetProjectFilter(msg.Project)
		a.conversationsM.FocusAgent(msg.Agent)
		a.conversationsM.SetLoading(true)
		return a, tea.Batch(a.refreshConversationsCmd(), a.conversationsM.SpinnerTickCmd())

	case conversationsLoadedMsg:
		if msg.Gen != a.convLoadGen {
			return a, nil // superseded by a newer refresh
		}
		if msg.Err != nil {
			a.conversationsM.SetLoadErr(msg.Err.Error())
			return a, nil
		}
		a.conversationsM.SetList(msg.List)
		// Kick off the lazy stats load for the row the cursor landed
		// on so the detail pane's "messages N" populates without
		// waiting for the next keystroke.
		return a, a.conversationsM.LoadStatsCmd()

	case conversationStatsLoadedMsg:
		if msg.Err != nil {
			// On error, plant a sentinel so we don't re-fire the load on
			// every cursor move. The pane will silently skip the row.
			a.conversationsM.SetMessageCount(msg.ID, -1)
			return a, nil
		}
		a.conversationsM.SetMessageCount(msg.ID, msg.Count)
		return a, nil

	case conversationPreviewLoadedMsg:
		if msg.Err != nil {
			a.convPreview.SetLoadErr(msg.ID, msg.Err.Error())
			return a, nil
		}
		a.convPreview.SetMessages(msg.ID, msg.Messages)
		return a, nil

	case updateCheckMsg:
		// A failed check (no checkout, no upstream, offline) is not an
		// error worth surfacing — it just means "can't tell." Silently
		// drop it; the dashboard shows no banner.
		if msg.Err == nil && msg.Result.Available() {
			a.dashboard.SetUpdateAvailable(msg.Result)
		}
		return a, nil

	case tierDetectedMsg:
		// Async result of detectTierCmd. Only adopt a detected tier when
		// the user hasn't declared one (tier unset in config.toml) — a
		// set tier, including an explicit "api", always wins. Push the
		// updated config into the screens that render it.
		// Writes through SetTierFor("claude", …) so the per-agent map
		// + the legacy Tier field stay in sync.
		//
		// The detected tier is display-only: it lives in
		// detectedClaudeTier and is overlaid on each adopted config,
		// never saved, so a later detection (or a plan change) can't be
		// frozen into config.toml.
		a.detectedClaudeTier = msg.Tier
		if a.overlayDetectedTier(&a.cfg) {
			a.dashboard.SetConfig(a.cfg)
			a.settings.SetConfig(a.cfg)
		}
		return a, nil

	case moshiDetectedMsg:
		// Async result of detectMoshiCmd — push it into the Settings
		// screen, which renders the moshi/moshi-hook status block.
		// Probe is now finished, so do not re-arm the spinner ticker.
		a.settings.SetMoshiState(msg.State)
		return a, nil

	case spinner.TickMsg:
		// Route every spinner tick globally, fanning it out to every
		// model that owns a spinner: Settings (Moshi probe), Projects
		// (initial discovery), Network (per-row probe), Conversations
		// (loading walk), Notes (vault load), and Agents (the Cursor
		// sub-tab's SQLite read). Each spinner's loop is keyed by ID,
		// so a model ignores ticks that aren't its own — fanning out
		// is safe and keeps every spinner animating even when the
		// user navigates to another screen mid-probe. This case
		// returns early, so any model left out of the fan-out has its
		// spinner frozen on frame 0 (the Conversations/Notes/Cursor
		// bug) — add new spinner owners here.
		var scmd, pcmd, ncmd, ccmd, nocmd, acmd tea.Cmd
		a.settings, scmd = a.settings.Update(msg)
		a.projectsM, pcmd = a.projectsM.Update(msg)
		a.network, ncmd = a.network.Update(msg)
		a.conversationsM, ccmd = a.conversationsM.Update(msg)
		a.notes, nocmd = a.notes.Update(msg)
		a.agentsM, acmd = a.agentsM.Update(msg)
		return a, tea.Batch(scmd, pcmd, ncmd, ccmd, nocmd, acmd)

	case conversationDeletedMsg:
		if msg.Err != nil {
			return a, func() tea.Msg {
				return toastMsg{
					Text:  tr("delete failed: ") + msg.Err.Error(),
					Kind:  toastError,
					Until: time.Now().Add(5 * time.Second),
				}
			}
		}
		// Transcript removed — refresh the list so the row disappears,
		// and toast the confirmation.
		a.conversationsM.SetLoading(true)
		return a, tea.Batch(
			a.refreshConversationsCmd(),
			a.conversationsM.SpinnerTickCmd(),
			func() tea.Msg {
				return toastMsg{
					Text:  fmt.Sprintf(tr("deleted %s conversation %s"), msg.Agent, shortConversationID(msg.ID)),
					Kind:  toastSuccess,
					Until: time.Now().Add(4 * time.Second),
				}
			},
		)

	case conversationResumedMsg:
		if msg.Err != nil {
			// Resume failed before we even started attaching — wipe
			// the overlay (it was switched on the moment the user
			// pressed Enter so the spawn latency was covered).
			a.stopAttaching()
			return a, func() tea.Msg {
				return toastMsg{
					Text:  tr("resume failed: ") + msg.Err.Error(),
					Kind:  toastError,
					Until: time.Now().Add(5 * time.Second),
				}
			}
		}
		// Spawn was successful; attach to the new tmux session and
		// refresh Sessions so the row shows up immediately on return.
		// Re-label the overlay now that we know the destination
		// session name (the resume cmd builds c-resume-<id>).
		label := ""
		if msg.Project != "" {
			label = filepath.Base(msg.Project)
		}
		a.attach.label = label
		if a.attach.label == "" {
			a.attach.label = msg.Session
		}
		// A session this resume just created is opened in mirror mode;
		// an already-running one (resumed before) is an ordinary
		// existing-session attach and honors sessions.attach_mode.
		attach := a.localNewSessionAttachCmd(msg.Session, label)
		if msg.Existing {
			attach = a.localAttachCmd(msg.Session, label)
		}
		return a, tea.Batch(
			attach,
			a.refreshSessionsCmd(),
			func() tea.Msg {
				return toastMsg{
					Text:  fmt.Sprintf(tr("resumed %s conversation in %s"), msg.Agent, msg.Session),
					Kind:  toastSuccess,
					Until: time.Now().Add(4 * time.Second),
				}
			},
		)

	case usageLoadedMsg:
		if msg.Err == nil && msg.Agg != nil {
			a.dashboard.SetUsage(msg.Agg)
		}
		// Codex and Antigravity summaries are pushed unconditionally —
		// today they're always empty (stub walkers) but the dashboard
		// uses HasData to decide between "real numbers" and "install
		// hint" rendering.
		a.dashboard.SetCodexUsage(msg.Codex)
		a.dashboard.SetAntigravityUsage(msg.Antigravity)
		a.dashboard.SetOtherUsage(msg.Others)
		a.dashboard.SetCcusageBlock(msg.CcusageBlock)
		a.dashboard.SetOpenRouterSpend(msg.OpenRouter)
		return a, nil

	case sessionsLoadedMsg:
		if msg.Gen != 0 {
			// This result (or a newer one) settles the tick's refresh.
			if a.sessionsTickGen != 0 && msg.Gen >= a.sessionsTickGen {
				a.sessionsTickGen = 0
			}
			// Refreshes finish out of order when a host is slow; never
			// let an older list overwrite a newer one.
			if msg.Gen < a.sessionsAppliedGen {
				return a, nil
			}
			a.sessionsAppliedGen = msg.Gen
		}
		a.lastRefresh = msg.At
		a.sessions = msg.Sessions
		a.hosts = msg.Hosts
		a.daemonOnline = daemonOnline(msg.Hosts)
		a.dashboard.SetSessions(a.sessions)
		a.dashboard.SetHosts(a.hosts)
		a.network.SetHosts(a.hosts)
		a.projectsM.SetHosts(a.hosts)
		a.projectsM.SetSessions(a.sessions)
		a.sessionsM.SetHosts(a.hosts)
		a.notes.SetHosts(a.hosts)
		a.sessionsM.SetDefaultDir(a.cfg.Sessions.DefaultDir)
		a.sessionsM.SetDefaultAgent(a.cfg.Agents.Default)
		a.sessionsM.SetAgentCommands(a.cfg.AgentCommands())
		a.projectsM.SetDefaultAgent(a.cfg.Agents.Default)
		a.projectsM.SetAgentCommands(a.cfg.AgentCommands())
		a.dashboard.SetVersion(a.version)
		// The list may have moved the selection (a kill, a session that
		// ended): the preview pane re-captures the new one right away.
		previewCmd := a.sessionsM.SetSessions(a.sessions)
		if msg.Err != nil {
			a.toasts.Set(toastError, tr("refresh: ")+msg.Err.Error(), 5*time.Second)
		}
		return a, previewCmd

	case notesProjectsWantedMsg:
		if len(a.projects) > 0 {
			a.notes.SetProjects(a.projects)
			return a, nil
		}
		return a, a.refreshProjectsCmd()

	case projectsLoadedMsg:
		if msg.Err != nil {
			a.notes.ProjectsLoadFailed()
		}
		if msg.Err == nil {
			// Like sessions: never let an older refresh that finished
			// late replace a newer list. Gen 0 is unnumbered.
			if msg.Gen != 0 {
				if msg.Gen < a.projectsAppliedGen {
					return a, nil
				}
				a.projectsAppliedGen = msg.Gen
			}
			a.projects = msg.Projects
			a.projectsM.SetProjects(a.projects)
			// Notes screen needs the full list for its project picker.
			a.notes.SetProjects(a.projects)
		}
		return a, nil

	case toastMsg:
		// An error toast arriving while we're attaching almost
		// always *is* the attach reporting failure (tmux.New
		// errored, remote daemon rejected, etc.) — clear the
		// overlay so the user can see the toast underneath
		// instead of staring at "Opening …" indefinitely.
		if msg.Kind == toastError && a.attach.active {
			a.stopAttaching()
		}
		a.toasts.Set(msg.Kind, msg.Text, time.Until(msg.Until))
		return a, nil

	case projectMenuMsg:
		// A project was opened — show its running sessions and past
		// conversations so the user can attach, resume, or start new.
		// Clear the "Opening …" overlay that the Enter-on-Projects
		// path flipped on, otherwise the modal would render under it.
		a.stopAttaching()
		menu := newProjectMenu(a.styles, msg.Project, msg.ProjectPath, msg.Sessions, msg.Conversations)
		a.projectsM.menu = &menu
		return a, nil

	case projectMenuPickMsg:
		a.projectsM.menu = nil
		switch msg.Entry.kind {
		case menuSession:
			// Attach to the chosen running session.
			tick := a.startAttaching(attachKindAttach, msg.Project)
			return a, tea.Batch(tick, a.localAttachCmd(msg.Entry.session.Name, msg.Project))
		case menuConversation:
			// Resume the chosen past conversation in a fresh session.
			// Flip the overlay now — the resume cmd's tmux.New can
			// take several seconds while the agent boots, and the
			// user shouldn't see the regular TUI in the meantime.
			tick := a.startAttaching(attachKindResume, msg.Project)
			return a, tea.Batch(tick, a.resumeConversationCmd(msg.Entry.conv))
		case menuNewSession:
			// Start a new session for the project. The launch command
			// comes from the project's .ccmux/agent sidecar so an
			// Antigravity/Codex project doesn't silently boot claude.
			projectPath := msg.ProjectPath
			projectLabel := msg.Project
			launch := launchCmdForProjectPathWithCommands(projectPath, a.cfg.AgentCommands())
			tick := a.startAttaching(attachKindNew, projectLabel)
			return a, tea.Batch(tick, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				// Name it as the project's own session (c-<project>, or
				// its path-tagged name when a same-named project
				// elsewhere holds c-<project>: tmux.ProjectSessionName),
				// uniquified when that is already taken — the menu's
				// "new" is explicitly a second session alongside any
				// existing one.
				sessions, err := projectTmuxList(ctx)
				if err != nil {
					return toastMsg{Text: tr("start session: ") + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
				}
				name, _ := tmux.ProjectSessionName(sessions, projectPath)
				name = uniqueSessionName(sessions, name)
				if err := projectTmuxNew(ctx, name, projectPath, launch); err != nil {
					return toastMsg{Text: tr("start session: ") + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
				}
				return projectSessionReadyMsg{Session: name, Project: projectLabel}
			})
		}
		return a, nil

	case projectMenuCancelMsg:
		a.projectsM.menu = nil
		return a, nil

	case renameSessionSubmitMsg:
		a.sessionsM.renameForm = nil
		// Route by the row's host: a remote session is renamed through
		// its own daemon, never on the local tmux server.
		return a, a.renameSessionTargetCmd(msg.Host, msg.OldName, msg.NewName)

	case renameSessionCancelMsg:
		a.sessionsM.renameForm = nil
		return a, nil

	case sessionRenamedMsg:
		refresh := a.refreshSessionsCmd()
		if msg.Err != nil {
			a.toasts.Set(toastError, tr("rename failed: ")+msg.Err.Error(), 5*time.Second)
		} else {
			a.toasts.Set(toastSuccess, fmt.Sprintf(tr("renamed %s → %s"), sessionDisplayName(hostLabel(msg.Host, a.hosts), msg.OldName), msg.NewName), 3*time.Second)
			// Keep the cursor on the renamed row: the refresh below
			// finds the selection by name, which just changed.
			a.sessionsM.RenameSession(msg.Host, msg.OldName, msg.NewName)
			// Refreshes already in flight may have listed the sessions
			// before the rename; applied late (a slow host holds them
			// up) they put the old name back, the cursor followed it,
			// and the next list lost it. Only the refresh started just
			// now, or a later one, may replace the list.
			a.sessionsAppliedGen = a.sessionsLoadGen
		}
		return a, refresh

	case remoteSessionStartedMsg:
		// Remote daemon already created the tmux session for us;
		// reuse the same ssh-into-peer path the Sessions screen uses
		// for discovered hosts (PATH prepend + login shell, etc.).
		if msg.DialHost == "" {
			a.stopAttaching()
			a.toasts.Set(toastError, tr("remote session created but no dial host known"), 5*time.Second)
			return a, nil
		}
		c, target, remoteCmd := remoteNewSessionAttachProcess(msg)
		if msg.Mosh {
			if dbg := debugLogger(); dbg != nil {
				dbg.Printf("remote attach: mosh %s -- bash -c %q", target, remoteCmd)
			}
		} else {
			if dbg := debugLogger(); dbg != nil {
				dbg.Printf("remote attach: ssh -t %s %q", target, remoteCmd)
			}
		}
		// Flip the overlay if a producer earlier in the flow didn't
		// already do it. attachExitedMsg is the centralized cleanup
		// path (replaces the old direct refreshAfterDetachMsg).
		//
		// RemoteSSHTarget is populated so that an auth failure here
		// (the user is creating a session on a host whose key isn't
		// installed yet) routes to the SSH setup wizard instead of
		// silently flashing the openssh error.
		port := msg.SSHPort
		if port == 0 {
			port = 22
		}
		rt := &attachRemoteTarget{User: msg.User, Host: msg.DialHost, Port: port}
		label := "ssh " + target
		if msg.Mosh {
			label = "mosh " + target
		}
		if !a.attach.active {
			tick := a.startAttaching(attachKindRemote, msg.DialHost)
			return a, tea.Batch(tick, execAttach(c, label, rt, false))
		}
		return a, execAttach(c, label, rt, false)

	case newBareSessionSubmitMsg:
		// Close the form immediately — the form's own sessionsModel.Update
		// never runs this message (the type-switch in App.Update intercepts
		// it first), so we must nil it here.
		a.sessionsM.form = nil
		// Route to the right host: local → tmux.New + localAttachCmd;
		// remote → POST /v1/sessions/bare → ssh-attach via remoteSessionStarted.
		return a, spawnBareSessionCmd(msg)

	case bareSessionReadyMsg:
		// Local-bare-session creation finished. Attach via the same
		// path the new-project flow uses, so the nested-tmux case
		// (running ccmux inside the outer ccmux on mobile) handles
		// switch-client correctly. projectLabel is the session name
		// here — bare sessions don't have a richer label.
		//
		// If the spawn path didn't already flip the overlay (e.g. a
		// remote-bare flow that bounced through bareSessionReadyMsg),
		// flip it now so the user sees the loading screen during the
		// Moshi-detect + chrome-apply prep.
		if !a.attach.active {
			tick := a.startAttaching(attachKindAttach, msg.Session)
			return a, tea.Batch(tick, a.localNewSessionAttachCmd(msg.Session, msg.Session))
		}
		a.attach.label = msg.Session
		return a, a.localNewSessionAttachCmd(msg.Session, msg.Session)

	case projectSessionReadyMsg:
		// New project is scaffolded and its tmux session is running.
		// Route through localAttachCmd so the nested-tmux case (ccmux
		// running inside the outer ccmux session on mobile) uses
		// switch-client instead of attach-session — otherwise tmux
		// refuses the nested attach and the user just stares at the
		// Projects screen wondering why nothing happened.
		if !a.attach.active {
			tick := a.startAttaching(attachKindNew, msg.Project)
			return a, tea.Batch(tick, a.localNewSessionAttachCmd(msg.Session, msg.Project))
		}
		a.attach.label = msg.Project
		return a, a.localNewSessionAttachCmd(msg.Session, msg.Project)

	case sessionKilledMsg:
		if msg.Err != nil {
			a.toasts.Set(toastError, tr("kill failed: ")+msg.Err.Error(), 5*time.Second)
		} else {
			a.toasts.Set(toastSuccess, fmt.Sprintf(tr("killed %s"), sessionDisplayName(hostLabel(msg.Host, a.hosts), msg.Name)), 3*time.Second)
		}
		return a, a.refreshSessionsCmd()

	case openEditorMsg:
		// A screen (Notes or Settings) asked the app to suspend and run
		// $EDITOR. Route the follow-up reload by Source so the right
		// screen refreshes when control returns.
		return a, openEditorCmd(msg.Editor, msg.Path, editorReloadMsg(msg.Source))

	case agentsReloadMsg:
		// An agent's config file was edited in $EDITOR from the Agents
		// tab (Codex, Antigravity, Gemini) — re-read them all.
		a.agentsM.Reload()
		return a, nil

	case configReloadMsg:
		// User finished editing ~/.config/ccmux/config.toml in $EDITOR.
		// Re-read it and push the new shape into every screen that
		// holds a cached copy. Errors surface as a toast — the previous
		// in-memory config stays in place so the TUI doesn't go blank.
		if cfg, err := config.Load(); err != nil {
			a.toasts.Set(toastError, tr("reload config: ")+err.Error(), 5*time.Second)
		} else {
			a.startupConfigErr = nil
			cmd := a.adoptConfig(cfg)
			a.toasts.Set(toastSuccess, tr("config reloaded"), 2*time.Second)
			return a, cmd
		}
		return a, nil

	case configSavedMsg:
		// A screen persisted a change via config.Update. Adopt the saved
		// state so every screen — and any later save — sees it.
		return a, a.adoptConfig(msg.Cfg)

	case refreshAfterDetachMsg:
		// Returning from tmux attach. Also clears the loading overlay
		// in case the suspend-resume path skipped attachExitedMsg
		// (older remote paths still emit refreshAfterDetachMsg
		// directly from their tea.ExecProcess callbacks).
		a.stopAttaching()
		return a, tea.Batch(a.refreshSessionsCmd(), a.refreshProjectsCmd())

	case attachReadyMsg:
		// Prep finished — actually suspend Bubble Tea and exec into
		// tmux. The overlay stays drawn through the suspend; on the
		// way back, the callback's attachExitedMsg clears it.
		if msg.Nested {
			return a, execAttach(tmux.SwitchClientCmd(msg.Session), "tmux switch-client -t "+msg.Session, nil, false)
		}
		return a, execAttach(tmux.AttachCmd(msg.Session, msg.DetachOthers), "tmux attach -t "+msg.Session, nil, false)

	case attachExitedMsg:
		// Tmux exited (user detached, or the exec itself failed).
		// Clear the overlay, surface an error toast if any, refresh.
		// Bubble Tea turned mouse reporting off to hand the terminal
		// over and doesn't turn it back on when it takes it back, so
		// re-enable it — otherwise the wheel is dead after any attach.
		a.stopAttaching()
		cmds := []tea.Cmd{tea.EnableMouseCellMotion, a.refreshSessionsCmd(), a.refreshProjectsCmd()}
		if msg.Err != nil {
			// If the failure smells like SSH auth (ssh / mosh
			// exit 255 with "permission denied" in the stderr-as-
			// error), and we have an SSH target the user just
			// tried to attach to, offer the wizard instead of a
			// flat error toast. This is the "don't fail silently"
			// payoff — instead of leaving the user staring at
			// "Permission denied (publickey)" they get a 30-sec
			// password-once flow.
			if target := remoteAttachTargetFromErr(msg); target != nil {
				// Carry the shell-vs-attach intent so a successful
				// setup finishes the right job: Network-tab Enter
				// opens a shell; a session attach just signals ready.
				resume := sshWizardResume{OpenShell: msg.RemoteSSHTarget.OpenShellOnSetup}
				return a, tea.Batch(tea.EnableMouseCellMotion, func() tea.Msg {
					return openSSHWizardMsg{target: *target, resume: resume}
				})
			}
			until := time.Now().Add(5 * time.Second)
			text := attachFailureText(msg)
			cmds = append(cmds, func() tea.Msg {
				return toastMsg{Text: text, Kind: toastError, Until: until}
			})
		}
		return a, tea.Batch(cmds...)

	case attachSpinTickMsg:
		// Animate the overlay's spinner. Stop ticking once the
		// overlay is no longer active so we don't leak ticks.
		if !a.attach.active {
			return a, nil
		}
		a.attach.spinFrame++
		return a, attachSpinTickCmd()

	case previewLoadedMsg, previewTickMsg:
		// Session-preview pane plumbing. These are NOT key messages, so
		// they must be handled as their own top-level cases (every
		// other custom msg in this switch is). Routing them to
		// sessionsM here — rather than relying on the screen-forward
		// tail below — keeps the once-a-second tick chain alive even
		// when the user has navigated away from the Sessions screen
		// (the preview stays warm so re-entering shows live content,
		// not a stale frame). sessionsM.Update self-terminates the
		// chain when the user toggles the pane off, so this is not a
		// leak. Returning the cmd directly (instead of letting the
		// screen-forward switch overwrite it) is what previously
		// dropped the next tick when another screen was active.
		var cmd tea.Cmd
		a.sessionsM, cmd = a.sessionsM.Update(msg)
		return a, cmd

	case notesEntriesLoadedMsg, notesPreviewLoadedMsg, notesSearchResultMsg:
		// Results of a Notes load. Like the preview plumbing above they
		// go to their owner whatever screen is active: forwarded only to
		// the active screen, a listing that finished after the user
		// pressed another digit was dropped and Notes spun forever.
		var cmd tea.Cmd
		a.notes, cmd = a.notes.Update(msg)
		return a, cmd

	case cursorLoadedMsg:
		// The Cursor sub-tab's SQLite read — same off-screen routing.
		var cmd tea.Cmd
		a.agentsM, cmd = a.agentsM.Update(msg)
		return a, cmd

	case claudeModelChangedMsg:
		// The Agents tab's model pick also pinned the model in
		// config.toml. Adopt the saved config: every TUI launch reads
		// ANTHROPIC_MODEL from a.cfg, which otherwise kept the old pin
		// until restart. Then let the Agents tab reload and toast.
		var adopt tea.Cmd
		if msg.Cfg != nil {
			adopt = a.adoptConfig(*msg.Cfg)
		}
		var cmd tea.Cmd
		a.agentsM, cmd = a.agentsM.Update(msg)
		return a, tea.Batch(adopt, cmd)

	case claudeEffortChangedMsg, claudeAlwaysThinkingChangedMsg, claudeYoloChangedMsg, claudeReloadMsg:
		// Async Claude config saves can land after the user switches
		// screens or sub-tabs — route globally so toasts and state
		// reloads are never silently dropped.
		var cmd tea.Cmd
		a.agentsM, cmd = a.agentsM.Update(msg)
		return a, cmd

	case projectAgentSwitchedMsg:
		// Applied straight to the list: neither another active screen
		// nor a form open over Projects may swallow it.
		a.projectsM.SetProjectAgent(msg.Path, msg.Agent)
		return a, nil

	case tea.MouseMsg:
		if a.confirm.open() {
			return a.updateConfirmationMouse(msg)
		}
		// Never let mouse wheel events bleed through active overlays
		// and scroll Notes/Agents underneath.
		if a.attach.active || a.tour.Active() || a.helpOpen || a.usageOpen ||
			a.convPreview.IsOpen() || a.projectInfoOpen || a.settingsInfoOpen ||
			a.network.DetailOpen() || a.matrix.Active() || (a.sshWizard != nil && a.sshWizard.Active()) {
			return a, nil
		}
		// Forward wheel events to the active screen so its
		// scrollable regions (notes preview, agents browser preview,
		// etc.) can respond. Non-wheel mouse events are intentionally
		// swallowed — clicks/drags don't drive navigation here, and
		// absorbing them keeps the rest of the app pointer-free.
		if isWheelMsg(msg) {
			var cmd tea.Cmd
			switch a.screen {
			case ScreenNotes:
				a.notes, cmd = a.notes.Update(msg)
			case ScreenAgents:
				a.agentsM, cmd = a.agentsM.Update(msg)
			}
			return a, cmd
		}
		return a, nil

	case tea.KeyMsg:
		// The "Opening…" overlay hides every screen and dialog, so keys
		// must not reach them: a second Enter would start a duplicate
		// resume, and `q` would open a quit confirmation nobody can see.
		// ctrl+c still quits.
		if a.attach.active {
			if msg.String() == "ctrl+c" {
				return a, tea.Quit
			}
			return a, nil
		}
		if a.confirm.open() {
			// The confirmation handles ctrl+c itself.
			return a.updateConfirmationKey(msg)
		}
		// ctrl+c quits from ANYWHERE — every overlay, wizard, form and
		// text field below. Checked once, here, ahead of all overlay
		// routing: overlays that swallow "every other key" (tour, help,
		// usage, previews, info panels, matrix, the SSH wizard) used to
		// swallow ctrl+c too, although the help promises it quits.
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}

		// SSH setup wizard owns the screen when open — keystrokes
		// go to the password field / multi-select rather than the
		// underlying Sessions / Network screen. Routed before the
		// matrix easter egg so 'M' typed into a password doesn't
		// trigger the rain.
		if a.sshWizard != nil && a.sshWizard.Active() {
			var cmd tea.Cmd
			a.sshWizard, cmd = a.sshWizard.Update(msg)
			return a, cmd
		}

		// Matrix easter egg takes priority — when active, the overlay
		// owns the screen until Esc dismisses it. Routed before the
		// tour so triggering the rain while the tour is on doesn't
		// produce a confused split state.
		if a.matrix.Active() {
			var cmd tea.Cmd
			a.matrix, cmd = a.matrix.Update(msg)
			return a, cmd
		}
		// M (shift-M) opens the Matrix overlay from any navigation surface,
		// mirroring T which reopens the tour. Suppressed when a text-input
		// modal has focus so a session named "My-project" doesn't hijack.
		//
		// Excluded on the Agents screen: there `M` opens the ccmux-model
		// picker (pin a model for ccmux-launched sessions). The real
		// feature wins over the easter egg on the one screen that binds
		// the same key — otherwise the picker is silently dead, shadowed
		// here before the keystroke ever reaches the screen.
		if msg.String() == "M" && !a.modalCapturingText() && a.screen != ScreenAgents {
			a.matrix.Open()
			a.matrix.SetSize(a.width, a.height)
			return a, matrixTick()
		}

		// Tour overlay takes top priority. The tour owns the screen
		// until the user finishes it or skips with esc/q. Re-openable
		// later with `T`.
		if a.tour.Active() {
			switch msg.String() {
			case "right", "enter", " ", "n":
				if !a.tour.Next() {
					// Last slide → mark complete + close.
					a.tour.Close()
					a.markTourShown()
				}
			case "left", "p":
				a.tour.Prev()
			case "up", "k":
				a.tour.ScrollBy(-1, a.width, a.height)
			case "down", "j":
				a.tour.ScrollBy(1, a.width, a.height)
			case "esc", "q":
				a.tour.Close()
				a.markTourShown()
			}
			return a, nil
		}

		// Help overlay takes precedence — `?` or `esc` close it, the
		// arrows scroll it when it's taller than the terminal, and every
		// other key is swallowed.
		if a.helpOpen {
			switch msg.String() {
			case "?", "esc":
				a.helpOpen = false
			case "up", "k":
				a.helpScroll = maxInt(0, minInt(a.helpScroll, a.helpScrollMax())-1)
			case "down", "j":
				a.helpScroll = minInt(a.helpScroll+1, a.helpScrollMax())
			case "pgup":
				a.helpScroll = maxInt(0, minInt(a.helpScroll, a.helpScrollMax())-maxInt(1, a.height-helpChromeRows))
			case "pgdown", " ":
				a.helpScroll = minInt(a.helpScroll+maxInt(1, a.height-helpChromeRows), a.helpScrollMax())
			case "home", "g":
				a.helpScroll = 0
			case "end", "G":
				a.helpScroll = a.helpScrollMax()
			}
			return a, nil
		}

		// Usage overlay — `u` or `esc` close it. Same passthrough
		// model as the help overlay: only the close keys do
		// anything; everything else is swallowed.
		if a.usageOpen {
			switch msg.String() {
			case "u", "esc":
				a.usageOpen = false
			}
			return a, nil
		}

		// Transcript-preview overlay — `p` or `esc` close it. Same
		// swallow-everything-else model as the usage overlay.
		if a.convPreview.IsOpen() {
			switch msg.String() {
			case "p", "esc":
				a.convPreview.Close()
			}
			return a, nil
		}

		// Project-info overlay — `i` or `esc` close it. Same
		// passthrough model as usage/help: every other key is
		// swallowed so the underlying list doesn't see them.
		if a.projectInfoOpen {
			switch msg.String() {
			case "i", "esc":
				a.projectInfoOpen = false
			}
			return a, nil
		}

		// Settings info overlay — `i` or `esc` close it. Same
		// swallow-all-other-keys pattern as the help/usage overlays.
		if a.settingsInfoOpen {
			switch msg.String() {
			case "i", "esc":
				a.settingsInfoOpen = false
			}
			return a, nil
		}

		// Network host-detail overlay — `i` or `esc` close it.
		// Same passthrough model as the usage overlay.
		if a.network.DetailOpen() {
			switch msg.String() {
			case "i", "esc":
				a.network.CloseDetail()
			}
			return a, nil
		}

		// Esc dismisses the current toast — unless a form, editor or
		// overlay is open, where esc belongs to it. (A toast that keeps
		// being re-set, like the daemon-offline refresh error, used to
		// make every such form impossible to close.)
		if msg.String() == "esc" && a.toasts.Active() && !a.modalCapturingText() {
			a.toasts.Clear()
			return a, nil
		}

		// `?` opens the help overlay from any screen.
		if msg.String() == "?" && !a.modalCapturingText() {
			a.helpOpen = true
			a.helpScroll = 0
			return a, nil
		}

		// `u` opens the usage overlay — only on the Sessions/home
		// screen, where the inline Usage panel hints at it. On other
		// screens `u` is free for a future per-screen binding.
		// Modal-text guard mirrors `M`: a session named "ubuntu" or a
		// rename like "tui-rename-dst" mustn't hijack the keystroke
		// while the textinput has focus.
		if msg.String() == "u" && a.screen == ScreenSessions && !a.modalCapturingText() {
			a.usageOpen = true
			return a, nil
		}

		// `p` opens the transcript-preview overlay — only on the
		// Conversations screen, where a row is selected. The modal-
		// text guard keeps the keystroke out when the help overlay,
		// tour, or any text-capturing form is open. The same `p`
		// closes the overlay (handled above).
		if msg.String() == "p" && a.screen == ScreenConversations && !a.modalCapturingText() {
			sel := a.conversationsM.Selected()
			if sel != nil {
				a.convPreview.Open(*sel)
				return a, a.loadConvPreviewCmd(*sel)
			}
			return a, nil
		}

		// `i` opens the per-project info overlay on the Projects
		// screen. Modal-text guard mirrors `u`: a project name typed
		// into the filter or the new-project form must not hijack
		// the keystroke. No-op when no project is selected so a
		// freshly-discovered-empty list doesn't open an empty modal.
		if msg.String() == "i" && a.screen == ScreenProjects && !a.modalCapturingText() {
			if a.projectsM.Selected() != nil {
				a.projectInfoOpen = true
			}
			return a, nil
		}

		// `i` opens the note-info overlay on the Notes screen. Guard
		// against the modal-text trap: if the user is typing into the
		// search field or the new-note form, the `i` keystroke goes
		// to the input rather than opening the overlay.
		if msg.String() == "i" && a.screen == ScreenNotes && !a.modalCapturingText() {
			return a, func() tea.Msg { return noteInfoOpenMsg{} }
		}

		// `i` opens the Settings info overlay (ccmux version, config
		// path, log path, last save). Restricted to the Settings
		// screen so the keystroke remains available for future
		// per-screen bindings elsewhere. Same modal-text guard as `u`
		// so a textinput "i" doesn't hijack the overlay.
		if msg.String() == "i" && a.screen == ScreenSettings && !a.modalCapturingText() {
			a.settingsInfoOpen = true
			return a, nil
		}

		// `T` re-opens the first-run tour at step 0. Capital so it doesn't
		// collide with vim-style `t` someone might add to a per-screen
		// nav binding later. Same modal-text guard as `i`/`u` so typing a
		// capital T into a filter / rename / new-project / notes-search
		// field doesn't yank the user into the tour mid-input.
		if msg.String() == "T" && !a.modalCapturingText() {
			a.tour.Open()
			return a, nil
		}

		// If projects screen has its modal open (new-project form or session
		// picker), route through it. (ctrl+c already quit above.)
		if a.screen == ScreenProjects && (a.projectsM.form != nil || a.projectsM.menu != nil) {
			var cmd tea.Cmd
			a.projectsM, cmd = a.projectsM.Update(msg)
			return a, cmd
		}

		// Conversation search owns digits, delete/quit keys, and Enter until
		// committed; typing a query must never launch or delete a conversation.
		if a.screen == ScreenConversations && a.conversationsM.capturesInput() {
			var cmd tea.Cmd
			a.conversationsM, cmd = a.conversationsM.Update(msg)
			return a, cmd
		}

		// Projects filter mode: textinput owns the keystrokes. Enter
		// commits the filter and attaches to the highlighted match;
		// esc clears the filter without firing attach.
		if a.screen == ScreenProjects && a.projectsM.FilterActive() {
			if keyMatches(msg, a.keys.Enter) {
				a.projectsM.commitFilter()
				a2, cmd := a.attachOrCreateForSelectedProject()
				return a2, cmd
			}
			if msg.String() == "esc" {
				a.projectsM.exitFilter()
				return a, nil
			}
			var cmd tea.Cmd
			a.projectsM, cmd = a.projectsM.Update(msg)
			return a, cmd
		}

		// Same modal-routing for the Sessions tab: new-bare-session form and
		// rename form both need to intercept Enter/digit keys before the global
		// handlers see them. Without this, the global Enter handler below
		// intercepts the form's submit key and attaches to whatever session
		// the cursor was on — observed as "Enter in the new-session form
		// attaches to c-ccmux instead of creating a new session".
		if a.screen == ScreenSessions && (a.sessionsM.form != nil || a.sessionsM.renameForm != nil) {
			var cmd tea.Cmd
			a.sessionsM, cmd = a.sessionsM.Update(msg)
			return a, cmd
		}

		// Sessions filter mode: the textinput owns the keystrokes, so a
		// query containing a digit, q, x or R types instead of switching
		// screens, quitting, killing or renaming. Enter commits the
		// filter and attaches to the highlighted match; esc clears it.
		if a.screen == ScreenSessions && a.sessionsM.FilterActive() {
			if keyMatches(msg, a.keys.Enter) {
				a.sessionsM.commitFilter()
				return a.attachSelectedSession()
			}
			if msg.String() == "esc" {
				return a, a.sessionsM.exitFilter()
			}
			var cmd tea.Cmd
			a.sessionsM, cmd = a.sessionsM.Update(msg)
			return a, cmd
		}

		// Notes search mode: the search textinput owns every keystroke only
		// while it actually holds focus, so browsing results doesn't swallow
		// global shortcuts (1-7, q, ?, r) or trap the screen on search error.
		if a.screen == ScreenNotes && a.notes.searching && a.notes.searchInput.Focused() {
			var cmd tea.Cmd
			a.notes, cmd = a.notes.Update(msg)
			return a, cmd
		}

		// Notes new-note form: the modal owns every keystroke so the
		// filename / title fields don't have their characters
		// swallowed by global bindings (e.g. "r" → refresh).
		if a.screen == ScreenNotes && a.notes.newNoteForm != nil {
			var cmd tea.Cmd
			a.notes, cmd = a.notes.Update(msg)
			return a, cmd
		}

		// Notes info panel (`i`) and project picker (`p` / space): like
		// the other modals they own the keyboard until they close.
		// Without this, a digit switched screens underneath them, q
		// opened the quit dialog over the picker, and esc dismissed a
		// toast instead of closing it.
		if a.screen == ScreenNotes && (a.notes.noteInfo.open || a.notes.pickingProject) {
			var cmd tea.Cmd
			a.notes, cmd = a.notes.Update(msg)
			return a, cmd
		}

		// Settings inline editor: the textinput owns every keystroke so
		// characters typed into a value (e.g. a projects.root path with
		// an "r", a digit, or a "q" in it) aren't hijacked by the global
		// refresh / screen-switch / quit handlers.
		if a.screen == ScreenSettings && a.settings.IsEditing() {
			var cmd tea.Cmd
			a.settings, cmd = a.settings.Update(msg)
			return a, cmd
		}

		// Agents modal picker (Claude model / effort): the picker owns
		// every keystroke — up/down/enter/esc drive the selection — so
		// digit screen-switch and the global "q" quit-confirm can't fire
		// over the modal.
		if a.screen == ScreenAgents && a.agentsM.ModalOpen() {
			var cmd tea.Cmd
			a.agentsM, cmd = a.agentsM.Update(msg)
			return a, cmd
		}

		switch {
		case msg.String() == "q":
			return a.openQuitConfirmation()
		case keyMatches(msg, a.keys.Kill) && a.screen == ScreenSessions:
			if sel := a.sessionsM.Selected(); sel != nil {
				return a.openKillSessionConfirmation(sessionHostLabel(*sel), sel.Name)
			}
			return a, nil
		case keyMatches(msg, a.keys.Quit):
			return a, tea.Quit
		case keyMatches(msg, a.keys.Sessions):
			a.screen = ScreenSessions
			return a, nil
		case keyMatches(msg, a.keys.Conversations):
			a.screen = ScreenConversations
			// Conversations are read lazily — refresh on every entry so
			// a newly-saved transcript from another window shows up next
			// time the user opens this tab. The spinner tick drives the
			// loading placeholder while the walk runs.
			a.conversationsM.SetLoading(true)
			return a, tea.Batch(a.refreshConversationsCmd(), a.conversationsM.SpinnerTickCmd())
		case keyMatches(msg, a.keys.Projects):
			a.screen = ScreenProjects
			return a, nil
		case keyMatches(msg, a.keys.Notes):
			a.screen = ScreenNotes
			// Propagate the currently-focused project from Projects,
			// honoring any active filter so the Notes pane opens on
			// what the user just highlighted. SetProject returns a Cmd
			// that runs Vault.List off the UI goroutine — chain it so
			// the screen doesn't freeze on projects with deep trees.
			var cmd tea.Cmd
			if sel := a.projectsM.Selected(); sel != nil {
				cmd = a.notes.SetProject(sel)
			}
			return a, cmd
		case keyMatches(msg, a.keys.Claude):
			a.screen = ScreenAgents
			return a, nil
		case keyMatches(msg, a.keys.Settings):
			a.screen = ScreenSettings
			return a, nil
		case keyMatches(msg, a.keys.Network):
			a.screen = ScreenNetwork
			// Kick the spinner column on screen-enter so the user
			// sees motion while the refresh tick catches up. The
			// next sessionsLoadedMsg will clear the flag.
			tick := a.network.StartRefresh()
			return a, tea.Batch(tick, a.refreshSessionsCmd())
		case keyMatches(msg, a.keys.Refresh) && a.screen != ScreenAgents:
			// Refresh fans out: sessions are always refreshed (most
			// screens display them in some form via the dashboard or
			// the chrome). On the Projects screen we ALSO re-run the
			// project discovery, otherwise a project added/removed
			// from disk while ccmux is open never appears until
			// restart.
			//
			// The user pressing "r" is an explicit ask for current
			// numbers, so drop the ccusage memo — the 15s background
			// tick reuses it (see internal/ccusage.cacheTTL), but a
			// deliberate refresh should pay for a real re-run.
			ccusage.InvalidateCache()
			if a.screen == ScreenProjects {
				return a, tea.Batch(a.refreshSessionsCmd(), a.refreshProjectsCmd())
			}
			// Conversations screen: re-walk the transcripts too — the
			// help overlay promises "refresh conversation list", and a
			// sessions-only refresh left new transcripts invisible until
			// the tab was re-entered.
			if a.screen == ScreenConversations {
				a.conversationsM.SetLoading(true)
				sessions := a.refreshSessionsCmd()
				convs := a.refreshConversationsCmd()
				return a, tea.Batch(sessions, convs, a.conversationsM.SpinnerTickCmd())
			}
			if a.screen == ScreenNotes {
				return a, tea.Batch(a.refreshSessionsCmd(), func() tea.Msg { return notesReloadMsg{} })
			}
			// Network screen: start the per-row spinner so the user
			// sees a refresh in flight, even when the actual probe
			// finishes within a single frame.
			if a.screen == ScreenNetwork {
				tick := a.network.StartRefresh()
				return a, tea.Batch(tick, a.refreshSessionsCmd())
			}
			return a, a.refreshSessionsCmd()
		case keyMatches(msg, a.keys.Enter) && a.screen == ScreenSessions:
			a2, cmd := a.attachSelectedSession()
			return a2, cmd
		case keyMatches(msg, a.keys.Enter) && a.screen == ScreenProjects:
			a2, cmd := a.attachOrCreateForSelectedProject()
			return a2, cmd
		case keyMatches(msg, a.keys.Enter) && a.screen == ScreenConversations:
			sel := a.conversationsM.Selected()
			if sel == nil {
				return a, nil
			}
			// Flip the overlay BEFORE the resume cmd kicks off — its
			// tmux.New + agent boot can take several seconds, and the
			// user shouldn't see the Conversations screen during it.
			label := sel.Project
			if label == "" {
				label = string(sel.Agent) + " conversation"
			}
			tick := a.startAttaching(attachKindResume, label)
			return a, tea.Batch(tick, a.resumeConversationCmd(*sel))
		case keyMatches(msg, a.keys.ToggleHeadless) && a.screen == ScreenConversations:
			// Flip the include-headless toggle and re-walk transcripts
			// so the list reflects the new filter immediately. Refresh
			// goes through the same path as the Refresh keybind: set
			// loading state, dispatch refreshConversationsCmd which
			// reads conversationsM.showHeadless to build Options.
			now := a.conversationsM.ToggleHeadless()
			a.conversationsM.SetLoading(true)
			label := tr("Headless / SDK conversations: hidden")
			if now {
				label = tr("Headless / SDK conversations: shown")
			}
			toast := func() tea.Msg {
				return toastMsg{Text: label, Kind: toastInfo, Until: time.Now().Add(2 * time.Second)}
			}
			return a, tea.Batch(toast, a.refreshConversationsCmd())
		case keyMatches(msg, a.keys.Enter) && a.screen == ScreenNetwork:
			if c := a.network.SSHCmd(); c != nil {
				return a, c
			}
			return a, func() tea.Msg {
				return toastMsg{Text: tr("nothing to ssh into for that row"), Kind: toastInfo, Until: time.Now().Add(3 * time.Second)}
			}
		case msg.String() == "s" && a.screen == ScreenNetwork && !a.modalCapturingText():
			// `s` on the Network screen opens the SSH setup wizard
			// for the focused host. This is the proactive entry
			// point — the user fixes auth before attempting to
			// attach rather than after seeing Permission denied.
			if c := a.network.SetupSSHCmd(); c != nil {
				return a, c
			}
			return a, func() tea.Msg {
				return toastMsg{Text: tr("nothing to set up for that row"), Kind: toastInfo, Until: time.Now().Add(3 * time.Second)}
			}
		case msg.String() == "i" && a.screen == ScreenNetwork && !a.modalCapturingText():
			// `i` opens the host-detail overlay for the focused
			// row. Parallel to `u` on the home screen — same
			// passthrough modal model.
			if a.network.Selected() != nil {
				a.network.OpenDetail()
			}
			return a, nil
		}
	}

	// Forward to the active screen.
	var cmd tea.Cmd
	switch a.screen {
	case ScreenSessions:
		// Dashboard always updates (handles usage ticks, refresh msgs).
		// Sessions handles navigation and session-action keys.
		var dcmd tea.Cmd
		a.dashboard, dcmd = a.dashboard.Update(msg)
		a.sessionsM, cmd = a.sessionsM.Update(msg)
		cmd = tea.Batch(cmd, dcmd)
	case ScreenConversations:
		a.conversationsM, cmd = a.conversationsM.Update(msg)
	case ScreenProjects:
		a.projectsM, cmd = a.projectsM.Update(msg)
	case ScreenNotes:
		a.notes, cmd = a.notes.Update(msg)
	case ScreenAgents:
		a.agentsM, cmd = a.agentsM.Update(msg)
	case ScreenSettings:
		a.settings, cmd = a.settings.Update(msg)
	case ScreenNetwork:
		a.network, cmd = a.network.Update(msg)
	}
	return a, cmd
}
