#!/usr/bin/env bash
# render.sh — render a ccmux demo tape to a GIF in an isolated,
# reproducible environment.
#
#   docs/vhs/render.sh docs/vhs/cuj02_dashboard.tape
#
# Isolation strategy:
#   - All tmux activity goes through a named socket ($TMUX_SOCK) so it
#     never touches the user's real server, even when run inside a tmux
#     session ($TMUX is unset before any subprocess starts).
#   - A tmux wrapper in $root/bin/ prepends -S $TMUX_SOCK so ccmuxd and
#     ccmux TUI also hit the isolated socket without any code changes.
#   - cleanup() kills only that socket; the user's sessions are untouched.
#
# Environment variables:
#   CCMUX_UPDATE_DEMO=true   — seed a git repo that's 1 commit behind
#                              so the dashboard's update banner appears.
#                              Used by cuj11_update.tape.
set -euo pipefail

tape="${1:?usage: render.sh <tape.tape>}"
repo="$(cd "$(dirname "$0")/../.." && pwd)"

command -v vhs >/dev/null 2>&1 || { echo "render: vhs not installed — 'brew install vhs'"; exit 1; }
[ -x "$repo/bin/ccmux" ] && [ -x "$repo/bin/ccmuxd" ] || { echo "render: build first — 'make build'"; exit 1; }

REAL_TMUX="$(command -v tmux)"
REAL_HOME="$HOME"
root="$(mktemp -d "${TMPDIR:-/tmp}/ccmux-vhs.XXXXXX")"
TMUX_SOCK="$root/tmux.sock"

export HOME="$root/home"
export XDG_CONFIG_HOME="$HOME/.config"
export XDG_STATE_HOME="$HOME/.local/state"
mkdir -p "$HOME/Projects" "$root/bin" "$HOME/.config/ccmux" \
         "$HOME/.local/state/ccmux" "$HOME/.local/bin"
export PATH="$root/bin:$HOME/.local/bin:$PATH"

# Agent credentials live in the real HOME (Keychain + config files).
# The agent wrapper scripts in $root/bin/ restore HOME="$REAL_HOME" before
# exec'ing each agent, so no credential files need to be copied here.

# Break out of any parent tmux session so subprocesses don't inherit its socket.
unset TMUX
unset TMUX_TMPDIR

cleanup() {
  "$REAL_TMUX" -S "$TMUX_SOCK" kill-server 2>/dev/null || true
  pkill -f "ccmuxd.*$root" 2>/dev/null || true
  rm -rf "$root"
}
trap cleanup EXIT

# --- tmux wrapper: every process that invokes 'tmux' hits the isolated socket.
cat > "$root/bin/tmux" <<WRAPPER
#!/bin/sh
exec "$REAL_TMUX" -S "$TMUX_SOCK" "\$@"
WRAPPER
chmod +x "$root/bin/tmux"

# Shorthand for tmux calls within this script (real binary + socket).
T() { "$REAL_TMUX" -S "$TMUX_SOCK" "$@"; }

# Agent wrappers — restore REAL_HOME before exec'ing each agent so they
# authenticate against the real keychain/config, while ccmux/ccmuxd keep
# using the fake HOME for projects, conversations, and notes.
REAL_CLAUDE="$(command -v claude)"
REAL_AGY="$(command -v agy)"
REAL_CODEX="$(command -v codex)"

cat > "$root/bin/claude" <<WRAP
#!/bin/sh
exec env HOME="$REAL_HOME" "$REAL_CLAUDE" "\$@"
WRAP
chmod +x "$root/bin/claude"

cat > "$root/bin/agy" <<WRAP
#!/bin/sh
exec env HOME="$REAL_HOME" "$REAL_AGY" "\$@"
WRAP
chmod +x "$root/bin/agy"

cat > "$root/bin/codex" <<WRAP
#!/bin/sh
exec env HOME="$REAL_HOME" "$REAL_CODEX" "\$@"
WRAP
chmod +x "$root/bin/codex"

cp "$repo/bin/ccmux" "$repo/bin/ccmuxd" "$root/bin/"

# Add ccmux to .local/bin so 'ccmux doctor' PATH check passes.
ln -sf "$root/bin/ccmux" "$HOME/.local/bin/ccmux"

# --- Shell prompt (suppress zsh new-user wizard) ---------------------
printf "PROMPT='%%F{magenta}\xe2\x9d\xaf%%f '\nZSH_DISABLE_COMPFIX=true\n" > "$HOME/.zshrc"
touch "$HOME/.zshenv"

# --- Config + fake projects ------------------------------------------
auto_check="false"
if [ "${CCMUX_UPDATE_DEMO:-}" = "true" ]; then
  auto_check="true"
fi

cat > "$HOME/.config/ccmux/config.toml" <<CFG
[projects]
root = "$HOME/Projects"

[tour]
shown = true
shown_version = "0.0"

[update]
auto_check = $auto_check
CFG

# --- Projects --------------------------------------------------------
mkdir -p "$HOME/Projects/auth-service/.git" \
         "$HOME/Projects/web-dashboard/.git" \
         "$HOME/Projects/data-pipeline/.git" \
         "$HOME/Projects/mobile-app/.git"
# ccmux project gets a real git repo for the update-check path.
mkdir -p "$HOME/Projects/ccmux"

echo "# auth-service"  > "$HOME/Projects/auth-service/CLAUDE.md"
echo "# web-dashboard" > "$HOME/Projects/web-dashboard/CLAUDE.md"
echo "# data-pipeline" > "$HOME/Projects/data-pipeline/CLAUDE.md"
echo "# mobile-app"    > "$HOME/Projects/mobile-app/CLAUDE.md"

mkdir -p "$HOME/Projects/web-dashboard/.ccmux"
echo "codex"  > "$HOME/Projects/web-dashboard/.ccmux/agent"
mkdir -p "$HOME/Projects/data-pipeline/.ccmux"
echo "agy"    > "$HOME/Projects/data-pipeline/.ccmux/agent"
mkdir -p "$HOME/Projects/mobile-app/.ccmux"
echo "claude" > "$HOME/Projects/mobile-app/.ccmux/agent"

# --- Markdown notes --------------------------------------------------
mkdir -p "$HOME/Projects/auth-service/docs" \
         "$HOME/Projects/web-dashboard/docs" \
         "$HOME/Projects/data-pipeline/docs" \
         "$HOME/Projects/mobile-app/docs"

cat > "$HOME/Projects/auth-service/README.md" <<'MD'
# auth-service

JWT authentication middleware for the API gateway. Handles token
issuance, validation, and refresh for all downstream microservices.

## Quick start

```bash
go run . --addr :8080 --db postgres://localhost/auth
```

## Architecture

- **RS256** asymmetric signing — downstream services verify with the public key only
- **Stateless JWTs** — 15 min access tokens; no session store needed
- **Refresh rotation** — each `/auth/refresh` call invalidates the old token
- **Blacklist** — in-memory LRU + Redis for immediate high-severity revocation

## Services

| Endpoint       | Method | Auth required |
|----------------|--------|---------------|
| /auth/login    | POST   | No            |
| /auth/refresh  | POST   | No            |
| /auth/validate | GET    | Bearer token  |
| /auth/revoke   | POST   | Bearer token  |
| /auth/jwks     | GET    | No            |
MD

cat > "$HOME/Projects/auth-service/docs/architecture.md" <<'MD'
# Architecture

## Token lifecycle

1. Client POSTs credentials to `/auth/login`
2. Server validates password (bcrypt, cost 12)
3. Issues **JWT** (RS256, 15 min) + opaque refresh token (7 days)
4. Client sends `Authorization: Bearer <jwt>` on every request
5. On 401 the client POSTs the refresh token to `/auth/refresh`
6. Server rotates: old refresh token invalidated, new pair issued

## Key decisions

- **RS256 over HS256** — private key stays in auth-service; downstream
  services fetch the public key from `/auth/jwks` and verify locally,
  no round-trip to auth-service on every request
- **Short-lived access tokens** — 15 min TTL is short enough that
  blacklisting is rarely needed; the refresh path is the revocation surface
- **Opaque refresh tokens** — stored as bcrypt hashes in Postgres;
  token value is never persisted in plaintext

## Data model

```sql
CREATE TABLE refresh_tokens (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users(id),
  token_hash TEXT NOT NULL,
  issued_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at TIMESTAMPTZ NOT NULL,
  revoked_at TIMESTAMPTZ
);

CREATE INDEX ON refresh_tokens (user_id, revoked_at);
```
MD

cat > "$HOME/Projects/auth-service/docs/api.md" <<'MD'
# API Reference

## POST /auth/login

```json
// Request
{ "email": "user@example.com", "password": "hunter2" }

// Response 200
{
  "access_token":  "<jwt>",
  "refresh_token": "<opaque-64-bytes-hex>",
  "expires_in":    900,
  "token_type":    "Bearer"
}
```

Errors: `401 invalid credentials` · `429 rate limit exceeded`

## POST /auth/refresh

```json
// Request
{ "refresh_token": "<opaque>" }

// Response 200 — same shape as /login
```

The old refresh token is immediately invalidated. A second call with the
same token returns `401 token already rotated`.

## GET /auth/jwks

Returns the public key set in JWKS format. Downstream services should
cache this response and re-fetch on `kid` mismatch.

## POST /auth/revoke

Revokes an access or refresh token immediately. Requires a valid
Bearer token in the `Authorization` header.
MD

cat > "$HOME/Projects/auth-service/docs/decisions.md" <<'MD'
# Architecture Decision Records

## ADR-001: Asymmetric signing (RS256)

**Status:** Accepted · **Date:** 2026-04-12

**Context:** Multiple downstream services need to verify JWTs without
coupling to auth-service availability.

**Decision:** RS256 with a 4096-bit RSA key pair. Private key in
auth-service only; public key exposed via `/auth/jwks` and cached
in each downstream service (5 min TTL, re-fetch on `kid` miss).

**Consequences:** Downstream services tolerate auth-service restarts
gracefully. Key rotation requires a JWKS republish and a short overlap
window where both old and new keys are valid.

---

## ADR-002: Opaque refresh tokens (not JWTs)

**Status:** Accepted · **Date:** 2026-04-14

**Context:** Refresh tokens need to be revocable on demand (account
compromise, explicit logout). JWTs are self-contained and cannot be
revoked without a blocklist.

**Decision:** 64-byte random refresh tokens, stored as bcrypt hashes
in Postgres. Rotation-on-use makes replay attacks immediately visible.

**Consequences:** Refresh validation requires a DB lookup. Acceptable
because refresh calls are infrequent (every 15 min per client).

---

## ADR-003: Rate limiting on /auth/login

**Status:** Accepted · **Date:** 2026-04-20

**Decision:** 10 req/min per IP, 5 req/min per email. Implemented in
the auth-service middleware layer (not the API gateway) so the limit
applies even when the gateway is misconfigured.
MD

cat > "$HOME/Projects/auth-service/docs/runbook.md" <<'MD'
# Ops Runbook

## High login error rate

1. Check `auth_login_errors_total` in Grafana → auth dashboard
2. If 401 spike: likely credential stuffing — check IP diversity
   ```bash
   psql $AUTH_DB -c "SELECT ip, count(*) FROM login_attempts
     WHERE created_at > now() - interval '5 min'
     GROUP BY ip ORDER BY count DESC LIMIT 20;"
   ```
3. If 500 spike: check DB connectivity
   ```bash
   kubectl exec -it deploy/auth-service -- /bin/sh -c \
     "pg_isready -h $DB_HOST -U auth"
   ```

## Rotate the signing key

1. Generate new key pair:
   ```bash
   openssl genrsa -out new_private.pem 4096
   openssl rsa -in new_private.pem -pubout -out new_public.pem
   ```
2. Update Kubernetes secret — both keys active during overlap:
   ```bash
   kubectl create secret generic auth-keys \
     --from-file=private=new_private.pem \
     --from-file=old-public=old_public.pem \
     --from-file=public=new_public.pem \
     --dry-run=client -o yaml | kubectl apply -f -
   ```
3. Rolling-restart auth-service (picks up new secret)
4. After 30 min (all old JWTs expired), remove `old-public` from secret
MD

cat > "$HOME/Projects/web-dashboard/README.md" <<'MD'
# web-dashboard

React + Vite ops dashboard. Shows live session counts, agent health,
token-quota burn, and Tailscale peer status from the ccmux daemon.

## Stack

| Layer      | Library                |
|------------|------------------------|
| Build      | Vite 5 + SWC           |
| UI         | React 18 + TypeScript  |
| Components | shadcn/ui + Tailwind   |
| Data       | SWR (5 s poll)         |
| Charts     | Recharts               |

## Running locally

```bash
npm install
VITE_API_BASE=http://localhost:7474 npm run dev
```
MD

cat > "$HOME/Projects/web-dashboard/docs/components.md" <<'MD'
# Component Reference

## SessionList

Renders the live session table. Polls `/api/sessions` every 5 s via SWR.
State badges use Tailwind variants:

| State       | Badge class             |
|-------------|-------------------------|
| active      | `badge-green`           |
| needs_input | `badge-red animate-pulse` |
| idle        | `badge-yellow`          |

```tsx
<SessionList
  filter="needs_input"   // optional — omit to show all
  onSelect={(id) => ...}
/>
```

## QuotaBar

Horizontal progress bar for the 5-hour token window. Turns amber at
80 %, red at 95 %.

```tsx
<QuotaBar used={42350} limit={88000} />
```

## PeerGrid

Tailscale device grid. Polls `/api/peers` every 30 s.
Shows ccmuxd version badge and "update available" indicator.
MD

cat > "$HOME/Projects/web-dashboard/docs/data-fetching.md" <<'MD'
# Data Fetching

All data comes from the local `ccmuxd` HTTP API (default `:7474`).

## Endpoints used

| Endpoint          | Interval | Component     |
|-------------------|----------|---------------|
| GET /v1/sessions  | 5 s      | SessionList   |
| GET /v1/usage     | 60 s     | QuotaBar      |
| GET /v1/peers     | 30 s     | PeerGrid      |
| GET /v1/health    | 10 s     | StatusDot     |

## SWR config

```ts
const { data, error } = useSWR('/v1/sessions', fetcher, {
  refreshInterval: 5000,
  revalidateOnFocus: true,
  dedupingInterval: 2000,
})
```

## Error handling

On network error, the last good data is kept visible with a
`stale` indicator in the status bar. Hard error (never loaded)
shows a skeleton + retry button.
MD

cat > "$HOME/Projects/data-pipeline/README.md" <<'MD'
# data-pipeline

Nightly ETL pipeline. Reads from `user_events` (Postgres), normalises
and enriches, loads into the analytics warehouse (BigQuery).

## Schedule

Runs via Cloud Scheduler at **02:00 UTC** daily. Incremental load:
only rows with `created_at > last_watermark` are processed.

## Quick start

```bash
python -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
python -m pipeline.run --dry-run
```

## Pipeline stages

1. **Extract** — chunked read from Postgres (10 k rows/batch)
2. **Validate** — Pydantic models; invalid rows written to `errors/`
3. **Transform** — normalise event payloads, resolve user IDs
4. **Enrich** — join with `dim_users` for plan + cohort attributes
5. **Load** — streaming insert into BigQuery via Storage Write API
MD

cat > "$HOME/Projects/data-pipeline/docs/schema.md" <<'MD'
# Schema Reference

## Source: user_events (Postgres)

```sql
CREATE TABLE user_events (
  id          BIGSERIAL PRIMARY KEY,
  user_id     UUID NOT NULL,
  event_type  TEXT NOT NULL,        -- 'session_start' | 'session_end' | 'tool_call' ...
  payload     JSONB,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX ON user_events (created_at);
CREATE INDEX ON user_events (user_id, event_type);
```

## Destination: analytics.fact_events (BigQuery)

| Column         | Type      | Notes                         |
|----------------|-----------|-------------------------------|
| event_id       | INT64     | Source PK                     |
| user_id        | STRING    | UUID as string                |
| event_type     | STRING    |                               |
| plan           | STRING    | Joined from dim_users         |
| cohort_month   | DATE      | First seen month              |
| payload        | JSON      | Normalised subset             |
| event_date     | DATE      | Partition column              |
| loaded_at      | TIMESTAMP | Pipeline run timestamp        |

## Watermark table

```sql
CREATE TABLE pipeline_watermarks (
  pipeline_name TEXT PRIMARY KEY,
  last_id       BIGINT NOT NULL DEFAULT 0,
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```
MD

cat > "$HOME/Projects/data-pipeline/docs/pipeline.md" <<'MD'
# Pipeline Details

## Incremental load

The pipeline reads `last_id` from `pipeline_watermarks` and fetches
only rows with `id > last_id`. After a successful load the watermark
is updated inside the same Postgres transaction as the source read.

```python
with pg.transaction():
    watermark = fetch_watermark("user_events")
    rows = fetch_rows(since_id=watermark)
    load_to_bigquery(rows)
    update_watermark("user_events", rows[-1].id)
```

## Error handling

- **Validation errors** — written to `gs://pipeline-errors/YYYY-MM-DD/`
  as newline-delimited JSON. Never block a run.
- **BQ insert errors** — retried 3× with exponential back-off (1 s, 4 s, 16 s).
  If all retries fail, the run is marked failed and PagerDuty fires.
- **Source DB unavailable** — run exits immediately; Cloud Scheduler
  retries after 30 min (max 3 retries).

## Monitoring

Metrics exported to Cloud Monitoring:
- `pipeline/rows_loaded` — rows successfully written to BQ
- `pipeline/rows_errored` — validation failures
- `pipeline/duration_seconds` — end-to-end wall time
MD

cat > "$HOME/Projects/mobile-app/README.md" <<'MD'
# mobile-app

React Native app for iOS and Android. Wraps the ccmux TUI experience
in a mobile shell with native push notifications and biometric auth.

## Requirements

- Node 22+ · Ruby 3.3+ · Xcode 16+ (iOS) · Android Studio (Android)
- `npx expo install` to sync native deps after `npm install`

## Running

```bash
# iOS simulator
npx expo run:ios

# Physical device (requires Apple Developer account)
npx expo run:ios --device
```

## Architecture

```
app/
  (tabs)/
    index.tsx      — Session list (WebSocket feed from ccmuxd)
    conversations.tsx
    settings.tsx
  session/[id].tsx — Embedded terminal (xterm.js in WebView)
components/
  SessionRow.tsx
  QuotaBadge.tsx
  PushStatus.tsx
```
MD

cat > "$HOME/Projects/mobile-app/docs/auth-flow.md" <<'MD'
# Authentication Flow

## First launch

1. App opens to onboarding → user enters ccmuxd host + port
2. App calls `GET /v1/health` to verify reachability
3. If Tailscale is detected, tailnet peers are listed automatically
4. User taps **Connect** → WebSocket handshake to `/v1/ws`

## Biometric unlock

After the first successful connection the session token is stored in
the iOS Keychain (kSecAttrAccessibleWhenUnlockedThisDeviceOnly).
Subsequent app opens trigger Face ID / Touch ID; on success the stored
token is used without re-entering the host address.

```swift
let context = LAContext()
context.evaluatePolicy(
    .deviceOwnerAuthenticationWithBiometrics,
    localizedReason: "Unlock ccmux"
) { success, error in
    if success { connectWithStoredToken() }
}
```

## Push notifications

Moshi handles push. On first connect the app registers an APNs token
and POSTs it to `ccmuxd /v1/push/register`. The daemon pushes
`needs_input` events categorised as `approval_required` or
`task_complete`.
MD

cat > "$HOME/Projects/mobile-app/docs/setup.md" <<'MD'
# Development Setup

## iOS certificates

1. Open `ios/` in Xcode → Signing & Capabilities → enable automatic signing
2. Select your personal team for development builds
3. For push notifications: add the **Push Notifications** capability
   and upload the APNs key to the Moshi dashboard

## Environment variables

Copy `.env.example` to `.env.local`:

```
CCMUXD_HOST=your-machine.tailnet.ts.net
CCMUXD_PORT=7474
MOSHI_APP_ID=your-moshi-app-id
```

## EAS Build (CI)

```bash
eas build --profile development --platform ios
eas build --profile production  --platform all
```

Builds are triggered automatically on push to `main` via the
`.github/workflows/eas.yml` workflow.
MD

# --- Impressive showcase notes (Glamour rendering: diagrams, tables, code) ---

cat > "$HOME/Projects/auth-service/docs/token-lifecycle.md" <<'MD'
# Token Lifecycle

## End-to-end sequence

```
Client          auth-service         Postgres          Downstream
  │                   │                  │                   │
  │── POST /login ───>│                  │                   │
  │                   │── SELECT user ──>│                   │
  │                   │<─ row ───────────│                   │
  │                   │  bcrypt verify   │                   │
  │                   │── INSERT token ─>│                   │
  │<─ {access, refresh} ────────────────│                   │
  │                   │                  │                   │
  │── GET /api/data Authorization: Bearer ─────────────────>│
  │                   │                  │   verify RS256 key│
  │<─────────────────────────────────── 200 ────────────────│
  │                   │                  │                   │
  │── POST /auth/refresh ─────────────>│                   │
  │                   │── SELECT token ─>│                   │
  │                   │── UPDATE revoked>│                   │
  │                   │── INSERT new ───>│                   │
  │<─ {new_access, new_refresh} ────────│                   │
```

## JWT claims

| Claim | Value          | Notes                              |
|-------|----------------|------------------------------------|
| `sub` | `uuid`         | User ID                            |
| `iss` | `auth-service` | Issuer                             |
| `aud` | `api-gateway`  | Intended consumer                  |
| `iat` | Unix epoch     | Issued-at                          |
| `exp` | `iat + 900`    | 15-minute access window            |
| `kid` | `v3`           | Key version for JWKS lookup        |
| `plan`| `pro`          | Subscription tier (custom claim)   |

## Rotation invariants

1. Every `/auth/refresh` call **must** atomically invalidate the old token and issue the new one — no window where both are valid
2. On replay (`token_hash` already has `revoked_at`) → `401 token already rotated`; do not issue a new pair
3. Access token TTL is **never** extended — clients must refresh before expiry
4. JWKS cache TTL on downstream services: **5 min** + re-fetch on `kid` miss
MD

cat > "$HOME/Projects/auth-service/docs/security-model.md" <<'MD'
# Security Model

## Trust boundary

```
┌─────────────────────────────────────────────────────────┐
│  Public internet                                        │
│                                                         │
│   [Browser / Mobile]──── TLS 1.3 ────[API Gateway]     │
│                                             │           │
└─────────────────────────────────────────────────────────┘
                                             │ mTLS
┌─────────────────────────────────────────────────────────┐
│  Internal network (VPC)                                 │
│                                             ▼           │
│                                      [auth-service]     │
│                                          │    │         │
│                                     [Postgres][Redis]   │
└─────────────────────────────────────────────────────────┘
```

## Credential storage

| Secret              | Where stored                  | At-rest protection            |
|---------------------|-------------------------------|-------------------------------|
| User password       | Postgres `users.password_hash`| bcrypt cost 12                |
| Refresh token       | Postgres `refresh_tokens.hash`| bcrypt cost 10                |
| RSA private key     | Kubernetes Secret             | SOPS + KMS envelope           |
| Service credentials | Vault dynamic secrets         | Rotated every 24 h            |

## Attack mitigations

| Vector               | Mitigation                                             |
|----------------------|--------------------------------------------------------|
| Credential stuffing  | 10 req/min/IP + 5 req/min/email token buckets          |
| Timing attacks       | `subtle.ConstantTimeCompare` on all token comparisons  |
| User enumeration     | Unified error: `401 invalid credentials`               |
| Token replay         | Rotation-on-use; replay immediately detects tampering  |
| JWT algorithm confusion| `alg` field validated; `none` explicitly rejected    |
| JWKS poisoning       | Keys pinned by `kid`; stale entries rejected after 1 h |

## Security headers (every response)

```http
Strict-Transport-Security: max-age=63072000; includeSubDomains
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Content-Security-Policy: default-src 'none'
```
MD

cat > "$HOME/Projects/data-pipeline/docs/performance.md" <<'MD'
# Performance Analysis

## Extract phase benchmarks

| Strategy                      | 200M rows   | p99 latency | Memory    |
|-------------------------------|-------------|-------------|-----------|
| Sequential 10k-row chunks     | 4 h 12 min  | —           | 480 MB    |
| Parallel 8 workers, range split| 38 min     | —           | 1.2 GB    |
| Parallel + server-side cursor | **31 min**  | —           | **310 MB**|

Winning config: 8 parallel workers with server-side cursors and a
covering index on `(id, created_at, user_id, event_type)`.

## Query plan: before vs after index

**Before** (sequential scan, 4h+ runtime):

```sql
EXPLAIN ANALYZE
SELECT id, user_id, event_type, payload, created_at
FROM user_events WHERE id > 182400000 LIMIT 10000;

Seq Scan on user_events  (cost=0.00..4821043.20 rows=10000)
  Filter: (id > 182400000)
  Rows Removed by Filter: 182399999
Planning Time: 0.3 ms  Execution Time: 94823.4 ms
```

**After** (index scan, <50ms):

```sql
Index Scan using user_events_pkey on user_events
  (cost=0.57..820.57 rows=10000)
  Index Cond: (id > 182400000)
Planning Time: 0.1 ms  Execution Time: 42.7 ms
```

## BigQuery Storage Write API throughput

```python
# Optimal batch configuration (empirically tuned):
BATCH_SIZE   = 5_000     # rows per proto batch
MAX_WORKERS  = 16        # parallel streams to BQ
COMMIT_EVERY = 50_000    # rows between watermark commits

# Achieved: ~180k rows/min at steady state
# BQ slot utilisation: ~40% (on-demand pricing)
```

## Memory profile (8-worker run)

| Phase         | Peak RSS  | Notes                              |
|---------------|-----------|------------------------------------|
| Extract       | 312 MB    | Server-side cursor, 10k buffer     |
| Validate      | 48 MB     | Pydantic models gc'd per batch     |
| Transform     | 22 MB     | Streaming — no full-dataset hold   |
| BQ load       | 110 MB    | Proto serialisation buffer         |
| **Total peak**| **492 MB**| Well within 2 GB pod limit         |
MD

cat > "$HOME/Projects/web-dashboard/docs/architecture.md" <<'MD'
# Frontend Architecture

## Component tree

```
<App>
 ├── <Header>          — nav, user badge, connection status dot
 ├── <Dashboard>
 │    ├── <QuotaBar>   — 5-hour token window (SWR 60 s)
 │    ├── <SessionList>— live session table (SWR 5 s)
 │    │    └── <SessionRow> × N
 │    │         ├── <StateBadge>   — active / needs_input / idle
 │    │         └── <AgentBadge>   — claude / codex / agy
 │    └── <PeerGrid>   — Tailscale peers (SWR 30 s)
 │         └── <PeerCard> × N
 ├── <ConversationPanel> (slide-over)
 │    └── <MessageList>
 └── <SettingsSheet>
```

## Data flow

```
ccmuxd HTTP API (:7474)
        │
        ▼
   SWR fetcher (revalidateOnFocus, dedupingInterval: 2000ms)
        │
   React context (SessionContext, PeerContext)
        │
   Memoised selectors (useMemo) → child components
        │
   React.memo boundaries → skip re-render when data unchanged
```

## SWR configuration

```typescript
const swrConfig: SWRConfiguration = {
  fetcher: (url: string) =>
    fetch(`${import.meta.env.VITE_API_BASE}${url}`)
      .then(r => { if (!r.ok) throw new Error(r.statusText); return r.json(); }),
  onError: (err) => console.error('[swr]', err),
  shouldRetryOnError: true,
  errorRetryCount: 3,
  errorRetryInterval: 5000,
}

// Per-hook overrides:
// Sessions: { refreshInterval: 5000, revalidateOnFocus: true }
// Peers:    { refreshInterval: 30000 }
// Usage:    { refreshInterval: 60000 }
```

## Error states

| Condition              | UI behaviour                                      |
|------------------------|---------------------------------------------------|
| Never loaded           | Full-page skeleton + "Connecting…" spinner        |
| Stale (network error)  | Last data shown + amber "stale" badge in header   |
| 401 Unauthorized       | Redirect to `/connect` — re-enter host address    |
| ccmuxd version too old | Banner: "Update ccmuxd to ≥ 0.9 for all features"|
MD

cat > "$HOME/Projects/mobile-app/docs/architecture.md" <<'MD'
# Mobile Architecture

## Navigation tree

```
<Stack>
 ├── (tabs)/
 │    ├── index           — Session list (WebSocket feed)
 │    │    └── SessionRow — tap → session detail
 │    ├── conversations   — Past threads, resume on tap
 │    └── settings        — Host config, biometric toggle
 ├── session/[id]         — Embedded terminal (xterm.js / WebView)
 └── connect              — First-launch host setup
```

## WebSocket event types

| Event type        | Payload fields                       | UI action                    |
|-------------------|--------------------------------------|------------------------------|
| `session_update`  | `id, state, agent, project, ts`      | Update SessionRow in place   |
| `session_new`     | full session object                  | Prepend to list              |
| `session_removed` | `id`                                 | Remove row, cancel badge     |
| `needs_input`     | `id, session_name, message_preview`  | Push notification + badge    |
| `quota_update`    | `used, limit, window_start`          | Refresh QuotaBadge           |
| `peer_update`     | `peers[]`                            | (reserved — not shown in UI) |

## Offline behaviour

```
                  ┌─────────────────┐
Network OK ──────>│  Live WebSocket │──> MMKV snapshot every 30 s
                  └────────┬────────┘
                           │ disconnect
                           ▼
                  ┌─────────────────┐
                  │  Stale banner   │   "Last updated 2 min ago"
                  │  MMKV hydrated  │──> SessionList still scrollable
                  └────────┬────────┘
                           │ reconnect
                           ▼
                  ┌─────────────────┐
                  │ Full sync fetch │──> diff applied, banner clears
                  └─────────────────┘
```

## Key libraries

| Concern            | Library                              | Notes                        |
|--------------------|--------------------------------------|------------------------------|
| Navigation         | Expo Router 3 (file-based)           | Type-safe `href` props       |
| Terminal emulator  | `@xterm/xterm` in WebView            | OSC 52 clipboard bridged     |
| Secure storage     | `expo-secure-store`                  | iOS Keychain backed          |
| Biometric auth     | `expo-local-authentication`          | FaceID / TouchID             |
| Fast KV cache      | `react-native-mmkv`                  | Synchronous, JSI-based       |
| Push notifications | Moshi SDK                            | APNs + FCM unified           |
MD

# --- Claude global config (for C9 agents screen) ---------------------
mkdir -p "$HOME/.claude"
cat > "$HOME/.claude/CLAUDE.md" <<'MD'
# Global agent instructions

- Default to concise answers; expand only when asked
- Use structured output (tables, JSON) for anything tabular or comparative
- Prefer TypeScript for new frontend code; Go for backend services
- Always write tests alongside implementation — no PR ships without them
- For database changes, always include a rollback migration
- When touching auth or crypto code, note security implications explicitly
MD

mkdir -p "$HOME/.claude/commands"
cat > "$HOME/.claude/commands/review.md" <<'MD'
Review the current file for correctness, test coverage, and style.
Flag any security issues first, then correctness issues, then style.
MD

cat > "$HOME/.claude/commands/explain.md" <<'MD'
Explain the selected code to a new team member who knows the language
but is unfamiliar with this codebase. Cover: what it does, why it
exists, and any non-obvious invariants.
MD

cat > "$HOME/.claude/commands/perf.md" <<'MD'
Profile the current file or function for performance issues.
Identify the top bottleneck and suggest a concrete fix with a benchmark.
MD

cat > "$HOME/.claude/commands/migrate.md" <<'MD'
Write a database migration for the described schema change.
Include: up migration, down migration, and index considerations.
Always wrap in a transaction. Note any zero-downtime constraints.
MD

cat > "$HOME/.claude/commands/security.md" <<'MD'
Audit the current file for security vulnerabilities.
Check for: injection, auth bypass, secrets in code, insecure defaults,
missing input validation, and OWASP Top 10 issues.
MD

# --- Conversations ---------------------------------------------------
# Claude encodes a project's cwd into one flat directory name by
# replacing EVERY '/' with '-'. ${HOME//\//-} already dashes the home
# prefix; append the dashed Projects/<name> tail so the whole path is
# one flat segment (a stray '/' here nests the dir and ListClaude,
# which only reads one level under ~/.claude/projects, finds nothing).
auth_enc="${HOME//\//-}-Projects-auth-service"
web_enc="${HOME//\//-}-Projects-web-dashboard"
pipe_enc="${HOME//\//-}-Projects-data-pipeline"
mob_enc="${HOME//\//-}-Projects-mobile-app"

mkdir -p "$HOME/.claude/projects/$auth_enc" \
         "$HOME/.claude/projects/$web_enc"  \
         "$HOME/.claude/projects/$pipe_enc" \
         "$HOME/.claude/projects/$mob_enc"

# auth-service conversations
cat > "$HOME/.claude/projects/$auth_enc/aaaaaaaa-0001-0001-0001-000000000001.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"Implement JWT authentication middleware with RS256 signing and refresh-token rotation"},"timestamp":"2026-05-21T08:30:00.000Z","cwd":"$HOME/Projects/auth-service"}
{"type":"assistant","message":{"role":"assistant","content":"I'll implement the JWT middleware. Starting with the key pair generation, then the sign/verify helpers, then the HTTP middleware layer..."},"timestamp":"2026-05-21T08:30:05.000Z"}
{"type":"user","message":{"role":"user","content":"Add the JWKS endpoint so downstream services can fetch the public key"},"timestamp":"2026-05-21T09:15:00.000Z","cwd":"$HOME/Projects/auth-service"}
{"type":"assistant","message":{"role":"assistant","content":"Adding GET /auth/jwks — returns the RSA public key in JWK format with kid, use, alg fields..."},"timestamp":"2026-05-21T09:15:08.000Z"}
EOF

cat > "$HOME/.claude/projects/$auth_enc/aaaaaaaa-0001-0001-0001-000000000002.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"Write integration tests for /auth/refresh — happy path, expired token, replay attack"},"timestamp":"2026-05-20T16:20:00.000Z","cwd":"$HOME/Projects/auth-service"}
{"type":"assistant","message":{"role":"assistant","content":"I'll write table-driven tests covering: successful rotation, expired refresh token (401), replayed token (401 already rotated), and concurrent rotation race..."},"timestamp":"2026-05-20T16:20:10.000Z"}
EOF

cat > "$HOME/.claude/projects/$auth_enc/aaaaaaaa-0001-0001-0001-000000000003.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"Add rate limiting on /auth/login — 10 req/min per IP, 5 req/min per email"},"timestamp":"2026-05-19T14:00:00.000Z","cwd":"$HOME/Projects/auth-service"}
{"type":"assistant","message":{"role":"assistant","content":"I'll add a two-key rate limiter using a token bucket in Redis. IP bucket: 10/min. Email bucket: 5/min. Returns 429 with Retry-After header on breach..."},"timestamp":"2026-05-19T14:00:12.000Z"}
EOF

cat > "$HOME/.claude/projects/$auth_enc/aaaaaaaa-0001-0001-0001-000000000004.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"Security audit: check the auth middleware for timing attacks and token leakage"},"timestamp":"2026-05-18T11:30:00.000Z","cwd":"$HOME/Projects/auth-service"}
{"type":"assistant","message":{"role":"assistant","content":"Audit complete. Found 2 issues: (1) bcrypt compare uses == instead of subtle.ConstantTimeCompare on the token hash — fixable. (2) error messages distinguish 'user not found' from 'wrong password' — should unify to prevent user enumeration..."},"timestamp":"2026-05-18T11:30:20.000Z"}
EOF

# web-dashboard conversations
cat > "$HOME/.claude/projects/$web_enc/bbbbbbbb-0002-0002-0002-000000000001.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"Add a live session-count panel with SWR — poll /api/sessions every 5s"},"timestamp":"2026-05-21T14:45:00.000Z","cwd":"$HOME/Projects/web-dashboard"}
{"type":"assistant","message":{"role":"assistant","content":"I'll create SessionPanel.tsx using useSWR with refreshInterval: 5000. The panel shows counts per state (active/needs_input/idle) with color-coded badges..."},"timestamp":"2026-05-21T14:45:08.000Z"}
EOF

cat > "$HOME/.claude/projects/$web_enc/bbbbbbbb-0002-0002-0002-000000000002.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"The QuotaBar component is re-rendering every 5s even when the value hasn't changed — fix it"},"timestamp":"2026-05-20T10:20:00.000Z","cwd":"$HOME/Projects/web-dashboard"}
{"type":"assistant","message":{"role":"assistant","content":"The issue is that useSWR returns a new object reference even when data is identical, triggering re-renders. Fix: wrap QuotaBar in React.memo and memoize the derived values with useMemo..."},"timestamp":"2026-05-20T10:20:15.000Z"}
EOF

cat > "$HOME/.claude/projects/$web_enc/bbbbbbbb-0002-0002-0002-000000000003.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"Bundle size is 890 KB gzipped — get it under 300 KB"},"timestamp":"2026-05-17T09:00:00.000Z","cwd":"$HOME/Projects/web-dashboard"}
{"type":"assistant","message":{"role":"assistant","content":"Running bundle analysis... Recharts is 180 KB alone. Switching to lightweight chart primitives and tree-shaking shadcn imports drops it to 270 KB gzipped..."},"timestamp":"2026-05-17T09:00:30.000Z"}
EOF

# data-pipeline conversations
cat > "$HOME/.claude/projects/$pipe_enc/cccccccc-0003-0003-0003-000000000001.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"Build the ETL pipeline for the user_events table — incremental load with watermark"},"timestamp":"2026-05-21T22:00:00.000Z","cwd":"$HOME/Projects/data-pipeline"}
{"type":"assistant","message":{"role":"assistant","content":"I'll implement a watermark-based incremental load. The pipeline reads last_id from pipeline_watermarks, fetches events in 10k-row chunks, validates with Pydantic, and streams to BigQuery via the Storage Write API..."},"timestamp":"2026-05-21T22:00:18.000Z"}
EOF

cat > "$HOME/.claude/projects/$pipe_enc/cccccccc-0003-0003-0003-000000000002.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"The nightly run is timing out — events table has 200M rows now"},"timestamp":"2026-05-19T07:30:00.000Z","cwd":"$HOME/Projects/data-pipeline"}
{"type":"assistant","message":{"role":"assistant","content":"The bottleneck is the sequential chunk fetch. Fix: add a covering index on (id, created_at, user_id, event_type) and parallelize the extract phase across 8 workers with non-overlapping ID ranges..."},"timestamp":"2026-05-19T07:30:22.000Z"}
EOF

# mobile-app conversations
cat > "$HOME/.claude/projects/$mob_enc/dddddddd-0004-0004-0004-000000000001.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"Implement biometric auth with Face ID / Touch ID using the Keychain"},"timestamp":"2026-05-21T19:00:00.000Z","cwd":"$HOME/Projects/mobile-app"}
{"type":"assistant","message":{"role":"assistant","content":"I'll use expo-local-authentication for the biometric prompt and expo-secure-store to persist the session token in the iOS Keychain with kSecAttrAccessibleWhenUnlockedThisDeviceOnly..."},"timestamp":"2026-05-21T19:00:14.000Z"}
EOF

cat > "$HOME/.claude/projects/$mob_enc/dddddddd-0004-0004-0004-000000000002.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"Wire up Moshi push notifications for needs_input events"},"timestamp":"2026-05-20T16:00:00.000Z","cwd":"$HOME/Projects/mobile-app"}
{"type":"assistant","message":{"role":"assistant","content":"I'll register the APNs token with ccmuxd on connect and handle the needs_input payload in the notification handler. The Moshi SDK maps approval_required to a critical alert category..."},"timestamp":"2026-05-20T16:00:18.000Z"}
EOF

cat > "$HOME/.claude/projects/$mob_enc/dddddddd-0004-0004-0004-000000000003.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"Add offline mode — cache the session list so the app is usable on spotty connections"},"timestamp":"2026-05-18T14:00:00.000Z","cwd":"$HOME/Projects/mobile-app"}
{"type":"assistant","message":{"role":"assistant","content":"I'll use MMKV for fast synchronous storage. On each successful fetch, persist the session list. On network failure, hydrate from the MMKV snapshot and show a stale-data banner..."},"timestamp":"2026-05-18T14:00:12.000Z"}
EOF

# ccmux project conversations — most recent timestamps so they sort to the top
ccmux_enc="${HOME//\//-}-Projects-ccmux"
mkdir -p "$HOME/.claude/projects/$ccmux_enc"

cat > "$HOME/.claude/projects/$ccmux_enc/eeeeeeee-0005-0005-0005-000000000001.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"Add Tailscale peer auto-discovery — scan the tailnet for ccmuxd instances on startup"},"timestamp":"2026-05-22T10:00:00.000Z","cwd":"$HOME/Projects/ccmux"}
{"type":"assistant","message":{"role":"assistant","content":"I'll add a background goroutine that runs tailscale status --json every 30s, probes each online peer's /v1/health endpoint, and merges responders into the host list..."},"timestamp":"2026-05-22T10:00:12.000Z"}
{"type":"user","message":{"role":"user","content":"Cache the peer list so the network screen is instant even while the probe is in-flight"},"timestamp":"2026-05-22T10:45:00.000Z","cwd":"$HOME/Projects/ccmux"}
{"type":"assistant","message":{"role":"assistant","content":"Adding a sync.Map for the peer cache. The UI reads from cache immediately; the probe goroutine writes updates and sends a refreshPeersMsg to the Bubble Tea runtime..."},"timestamp":"2026-05-22T10:45:08.000Z"}
EOF

cat > "$HOME/.claude/projects/$ccmux_enc/eeeeeeee-0005-0005-0005-000000000002.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"Implement the Conversations screen — list every past Claude/Codex/agy thread sorted by recency, resume on Enter"},"timestamp":"2026-05-22T08:15:00.000Z","cwd":"$HOME/Projects/ccmux"}
{"type":"assistant","message":{"role":"assistant","content":"Walking ~/.claude/projects/*/*.jsonl and ~/.codex/sessions/ to build a unified thread index. Each entry shows project name, first user message (truncated), and relative timestamp. Enter launches the agent with --resume <id>..."},"timestamp":"2026-05-22T08:15:20.000Z"}
EOF

cat > "$HOME/.claude/projects/$ccmux_enc/eeeeeeee-0005-0005-0005-000000000003.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"Fix the VHS render isolation — the cleanup was killing my real tmux sessions"},"timestamp":"2026-05-22T07:00:00.000Z","cwd":"$HOME/Projects/ccmux"}
{"type":"assistant","message":{"role":"assistant","content":"The issue is that cleanup() runs bare tmux kill-server which hits the default socket shared with your live sessions. Fix: named socket (-S \$TMUX_SOCK) for all render activity + unset TMUX so subprocesses don't inherit the parent socket..."},"timestamp":"2026-05-22T07:00:18.000Z"}
EOF

cat > "$HOME/.claude/projects/$ccmux_enc/eeeeeeee-0005-0005-0005-000000000004.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"Add the 5-hour token quota bar to the dashboard — read from ~/.claude/projects/*.jsonl"},"timestamp":"2026-05-21T20:00:00.000Z","cwd":"$HOME/Projects/ccmux"}
{"type":"assistant","message":{"role":"assistant","content":"Walking ~/.claude/projects/*/*.jsonl, summing input_tokens + output_tokens for entries in the last 5 hours. The bar is a Lipgloss progress component that turns amber at 80% and red at 95%..."},"timestamp":"2026-05-21T20:00:25.000Z"}
EOF

# --- Git repo for update-check (C11) ---------------------------------
# Only seeds the repo; CCMUX_UPDATE_DEMO controls whether auto_check
# is on in config (set above). The repo must have .git AND Makefile
# so looksLikeCcmuxRepo returns true.
bare_remote="$root/ccmux-remote.git"
git init --bare -q "$bare_remote"

export GIT_AUTHOR_NAME="Demo"
export GIT_AUTHOR_EMAIL="demo@example.com"
export GIT_COMMITTER_NAME="Demo"
export GIT_COMMITTER_EMAIL="demo@example.com"

git -C "$HOME/Projects/ccmux" init -q
echo "# ccmux (demo)" > "$HOME/Projects/ccmux/README.md"
echo "build:" > "$HOME/Projects/ccmux/Makefile"
git -C "$HOME/Projects/ccmux" add README.md Makefile
git -C "$HOME/Projects/ccmux" commit -q -m "initial"
git -C "$HOME/Projects/ccmux" remote add origin "$bare_remote"
local_branch=$(git -C "$HOME/Projects/ccmux" rev-parse --abbrev-ref HEAD)
git -C "$HOME/Projects/ccmux" push -q -u origin "$local_branch"
# Push one extra commit to the remote, then reset local — making
# local HEAD 1 commit behind origin so the update banner fires.
git -C "$HOME/Projects/ccmux" commit -q --allow-empty -m "feat: new dashboard columns"
git -C "$HOME/Projects/ccmux" push -q origin "$local_branch"
git -C "$HOME/Projects/ccmux" reset -q --hard HEAD~1

unset GIT_AUTHOR_NAME GIT_AUTHOR_EMAIL GIT_COMMITTER_NAME GIT_COMMITTER_EMAIL

# project.Discover sorts projects by mtime (newest first); inspect()
# takes the newer of the dir or CLAUDE.md mtime. Stamp auth-service's
# CLAUDE.md far in the future so it stays at projects[0] even after the
# agents started below write into the other project dirs and bump their
# mtimes to "now". projects[0] is the project the Notes screen opens on,
# and auth-service is what the CUJ-6 search demo queries. The Modified
# time is never surfaced in the UI, so a future stamp is invisible.
touch -t 205001010000 "$HOME/Projects/auth-service/CLAUDE.md"

# --- Daemon + running sessions ---------------------------------------
"$root/bin/ccmuxd" >/dev/null 2>&1 &
# Session names are chosen so the list sorts: claude → codex → agy.
# Alphabetical: auth-service(a) ccmux(c) mobile-app(m) react-dash(r) spark-etl(s)
T new-session -d -s c-auth-service -c "$HOME/Projects/auth-service"  claude
T new-session -d -s c-ccmux        -c "$HOME/Projects/ccmux"         claude
T new-session -d -s c-mobile-app   -c "$HOME/Projects/mobile-app"    claude
T new-session -d -s c-react-dash   -c "$HOME/Projects/web-dashboard" codex
T new-session -d -s c-spark-etl    -c "$HOME/Projects/data-pipeline" agy
# Give agents time to show their startup / workspace-trust prompts, then
# accept them so recording begins with agents showing their main TUI.
sleep 3
T send-keys -t c-auth-service Enter   # claude: accept workspace trust
T send-keys -t c-ccmux        Enter   # claude: accept workspace trust
T send-keys -t c-mobile-app   Enter   # claude: accept workspace trust
T send-keys -t c-react-dash   Enter   # codex: accept workspace trust
T send-keys -t c-spark-etl    Enter   # agy: accept workspace trust
# Wait for agents to fully initialize before the daemon's first poll.
sleep 4

# --- Record ----------------------------------------------------------
mkdir -p "$repo/docs/vhs/out"
cd "$repo"
vhs "$tape"
echo "render: wrote $(grep -m1 '^Output' "$tape" | awk '{print $2}')"
