# Tailscale Networking

Tailscale is the invisible substrate that everything else in ccmux builds on. This doc explains the wire-level picture, why this specific tool, and what ccmux integrates with vs. what it leaves alone.

## One-line model

**Tailscale gives your Mac Mini a stable private address that's reachable from any device you own, on any network, without port forwarding.** Mosh, `ccmuxd`, and Moshi all treat that address like the Mac is on the same Wi-Fi.

## Connection path

```
   iPhone                                                  Mac Mini
 ┌─────────────────────────┐                       ┌───────────────────────────┐
 │ Tailscale iOS app:      │  WireGuard tunnel     │ tailscaled (daemon):      │
 │  100.x.x.y (your phone) │ ◄═══════════════════► │  100.x.x.x (the Mini)     │
 │  routes for tailnet IPs │  UDP (TCP/DERP        │  MagicDNS name "sputnik"  │
 │  MagicDNS resolver      │   fallback if blocked)│                           │
 └─────────┬───────────────┘                       └──────────┬────────────────┘
           │                                                  │
           │ Moshi resolves "sputnik"                         │ mosh-server binds
           │ via MagicDNS → 100.x.x.x                         │ to the tailnet IP
           ▼                                                  ▼
     mosh client over UDP ════════════════════════════►  mosh-server
     (Tailscale forwards through the WireGuard tunnel)         │
                                                               ▼
                                                        tmux new-session -A -s ccmux ccmux
                                                               │
                                                               ▼
                                                           ccmux TUI rendered
```

Key properties of this path:

- **Peer-to-peer when possible.** Tailscale uses NAT-punching so the phone and Mac talk directly, even when both are behind home routers. No "phone home" hop in your data plane.
- **Relay-through-DERP when necessary.** If either side is on a network that blocks UDP or aggressive symmetric NAT, traffic falls back to Tailscale's TCP relay (called DERP). Latency goes up a bit, but it still works on most hotel and corporate Wi-Fi.
- **End-to-end encrypted in either case.** WireGuard keys are negotiated peer-to-peer; relays only see ciphertext.
- **Stable identity.** Your Mac is `100.x.x.x` forever (until you remove and re-add it). Public IP changing, ISP rotating your home address, none of it matters.

## Why Tailscale and not something else

| Alternative | Problem for this workflow |
|---|---|
| Public SSH on a port-forwarded router | Exposes the SSH port to the internet, needs dynamic DNS, attracts brute-force traffic, requires fail2ban / SSH cert hygiene |
| ngrok, Cloudflare Tunnel | Third-party in the data path, latency, free tier session limits, frequently block UDP (which Mosh requires) |
| Reverse SSH to a bastion VPS | Brittle, requires a paid VPS, you maintain two machines, single point of failure |
| Consumer VPN (Mullvad, NordVPN…) | Hides outbound traffic — does **not** let you reach your home machine from elsewhere. Opposite problem. |
| WireGuard configured by hand | Works; you maintain the keys, the routing, the firewall rules, and the dynamic DNS. Tailscale is WireGuard with the management plane done for you. |
| **Tailscale** | Free for personal use, mesh networking, end-to-end WireGuard, no port forwarding, MagicDNS, works on hostile networks, audit trail of every device on your tailnet |

## What ccmux actually does with Tailscale

`ccmux` is a thin client of the network — it does not speak Tailscale's API directly. There are three concrete touchpoints, and that's it:

### 1. `ccmux doctor` checks for the binary

`ccmux doctor` runs `exec.LookPath("tailscale")`. If it's missing it prints the install link. There is no deeper integration — the daemon doesn't even need Tailscale to do its job on a local-only machine.

### 2. `ccmuxd` in server mode binds to the tailnet IP

When you flip `daemon.listen_tailnet = true` in `~/.config/ccmux/config.toml`, `ccmuxd` runs `tailscale ip -4` at startup, gets your `100.x.x.x`, and binds its HTTP listener to `100.x.x.x:7474` — **not** `0.0.0.0`. This means:

- Anything outside your tailnet (your wider LAN, the public internet) cannot reach the API.
- Anything on your tailnet (your phone, laptop, work machine if added) can.
- Tailscale's ACLs are your authorization layer if you want finer control (per-device rules).

The full bind logic lives in `cmd/ccmuxd/main.go` → `tailscaleAddr(port)`.

### 3. Remote hosts in `hosts.toml` use tailnet names

`ccmux host add mini sputnik` stores the MagicDNS name `sputnik` in `~/.config/ccmux/hosts.toml`. The client resolves it through the OS, which routes through Tailscale automatically. ccmux has **zero special-case code** for tailnet routing — it's just a hostname.

## What Tailscale does *not* do here

- **It's not your authentication system.** Anyone who has your phone + your SSH key (Moshi stores it in iOS Keychain, biometric unlock) + Mosh access can hit your Mac. The defense-in-depth is: passcode/biometric on the phone, SSH key in Keychain, Mac user permissions, optional `~/.ssh/authorized_keys` restrictions.
- **It doesn't relay shell input.** Mosh handles that, end to end. Tailscale is the transport, not the protocol.
- **It doesn't make sessions persistent.** tmux does. If the tmux server dies, the session is gone — Tailscale being up doesn't help. `ccmuxd`'s job is to keep tmux happy (sleep prevention, etc.).
- **It doesn't sync files.** That's separately on you (iCloud, Obsidian Sync if you opt in, git push for the source, etc.).

## Setup quick reference

On the Mac Mini (host):

```bash
# install
brew install tailscale && sudo tailscaled install-system-daemon
# or use the official .pkg from tailscale.com/download

# join your tailnet (opens browser for auth)
tailscale up

# verify
tailscale status
tailscale ip -4    # shows your 100.x.x.x
```

On the iPhone:

1. App Store → install **Tailscale**.
2. Sign in to the same tailnet.
3. Allow VPN profile installation when iOS prompts.
4. Verify: Tailscale app → "Connected" with your devices listed.

That's the whole setup. The Mac Mini and phone now talk over a private mesh; you can `ping sputnik` from either.

## MagicDNS

MagicDNS resolves your devices' short names to their tailnet IPs across the tailnet — `sputnik` instead of `100.64.0.42`.

To enable:

1. [Tailscale admin console](https://login.tailscale.com/admin/dns) → DNS tab.
2. Toggle **MagicDNS**.
3. Optional: enable HTTPS certificates (gives you `https://sputnik.tail-xxxxx.ts.net/`).

After it's on, you can use short names anywhere ccmux asks for a host (`ccmux host add mini sputnik`).

## Gotchas worth knowing

- **Phone's Tailscale toggle off → nothing works.** Most common failure mode; iOS aggressively kills VPN profiles when battery is low or after long sleep. Open the Tailscale app, flip the VPN switch on.
- **Hotel / corporate Wi-Fi blocks UDP.** Mosh can't establish over UDP, so Mosh-direct will fail. Tailscale's DERP TCP relay still works for SSH fallback. Set Moshi's connection type to **Auto** so it tries Mosh first then SSH.
- **MagicDNS short names occasionally flake** (DNS resolution race on iOS). If `sputnik` doesn't resolve, fall back to the full `sputnik.tail-xxxxx.ts.net` form, or the raw `100.x.x.x`.
- **Don't use your LAN IP for the Mac in Moshi.** Use the tailnet IP / MagicDNS name. Tailscale's routing won't engage for a LAN IP, and you'll silently lose the mesh's mobility benefits.
- **Subnet routes and exit nodes are off by default.** You don't need either for the ccmux flow. Turn them on only if you specifically want the Mac to route traffic for other LAN devices or to act as your phone's outbound IP.

## ACLs (when you want them)

For a single-user personal tailnet, the default "everyone can talk to everyone" ACL is fine. For sharing the Mac with a teammate or restricting which devices can reach `ccmuxd`'s API:

```jsonc
// Tailscale admin → Access Controls
{
  "acls": [
    // Only your devices can reach the Mini at all
    {
      "action": "accept",
      "src":    ["autogroup:owner"],
      "dst":    ["sputnik:*"]
    },
    // Reserve port 7474 (ccmuxd) for your iPhone and laptop only
    {
      "action": "accept",
      "src":    ["device:iphone", "device:laptop"],
      "dst":    ["sputnik:7474"]
    }
  ]
}
```

Most people will never touch this. It's worth knowing it exists.

## References

- [Tailscale documentation](https://tailscale.com/kb/) — install, MagicDNS, ACLs
- [Moshi's Tailscale notes](https://getmoshi.app/docs/tailscale) — confirmation that Moshi treats tailnet hosts as ordinary SSH/Mosh targets, no special integration required
- [WireGuard whitepaper](https://www.wireguard.com/papers/wireguard.pdf) — the cryptographic protocol underneath Tailscale
- `cmd/ccmuxd/main.go` → `tailscaleAddr(port)` — the only place ccmux's code paths reference Tailscale
