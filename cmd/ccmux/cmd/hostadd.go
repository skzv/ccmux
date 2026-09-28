package cmd

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/config"
)

// hostAddOptions are `ccmux host add`'s flags.
type hostAddOptions struct {
	user     string
	userSet  bool // --user given explicitly
	mosh     bool
	port     int // ccmuxd HTTP port
	sshPort  int
	nameArg  string
	addrArg  string
	outWrite io.Writer
}

// newHostAddCmd: `ccmux host add <name> <address> [--user U] [--mosh=false]
// [--port N] [--ssh-port N]`.
//
// It used to store whatever it was given: an empty name or address
// (the latter later dialed localhost), the reserved name "local",
// names with spaces or shell metacharacters, an address like
// `-oProxyCommand=…` that ssh would read as an option, and
// `mini.ts.net:7474`, which failed much later as `…:7474:7474`. The
// README and the SSH guide also documented --mosh/--user/a port flag
// that didn't exist, and success printed nothing.
func newHostAddCmd() *cobra.Command {
	var o hostAddOptions
	c := &cobra.Command{
		Use:   "add <name> <address>",
		Short: "Add a remote ccmuxd host",
		Long: `Add a remote host running ccmuxd to ~/.config/ccmux/config.toml.

<name> is how you refer to it (` + "`ccmux shell --host <name>`" + `, ` + "`ccmux host setup-ssh <name>`" + `):
letters, digits, '.', '_', '-' and '@', starting with a letter or digit.
"local" is reserved for this device.

<address> is the host's tailnet name or IP — ` + "`mini`" + `, ` + "`mini.tail-xxxxx.ts.net`" + `,
` + "`100.64.0.5`" + ` — optionally as user@host. Ports go in flags, not the
address: --port is the ccmuxd port (default 7474), --ssh-port the sshd
port (default 22).

Attaching uses mosh by default (roams, survives stalls); pass --mosh=false
to attach over plain ssh.

Examples:
  ccmux host add mini mini.tail-xxxxx.ts.net
  ccmux host add alice@sputnik sputnik --user alice
  ccmux host add build 100.64.0.9 --ssh-port 2222 --mosh=false`,
		Args: cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			o.nameArg, o.addrArg = args[0], args[1]
			o.userSet = c.Flags().Changed("user")
			o.outWrite = c.OutOrStdout()
			return runHostAdd(o)
		},
	}
	c.Flags().StringVar(&o.user, "user", "", "SSH user on the remote (default: your local username)")
	c.Flags().BoolVar(&o.mosh, "mosh", true, "attach with mosh; --mosh=false uses plain ssh")
	c.Flags().IntVar(&o.port, "port", defaultTailnetPort, "ccmuxd HTTP port on the remote")
	c.Flags().IntVar(&o.sshPort, "ssh-port", 22, "sshd port on the remote")
	return c
}

func runHostAdd(o hostAddOptions) error {
	h, err := buildHostEntry(o)
	if err != nil {
		return err
	}
	// Abort on a Load error instead of proceeding: Load returns
	// Defaults() alongside the error on a corrupt or unreadable
	// config.toml, and Save truncates the file — so swallowing the error
	// would wipe every other host and all other settings on the next
	// write.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config (not modifying it): %w", err)
	}
	// Reject duplicate names instead of silently appending: `host
	// remove <name>` deletes every entry with that name, so a duplicate
	// add would make the eventual remove wipe both — including the
	// original.
	for _, existing := range cfg.Hosts {
		if strings.EqualFold(existing.Name, h.Name) {
			return fmt.Errorf("host %q already exists (address %s); run `ccmux host remove %s` first, or pick a different name",
				safeField(existing.Name), safeField(existing.Address), safeField(existing.Name))
		}
	}
	cfg.Hosts = append(cfg.Hosts, h)
	if err := config.Save(cfg); err != nil {
		return err
	}
	how := "mosh"
	if !h.Mosh {
		how = "ssh"
	}
	dial := h.Address
	if h.User != "" {
		dial = h.User + "@" + h.Address
	}
	fmt.Fprintf(o.outWrite, "✓ added host %s: ccmuxd at %s; attaches with %s to %s (ssh port %d)\n",
		h.Name, net.JoinHostPort(h.Address, strconv.Itoa(h.Port)), how, dial, h.EffectiveSSHPort())
	return nil
}

// buildHostEntry validates `host add`'s arguments and flags into the
// config row it stores.
func buildHostEntry(o hostAddOptions) (config.Host, error) {
	if err := validateHostName(o.nameArg); err != nil {
		return config.Host{}, err
	}
	addrUser, host, err := parseHostAddress(o.addrArg)
	if err != nil {
		return config.Host{}, err
	}
	user := addrUser
	if o.userSet {
		if addrUser != "" && addrUser != o.user {
			return config.Host{}, fmt.Errorf("address %q names user %q but --user says %q; pass one", o.addrArg, addrUser, o.user)
		}
		user = o.user
	}
	if user != "" {
		if err := validateSSHUser(user); err != nil {
			return config.Host{}, err
		}
	}
	for _, p := range []struct {
		flag string
		v    int
	}{{"--port", o.port}, {"--ssh-port", o.sshPort}} {
		if p.v < 1 || p.v > 65535 {
			return config.Host{}, fmt.Errorf("%s %d: want a port between 1 and 65535", p.flag, p.v)
		}
	}
	h := config.Host{Name: o.nameArg, Address: host, User: user, Mosh: o.mosh, Port: o.port}
	// Store the ssh port whenever it isn't the default — and also when
	// the ccmuxd port isn't: an entry with a non-7474 Port and no
	// SSHPort reads as a legacy "port is the SSH port" host
	// (sshTargetForHost), which would probe ssh on the ccmuxd port.
	if o.sshPort != 22 || o.port != defaultTailnetPort {
		h.SSHPort = o.sshPort
	}
	return h, nil
}

// reservedHostNames can't name a configured host: "local" already
// means this device (`ccmux shell --host local`, the notes host label).
var reservedHostNames = []string{"local"}

// validateHostName enforces the name rule `host add` documents.
func validateHostName(name string) error {
	for _, r := range reservedHostNames {
		if strings.EqualFold(name, r) {
			return fmt.Errorf("host name %q is reserved for this device; pick another name", name)
		}
	}
	if name == "" || len(name) > 64 || !isAlnum(name[0]) {
		return fmt.Errorf("invalid host name %q: use 1-64 letters, digits, '.', '_', '-' or '@', starting with a letter or digit", safeField(name))
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !isAlnum(c) && c != '.' && c != '_' && c != '-' && c != '@' {
			return fmt.Errorf("invalid host name %q: use 1-64 letters, digits, '.', '_', '-' or '@', starting with a letter or digit", safeField(name))
		}
	}
	return nil
}

// validateSSHUser accepts a POSIX-ish account name. Crucially it can't
// start with '-': `-oProxyCommand=…@host` would reach ssh as an option.
func validateSSHUser(user string) error {
	if user == "" || len(user) > 64 || !isAlnum(user[0]) && user[0] != '_' {
		return fmt.Errorf("invalid SSH user %q: use letters, digits, '.', '_' or '-', not starting with '-' or '.'", safeField(user))
	}
	for i := 0; i < len(user); i++ {
		c := user[i]
		if !isAlnum(c) && c != '.' && c != '_' && c != '-' {
			return fmt.Errorf("invalid SSH user %q: use letters, digits, '.', '_' or '-', not starting with '-' or '.'", safeField(user))
		}
	}
	return nil
}

// parseHostAddress splits `host add`'s address into an optional user
// and the host (a DNS name or an IP). A port is refused rather than
// guessed at — whether `:2222` meant ccmuxd or sshd is anyone's guess —
// with a message naming both flags.
func parseHostAddress(raw string) (user, host string, err error) {
	bad := func(why string) (string, string, error) {
		return "", "", fmt.Errorf("invalid address %q: %s", safeField(raw), why)
	}
	switch {
	case raw == "":
		return bad("the host's tailnet name or IP is required")
	case strings.HasPrefix(raw, "-"):
		return bad("it can't start with '-' (ssh would read it as an option)")
	case strings.Contains(raw, "://"):
		return bad("drop the scheme — give the bare host name or IP (e.g. mini.tail-xxxxx.ts.net)")
	}
	host = raw
	if i := strings.LastIndexByte(raw, '@'); i >= 0 {
		user, host = raw[:i], raw[i+1:]
		if user == "" {
			return bad("empty user before '@'")
		}
		if err := validateSSHUser(user); err != nil {
			return "", "", err
		}
	}
	if host == "" {
		return bad("the host's tailnet name or IP is required")
	}
	// A bracketed or bare IPv6 literal.
	if strings.HasPrefix(host, "[") {
		if h, _, perr := net.SplitHostPort(host); perr == nil && net.ParseIP(h) != nil {
			return bad("don't put a port in the address: pass the ccmuxd port with --port (default 7474) and the ssh port with --ssh-port (default 22)")
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
		if net.ParseIP(inner) == nil {
			return bad("not a valid IPv6 address")
		}
		return user, inner, nil
	}
	if strings.Count(host, ":") > 1 {
		if net.ParseIP(host) == nil {
			return bad("not a valid IPv6 address")
		}
		return user, host, nil
	}
	if strings.Contains(host, ":") {
		return bad("don't put a port in the address: pass the ccmuxd port with --port (default 7474) and the ssh port with --ssh-port (default 22)")
	}
	if net.ParseIP(host) != nil {
		return user, host, nil
	}
	if !validDNSName(host) {
		return bad("not a host name or IP (letters, digits, '-' and '.' only)")
	}
	return user, host, nil
}

// validDNSName reports whether s is a plausible host name: dot-separated
// labels of letters, digits, '-' and '_' (seen in some internal names),
// none empty or starting with '-'. Everything a shell or ssh treats
// specially — spaces, ';', '$', quotes, '/' — is excluded.
func validDNSName(s string) bool {
	if len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !isAlnum(c) && c != '-' && c != '_' {
				return false
			}
		}
	}
	return true
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
