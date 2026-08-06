# ADR 0001 — humanMCP ↔ mysłoodsiewnia bridge (wave 0 + wave 1)

- Status: Accepted
- Date: 2026-08-05
- Narada: `nar-4ac3506ab3bc` — 5 voices (ghost, hodor, mira-chen, maruda, axel-brandt)
- Commit tag: `[narada:nar-4ac3506ab3bc]`

## Context

humanmcp (this repo, Go, public on Fly `kapoost-humanmcp`) and mysłoodsiewnia
(kapoost's local FastAPI vault at `localhost:7331`, SQLite FTS5) have talked
one-way for months: the vault pushes persona markdown files into
`content/personas/*.md` via a local script, then `flyctl deploy` republishes
them. A fresh agent connecting to the public MCP endpoint had no way to reach
into kapoost's private corpus.

Wave 1 opens the other direction: a family of `mysloodsiewnia_*` tools on
humanmcp that read from the vault when it's live, and return a stable
`{status:"offline"}` when it isn't. Wave 2 (not this ADR) will add write.

## Decision

Adopt a **pull model** (vault polls Fly for pending ops, executes locally,
posts back) gated by a **liveness heartbeat store** on the Fly side. Wave 1
is **read-only** (three tools). Auth between vault and Fly is a single
**shared Bearer token** stored as a Fly secret.

## Decisions in detail (D1–D5 from the original brief)

### D1 — reachability: pull model

Vault is behind NAT; Fly cannot initiate. Options considered:

- **Pull (chosen)**: vault polls `GET /mysloodsiewnia/pending-ops` with
  adaptive backoff (2s queue-non-empty / 15s idle / 60s after 5 empty ticks),
  executes locally, `POST /mysloodsiewnia/complete` with result.
- Cloudflare Tunnel / Tailscale: real-time but adds a secret on Fly and an
  operational overhead the workload doesn't justify.
- Reverse SSH / chisel: fragile long-lived socket; supervisor + reconnect
  logic non-trivial.

Trade-off: 2–15s added latency per op. Acceptable for the read-only wave
and simplifies the offline-fallback story dramatically.

### D2+D3 — first wave surface: read-only, three tools

- `mysloodsiewnia_status` — wrapper around vault's `/healthz` + local
  liveness snapshot. Distinguishes `online` / `degraded` (fresh heartbeat
  but FTS unhealthy — Mira's post-restart FTS rebuild window) / `offline`.
- `mysloodsiewnia_search` — wraps `POST /query` (BM25 over `chunks_fts`).
- `mysloodsiewnia_get` — wraps `SELECT * FROM documents WHERE slug=?`.

Write is deferred because Maruda + Axel warned that bundling read + write
in one wave triples the review surface for wave-1 velocity gains that
don't exist. The queue protocol (`op_id` UUID, `accepted`/`picked`/`applied`/
`failed` states) is designed now so wave 2 is a mechanical add.

### D4 — auth between vault and Fly: Bearer only

`Authorization: Bearer $VAULT_BRIDGE_TOKEN` on every bridge endpoint.

- Rejected HMAC + timestamp: over-engineering for one trusted client over
  HTTPS. Threat model is single-writer, not adversarial.
- Rejected mTLS: cert rotation across Fly + macOS Keychain isn't worth it
  for one vault client.
- Bearer token lives in `flyctl secrets` (Fly side) and
  `~/mysloodsiewnia/config/bridge.env` chmod 600 (vault side).

**Rotate quarterly**: `openssl rand -hex 32` → `flyctl secrets set …` →
`security add-generic-password -s mysloodsiewnia-bridge-token -w …` →
rewrite `config/bridge.env`. Track next rotation as an explicit task.

### D5 — safety: delete OFF, write cap, hardcoded audit tag

- **Delete OFF** — permanently, not a scope decision for wave 2 either. The
  bridge never deletes from the vault. Manual only, on the vault host.
- **Write cap** — 100 KB per request, 10 requests per minute. Enforced
  **on the vault side** (not Fly): Fly is stateless across redeploys so
  its counters reset. Vault is the last line of defence.
- **Auto-tag `via:humanmcp-bridge`** — every future write op gets this tag
  hardcoded server-side (in the vault's execution path), not from the
  incoming payload. Maruda's insistence: audit trail forfeits value the
  moment agents can override it.

None of these apply to wave 1 (read-only), but the scaffolding lives in
`services/humanmcp_bridge.py` today so wave 2 doesn't re-argue them.

## Explicit deferrals (with revision triggers)

### P2 skipped: no auth on vault's `POST /query`

Hodor argued for adding `require_token` to the vault's search endpoint
before exposing it via the bridge. Kapoost accepted the risk:

- Vault binds to `0.0.0.0:7331` but on a LAN that's trusted for now.
- Bridge itself is owner-only (Bearer edit token on Fly), so the bridge
  path is already gated.

**Revision triggers** — reopen this decision if any of:
- `VAULT_HOST` stops being local/LAN (Tailscale mesh added, ngrok tunnel,
  cloud host).
- Vault ever ships to a non-single-tenant environment.
- Any friend gets a token for the vault (horcrux distribution).

### Wave 2 write is not in this ADR

Adding write means resolving:
- Idempotency: op_id already exists in the queue protocol, but the vault
  needs an idempotency table so a re-delivered op doesn't double-insert.
- Ghost's "accepted vs applied" distinction: bridge already returns
  `status:"applied"` vs `status:"queued"` — writes must honor this.
- Cap enforcement: sliding-window on vault side, before DB write.

## Non-goals

- **Email**: scoped out 2026-06-16 (CLAUDE.md line 70). No mail storage
  in mysłoodsiewnia. Bridge does not attempt to bring it back.
- **Real-time push**: pull adds seconds of latency; that's fine for reading
  documents. If sub-second becomes a requirement, revisit D1.
- **Cross-vault federation**: no support for multiple vaults. Horcrux
  distribution is a separate skill (project_horcrux memory).

## Consequences

Positive:
- Fresh agents (on any machine, after `bootstrap_session`) can search
  kapoost's local corpus without a manual export step.
- The `VaultOnline: true` hardcode in `web/handler.go:2320` finally goes
  away — every "test offline" storyboard now actually tests offline.
- Queue protocol is generic; wave 2 add-write is a small delta.

Negative / accepted:
- One more secret to rotate (`VAULT_BRIDGE_TOKEN`).
- A daemon runs on the vault machine when kapoost wants the bridge live.
  Fails-safe: `panel` toggles it, and it's a daemon thread that dies with
  the parent process.
- `POST /query` on the vault remains open on LAN. Bounded risk, tracked.

## Rollback plan

- Flip `flyctl secrets unset VAULT_BRIDGE_TOKEN` — bridge endpoints return
  503, tools all report `{status:"offline"}` (since heartbeats stop
  refreshing).
- Or, granularly: comment out the three `registerMysloodsiewnia*(server,
  src)` calls in `internal/mcp/v2/handler.go:New()` and re-deploy. The
  tool count check will fail until `docs/index.html` is also reverted
  (39 → 36) and the phantom_tools_test comment removed.

## Sabotage-verification protocol

The offline storyboards in `storyboards/mysloodsiewnia/*_offline_*.yaml`
must FAIL if the gate check is removed. This is a manual code-review
step — CI does not automate it because leaving a sabotage commit in
main would be its own regression. When reviewing a bridge PR:

1. Comment out the `mysloodsiewniaGate()` call in one tool.
2. Run `go test ./internal/storyboard/... -run
   TestStoryboards/mysloodsiewnia`.
3. Storyboard MUST fail. If it passes, the storyboard is vacuous.
4. Revert the sabotage before merging.

## References

- Narada log: `run_narada` job id `nar-4ac3506ab3bc`; fetch with
  `fetch_narada_result` to see the five voices in full.
- Storyboards: `storyboards/mysloodsiewnia/*.yaml`.
- Client: `services/humanmcp_bridge.py` in the mysłoodsiewnia repo.
- Wire pattern audit (6 places for a new MCP tool): see
  `internal/web/phantom_tools_test.go`.
