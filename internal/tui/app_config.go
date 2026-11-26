package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/i18n"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// markTourShown persists Tour.Shown=true so the tour doesn't re-fire on
// next launch. Errors are swallowed deliberately — the worst case is
// the user sees the tour twice, which is harmless. We don't want a
// config-write blip to interrupt the TUI's flow.
func (a *App) markTourShown() {
	if a.cfg.Tour.Shown && a.cfg.Tour.ShownVersion == a.version {
		return
	}
	a.cfg.Tour.Shown = true
	a.cfg.Tour.ShownVersion = a.version
	// Touch only the tour fields on disk: saving a.cfg wholesale would
	// also persist runtime overrides and whatever stale state it holds.
	version := a.version
	_, _ = config.Update(func(c *config.Config) error {
		c.Tour.Shown = true
		c.Tour.ShownVersion = version
		return nil
	})
}

// adoptConfig makes cfg (fresh from disk) the app's config: re-applies
// the runtime-only overlays and pushes it into every screen that caches
// a copy. The returned command re-lists projects when the projects root
// changed — without it, Projects and Notes kept showing the old root's
// projects until the user pressed `r`.
func (a *App) adoptConfig(cfg config.Config) tea.Cmd {
	if a.runtimeOverrides != nil {
		a.runtimeOverrides(&cfg)
	}
	a.overlayDetectedTier(&cfg)
	rootChanged := project.ResolveRoot(cfg.Projects.Root) != project.ResolveRoot(a.cfg.Projects.Root)
	a.cfg = cfg
	// A config.toml edit may have changed `lang` or `theme` — re-apply
	// both so a Settings cycle or $EDITOR save takes effect immediately.
	i18n.SetLanguage(cfg.Lang)
	a.applyStyles(styles.ForTheme(cfg.Theme))
	a.settings.SetConfig(cfg)
	a.dashboard.SetConfig(cfg)
	a.sessionsM.SetDefaultDir(cfg.Sessions.DefaultDir)
	a.sessionsM.SetDefaultAgent(cfg.Agents.Default)
	a.sessionsM.SetAgentCommands(cfg.AgentCommands())
	a.projectsM.SetDefaultAgent(cfg.Agents.Default)
	a.projectsM.SetAgentCommands(cfg.AgentCommands())
	a.projectsM.SetProjectsRoot(cfg.Projects.Root)
	if !rootChanged {
		return nil
	}
	// Notes showed a project from the old root; it follows Projects, so
	// start over from the new list.
	a.notes.SetProject(nil)
	return a.refreshProjectsCmd()
}

// overlayDetectedTier shows the auto-detected Claude tier when the user
// hasn't declared one (an empty tier on disk). Any tier the user set —
// including an explicit "api" — wins: treating "api" as "unset" meant
// the Settings cycle could never land on api (or get past it to pro)
// once a paid plan was detected, because every save of "api" was
// immediately overlaid with the detected plan again. Reports whether
// cfg changed.
func (a *App) overlayDetectedTier(cfg *config.Config) bool {
	t := a.detectedClaudeTier
	if t == "" || t == "api" {
		return false
	}
	if cfg.Subscription.TierFor("claude") != "" {
		return false
	}
	cfg.Subscription.SetTierFor("claude", t)
	return true
}

// SetRuntimeOverrides installs the per-run config tweaks that must be
// re-applied (never saved) each time a fresh config is adopted.
func (a *App) SetRuntimeOverrides(fn func(*config.Config)) { a.runtimeOverrides = fn }

// applyStyles pushes a newly-selected theme's Styles into the App and
// every child screen so theme changes take effect on the very next frame.
func (a *App) applyStyles(st styles.Styles) {
	a.styles = st
	a.dashboard.st = st
	a.sessionsM.st = st
	a.conversationsM.st = st
	a.projectsM.st = st
	a.notes.SetStyles(st)
	a.agentsM.SetStyles(st)
	a.settings.SetStyles(st)
	a.network.st = st
	a.tour.st = st
	if a.sshWizard != nil {
		a.sshWizard.st = st
	}
}

// SetStartupConfigError records a config.toml load failure to show once
// the UI is running. A config that didn't load can't say the first-run
// tour was already shown (and couldn't record it now), so the tour New
// opened from the defaults is closed: a broken config.toml used to
// re-show the tour on every launch. Someone with a config file to break
// isn't on their first run.
func (a *App) SetStartupConfigError(err error) {
	a.startupConfigErr = err
	if err != nil {
		a.tour.Close()
	}
}
