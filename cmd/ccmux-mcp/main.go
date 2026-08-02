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
// Wire it up in Claude Code via ~/.claude/settings.json:
//
//	"mcpServers": {
//	  "ccmux": {"command": "ccmux-mcp"}
//	}
//
// See docs/01_Specs/04_MCP_Server.md for the full tool surface and
// the security model around --allow-mutate.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/skzv/ccmux/internal/daemon"
)

// version is stamped by the linker (-X main.version) at build time.
// Defaults to "dev" for go-run / unstamped local builds.
var version = "dev"

func main() {
	var (
		allowMutate = flag.Bool("allow-mutate", false, "expose mutating tools (spawn_session, send_keys, kill_session). Off by default.")
		host        = flag.String("host", os.Getenv("CCMUX_HOST"), "target ccmuxd at host:port (tailnet). Empty = local Unix socket.")
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
		cli = daemon.RemoteClient(*host)
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
