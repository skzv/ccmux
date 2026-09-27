// Command ccmux-mcp is an MCP (Model Context Protocol) server that
// exposes ccmux to coding agents. It speaks JSON-RPC 2.0 over stdio
// per the MCP spec, and proxies tool calls to the local ccmuxd via
// the same internal/daemon.Client the TUI uses. Set CCMUX_HOST to
// target a remote tailnet peer's daemon instead.
//
// An agent running inside ccmux can use these tools to read state
// across every session and project on every machine — "what is the
// session in /Users/skz/Projects/foo doing right now," "list every
// past Claude conversation on the Mac mini," "what's the team's
// token spend this week." With --allow-mutate, it can also spawn
// new sessions and send keys into existing ones.
//
// Wire it up in Claude Code as a user-scope MCP server — `ccmux mcp
// register` (or `ccmux setup`) does this for you, or by hand:
//
//	claude mcp add --scope user ccmux -- ccmux-mcp
//
// which stores it under the top-level "mcpServers" of ~/.claude.json.
// (Claude Code does not read an "mcpServers" key in
// ~/.claude/settings.json.)
//
// See docs/01_Specs/04_MCP_Server.md for the full tool surface and
// the security model around --allow-mutate.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/skzv/ccmux/internal/daemon"
)

// version is stamped by the linker (-X main.version) at build time.
// Defaults to "dev" for go-run / unstamped local builds.
var version = "dev"

func main() {
	var (
		allowMutate = flag.Bool("allow-mutate", false, "expose mutating tools (spawn_session, send_keys, kill_session). Off by default.")
		host        = flag.String("host", os.Getenv("CCMUX_HOST"), "target ccmuxd at host[:port] on the tailnet (port defaults to 7474; an http:// prefix is accepted). Empty = local Unix socket.")
		printVer    = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *printVer {
		fmt.Println("ccmux-mcp", version)
		return
	}

	var (
		cli *daemon.Client
		err error
	)
	if *host != "" {
		addr, herr := remoteAddr(*host)
		if herr != nil {
			fmt.Fprintln(os.Stderr, "ccmux-mcp:", herr)
			os.Exit(2)
		}
		cli = daemon.RemoteClient(addr)
	} else {
		cli, err = daemon.LocalClient()
		if err != nil {
			fmt.Fprintln(os.Stderr, "ccmux-mcp: local daemon unreachable:", err)
			os.Exit(1)
		}
	}

	srv := NewServer(cli, *allowMutate, version)
	if err := srv.Run(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ccmux-mcp: exit:", err)
		os.Exit(1)
	}
}

// defaultDaemonPort is ccmuxd's tailnet HTTP port.
const defaultDaemonPort = "7474"

// remoteAddr turns a --host / CCMUX_HOST value into the host:port
// daemon.RemoteClient dials. It used to be passed through verbatim, so
// `--host http://mini:7474` built the URL http://http//mini:7474 and
// failed with a baffling DNS error. Accepted: host, host:port, an
// http:// URL with an optional trailing slash, and bracketed IPv6. A
// missing port means ccmuxd's default, 7474. https:// and paths are
// refused: ccmuxd serves plain HTTP at the root, on the tailnet only.
func remoteAddr(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	bad := func(why string) (string, error) {
		return "", fmt.Errorf("--host %q: %s (want host or host:port, e.g. mini.tail-xxxxx.ts.net:7474)", raw, why)
	}
	if _, ok := cutPrefixFold(s, "https://"); ok {
		return bad("ccmuxd speaks plain HTTP over the tailnet, not https")
	}
	if rest, ok := cutPrefixFold(s, "http://"); ok {
		s = strings.TrimSuffix(rest, "/")
	}
	if s == "" {
		return bad("empty host")
	}
	if strings.ContainsAny(s, "/?#@ \t") {
		return bad("not a host[:port]")
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		// No port (or a bare IPv6 literal): use the default.
		host, port = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]"), defaultDaemonPort
	}
	if host == "" {
		return bad("empty host")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return bad("invalid port")
	}
	return net.JoinHostPort(host, port), nil
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}
