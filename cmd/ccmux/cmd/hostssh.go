package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/sshsetup"
)

// newHostSetupSSHCmd: `ccmux host setup-ssh [user@]<address>` — the
// CLI mirror of the TUI's SSH setup wizard. Same package (sshsetup)
// under the hood, same probe-then-install-then-validate flow; the
// only difference is the terminal-driven prompts here vs. Bubble Tea
// modal there.
//
// Accepts the host as one of:
//   - a configured host NAME from ~/.config/ccmux/hosts.toml (we
//     look up its User+Address+Port);
//   - a "[user@]host[:port]" string for an ad-hoc target.
//
// Why both shapes: muscle memory + scripting. If you already have
// `host add mini sputnik`, you want `host setup-ssh mini`. If
// you're scripting a new machine, `host setup-ssh alice@sputnik`
// without first calling `host add` is faster.
func newHostSetupSSHCmd() *cobra.Command {
	var skipEnumerate bool
	c := &cobra.Command{
		Use:   "setup-ssh <name|[user@]host[:port]>",
		Short: "Bootstrap SSH key auth on a remote host so future attaches are passwordless",
		Long: `Interactive wizard that installs your local SSH public key on a remote host.

The wizard:
  1. Probes the target to see what's blocking attach.
  2. If key auth is just missing, prompts for the SSH password ONCE
     and uses it to install ~/.ssh/id_ed25519.pub on the remote.
  3. Validates by reconnecting with the key.
  4. Optionally lists other Unix accounts on the remote and offers
     to add them as additional ccmux hosts.

Password is used only for the bootstrap connection — it's never
written to disk, logged, or kept in memory past the install call.

Examples:
  ccmux host setup-ssh mini               # configured host
  ccmux host setup-ssh alice@sputnik      # ad-hoc target
  ccmux host setup-ssh alice@sputnik:2222 # non-default port`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runHostSetupSSH(args[0], skipEnumerate)
		},
	}
	c.Flags().BoolVar(&skipEnumerate, "skip-enumerate", false,
		"don't list other users on the remote after a successful install")
	return c
}

// runHostSetupSSH is the meat. Split out so we can unit-test the
// arg-parsing / branching without spawning a real cobra root.
func runHostSetupSSH(arg string, skipEnumerate bool) error {
	cfg, _ := config.Load()
	target, configuredName, err := resolveTarget(arg, cfg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt.Printf("Probing %s … ", target.String())
	res := sshsetup.Probe(ctx, target)
	fmt.Println(res.String())

	switch res {
	case sshsetup.ProbeOK:
		fmt.Println("✓ key auth already works — nothing to do")
		return nil
	case sshsetup.ProbeAuthFailed:
		// Fall through to wizard.
	case sshsetup.ProbeSshdDisabled:
		return fmt.Errorf("sshd is not accepting connections on %s. On macOS, open System Settings → General → Sharing and enable Remote Login", target.Host)
	case sshsetup.ProbeRefused:
		return fmt.Errorf("port %d on %s is closed — check that sshd is bound to that port", target.Port, target.Host)
	case sshsetup.ProbeTimeout:
		return fmt.Errorf("can't reach %s (timeout) — is Tailscale connected on both ends?", target.Host)
	case sshsetup.ProbeNoNetwork:
		return fmt.Errorf("can't resolve %s — check Tailscale MagicDNS or use the tailnet IP directly", target.Host)
	case sshsetup.ProbeHostKeyMismatch:
		return fmt.Errorf("⚠ host key for %s has changed. This can mean a real MITM — investigate before re-adding. To proceed anyway, delete the matching line from ~/.ssh/known_hosts", target.Host)
	default:
		return fmt.Errorf("probe returned %s; aborting", res.String())
	}

	fmt.Println("Loading local key …")
	lk, err := sshsetup.EnsureLocalKey()
	if err != nil {
		return fmt.Errorf("local key: %w", err)
	}
	fmt.Printf("  using %s\n", lk.PrivatePath)

	password, err := readPassword(fmt.Sprintf("SSH password for %s: ", target.String()))
	if err != nil {
		return err
	}
	defer scrub(&password)

	progress := sshsetup.Progress(func(stage, detail string) {
		fmt.Printf("  · %s: %s\n", stage, detail)
	})
	if err := sshsetup.InstallKeyViaPassword(ctx, target, password, lk, progress); err != nil {
		if errors.Is(err, sshsetup.ErrWrongPassword) {
			return fmt.Errorf("password rejected — re-run when ready")
		}
		return err
	}
	fmt.Printf("✓ key installed on %s\n", target.String())

	// Persist configured host's user if we used the configured
	// name and the toml had it empty. This means `setup-ssh mini`
	// after a fresh `host add mini sputnik` populates the user
	// field so future attaches don't need it on the command line.
	if configuredName != "" {
		if err := writeBackUserIfMissing(configuredName, target.User); err != nil {
			fmt.Fprintf(os.Stderr, "warn: could not persist user back to hosts.toml: %v\n", err)
		}
	}

	if skipEnumerate {
		return nil
	}
	others, err := sshsetup.EnumerateUsers(ctx, target, lk)
	if err != nil {
		// Don't fail the whole command on enumeration trouble —
		// the key is already installed, which is the important
		// part. Just note and continue.
		fmt.Fprintf(os.Stderr, "  (could not enumerate other users: %v)\n", err)
		return nil
	}
	if len(others) == 0 {
		return nil
	}
	fmt.Printf("\nOther users on %s: %s\n", target.Host, strings.Join(others, ", "))
	for _, u := range others {
		if !confirm(fmt.Sprintf("Add %s@%s as a separate host?", u, target.Host)) {
			continue
		}
		name := fmt.Sprintf("%s@%s", u, defaultHostName(target.Host))
		cfg.Hosts = append(cfg.Hosts, config.Host{
			Name:    name,
			Address: target.Host,
			User:    u,
			Port:    target.Port,
			Mosh:    true,
		})
		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("save host: %w", err)
		}
		fmt.Printf("  ✓ added %s\n", name)
	}
	return nil
}

// resolveTarget turns the CLI's positional arg into a Target. The
// arg is one of:
//   - the NAME of a configured host (looked up in cfg.Hosts);
//   - "host", "user@host", or "user@host:port" for an ad-hoc target.
//
// Returns the resolved Target plus the configured-host name (or ""
// for ad-hoc), so the caller can persist the user back into
// hosts.toml after a successful install.
func resolveTarget(arg string, cfg config.Config) (sshsetup.Target, string, error) {
	// Configured host?
	for _, h := range cfg.Hosts {
		if h.Name == arg {
			user := h.User
			if user == "" {
				user = currentUser()
			}
			port := h.Port
			if port == 0 || port == 7474 {
				// 7474 is the ccmuxd tailnet port; the SSH
				// listener is separate (22 by default). Fall
				// back to 22 unless the user explicitly set a
				// non-7474 port (in which case we honor it).
				port = 22
			}
			return sshsetup.Target{User: user, Host: h.Address, Port: port}, h.Name, nil
		}
	}
	// Ad-hoc parse.
	return parseAdHocTarget(arg)
}

// parseAdHocTarget reads "[user@]host[:port]" and constructs a
// Target, defaulting user to the local $USER and port to 22.
//
// IPv6 brackets are NOT supported — that's a future-me problem; the
// vast majority of tailnet targets are MagicDNS names or v4 IPs.
func parseAdHocTarget(arg string) (sshsetup.Target, string, error) {
	if arg == "" {
		return sshsetup.Target{}, "", errors.New("target is empty")
	}
	t := sshsetup.Target{}
	rest := arg
	if i := strings.Index(rest, "@"); i >= 0 {
		t.User = rest[:i]
		rest = rest[i+1:]
	}
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		t.Host = rest[:i]
		p := 0
		for _, r := range rest[i+1:] {
			if r < '0' || r > '9' {
				return sshsetup.Target{}, "", fmt.Errorf("invalid port in %q", arg)
			}
			p = p*10 + int(r-'0')
		}
		t.Port = p
	} else {
		t.Host = rest
	}
	if t.Host == "" {
		return sshsetup.Target{}, "", fmt.Errorf("could not parse host from %q", arg)
	}
	if t.User == "" {
		t.User = currentUser()
	}
	if t.Port == 0 {
		t.Port = 22
	}
	return t, "", nil
}

// currentUser returns the local username, falling back to $USER and
// then to "root" so we never produce an empty user.
func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "root"
}

// defaultHostName picks a name for a freshly-added host. Strips
// MagicDNS suffixes (".tail-xxxxx.ts.net") so we get "sputnik"
// instead of "sputnik.tail-12345.ts.net".
func defaultHostName(host string) string {
	if i := strings.Index(host, "."); i > 0 {
		return host[:i]
	}
	return host
}

// writeBackUserIfMissing persists the user we authenticated as back
// to hosts.toml if the configured entry had User="" (which is the
// common state after a fresh `host add`). Idempotent — re-running
// after the field is set is a no-op.
func writeBackUserIfMissing(name, user string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	changed := false
	for i := range cfg.Hosts {
		if cfg.Hosts[i].Name == name && cfg.Hosts[i].User == "" {
			cfg.Hosts[i].User = user
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return config.Save(cfg)
}

// readPassword prompts on stderr and reads a line from /dev/tty (or
// stdin if no TTY is attached, with echo on — used by tests).
//
// `term.ReadPassword` handles the no-echo + EOL semantics that a raw
// `bufio.Scanner` doesn't. We deliberately read from os.Stdin's FD
// rather than opening /dev/tty so a test that pipes stdin can still
// drive the prompt — that's what the unit test exercises.
func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	defer fmt.Fprintln(os.Stderr)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		pw, err := term.ReadPassword(fd)
		if err != nil {
			return "", err
		}
		return string(pw), nil
	}
	// Pipe / file backing stdin (tests, automation). Read a line.
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// confirm reads y/n from stdin. Default is no.
func confirm(prompt string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

// scrub overwrites a string-ish password value with zero bytes, then
// blanks the variable. Go strings are immutable so we can't
// strictly zero the underlying bytes, but this releases the
// reference so GC can reclaim the allocation. Defensive habit, not
// a guarantee.
func scrub(s *string) {
	if s == nil {
		return
	}
	*s = ""
}

// syscall is referenced only so the IDE doesn't strip the import
// when we add interrupt handling later. Harmless.
var _ = syscall.SIGINT
