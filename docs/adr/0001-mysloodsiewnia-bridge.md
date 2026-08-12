# ADR 0001 — humanMCP ↔ mysłoodsiewnia bridge (wave 0 + wave 1)

- Status: Accepted
- Date: 2026-08-05
- Narada: `nar-4ac3506ab3bc` — 5 voices (ghost, hodor, mira-chen, maruda, axel-brandt)
- Commit tag: `[narada:nar-4ac3506ab3bc]`

## Addenda

- **2026-08-06** — Wave 1.5: added `mysloodsiewnia_list` (owner-only, no
  required args) to complement `_search` for browsing by `doc_type` /
  paginating. FTS rejects empty query, so listing "all notes" was
  impossible with wave 1 tools alone. Vault worker uses direct lean SQL
  (`SELECT ... LIMIT/OFFSET`) — the stock `db.list_documents()` runs a
  per-row `chunk_count` subquery over the 9k-doc corpus and blows past
  the 20s wait timeout.
- **2026-08-06** — First sabotage-verify run recorded in
  `storyboards/mysloodsiewnia/SABOTAGE_LOG.md`. Storyboard `search_offline`
  flipped PASS→FAIL when gate removed — protocol works.
- **2026-08-12** — Wave 3 design (sharing / friend tokens) accepted after
  narada `nar-67cdd80179c2` (ghost, hodor, yuki-tanaka, mira-chen, maruda).
  Five-way consensus on all five open questions — see the "Wave 3 — sharing
  / friend tokens" section below for the chosen design. Implementation still
  gated on a separate brief (`prompts/wave3-sharing.md`, TBD) and a green
  storyboard tree before any `flyctl secrets set FRIEND_TOKEN_*` lands.

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

### Wave 3 — sharing / friend tokens (design accepted 2026-08-12)

Narada `nar-67cdd80179c2` (ghost, hodor, yuki-tanaka, mira-chen, maruda)
returned 5/5 consensus on every open question. This section is now the
chosen design, not a deferral. Commit tag: `[narada:nar-67cdd80179c2]`.

Today the bridge is single-tenant: one `EditToken` gates every
`mysloodsiewnia_*` tool, and any agent presenting that token can query the
whole corpus. Wave 3 introduces per-recipient friend tokens with narrow,
statically-auditable scopes — driven by the known horcrux scenario
(`project_horcrux` + `project_hodor_tomorrow` memories).

Already scaffolded elsewhere: `content.access` field on documents
(private/public/friends), `tokens.json` on the vault side for named friend
tokens. Wave 3 is the wire-up: extend `IsOwnerRequestByHeaders` to
`IsAuthorizedRequestByHeaders(scopes)`, propagate scopes to the bridge
queue, filter at SQL time in the vault.

#### W1 — scope grammar: static enum, not an expression language

Scope is `doc_type IN (<enum values>)` where enum values are drawn from a
fixed set (`literatura`, `note`, `pdf`, ...). JSONata / CEL / any
expression evaluator in the authorization path was rejected 5/5:
expressive grammar in a security boundary is a CVE waiting for a date.
Rotating tokens for new use cases is two commits; patching a scope
injection is a public incident on a public repo.

#### W2 — audit: per-response, transactional record

Extend `stats.ndjson` (or the equivalent audit sink) to record, per call:
`token_id`, `tool`, `timestamp`, and — critically — the list of
`doc_id`s that were returned to the caller. Hash the content, not the
content itself: forensics needs "what came out," size cost is fine.

Request-only logging is security theatre: at 3am when kapoost suspects
a leak, "Alice asked about literatura" without "Alice received these 23
slugs" is no evidence. Mira called out the failure mode explicitly:
never keep request-side metadata on Fly and response-side detail on the
vault as two logs to correlate later — write one transactional entry
per call, on the vault side, at commit time.

#### W3 — `access:private` is completely invisible to scoped tokens

Not "present but unreadable." **Zero rows** in results, zero increment
in count, zero mention in listings. A scoped token must not be able to
infer that a private document *exists*. Metadata leak over 9k docs and
50 req/hr is enough to reconstruct kapoost's private graph — Ghost,
Hodor, Yuki, and Maruda were unanimous on this being the harder-to-code
but correct choice.

Concretely: vault SQL layer adds `AND access != 'private'` for scoped
callers *before* any COUNT / LIMIT / listing operation, not after.

#### W4 — revocation: immediate hard cut, no TTL grace window

`flyctl secrets unset FRIEND_TOKEN_<slug>` triggers Fly restart; vault
must invalidate the same token within the same window. **No 60s TTL
cache** on the vault side that keeps a revoked token alive after Fly
already refuses it. Yuki noted a 60s cache is acceptable *only* with an
active alert on "token X used <cache_ttl>s after revoke" — kapoost has
no such alert today, so 60s cache = 60s of hoping.

Deployment atomicity (Mira, load-bearing): scope grammar and token
lookup live in three places — Fly (token → scopes), bridge queue
(propagate), vault (SQL filter + audit). Vault must not poll
`tokens.json`; it must reload on either a signal from Fly (webhook on
secret change) or SIGHUP from the vault operator. Poll model here is
the wrong choice specifically because "revoked in Fly, honored in
vault" is *the* case at incident time, not an edge case.

Additionally: encode a hard `expires_at` inside the friend token
payload itself, not only in the lookup table. Belt and suspenders.

#### W5 — read-only, always. Write is not in wave 3.

`leave_comment` and any other scoped-write primitive is a separate ADR.
Rationale (Maruda + Ghost + Mira agreed): scoped write is roughly 3× the
threat surface of scoped read, and the vault's own write path (wave 2)
does not exist yet in production. Implementing scoped write on top of an
unimplemented base write is inverted order. Sequence: wave 3 read → 30
days observation → separate decision on scoped write.

#### Prerequisite, not option: rate limit per token

Maruda flagged this as a hard prerequisite: friend token without a
per-token rate cap is a bulk-dump tool wearing a friendly hat.
Reasonable starting cap: 50 req/hr per friend token, unlimited for
owner `EditToken`. Enforced on the vault side (last line of defence),
mirrored on Fly (fast path). Both cap counters are per-token, not
shared.

#### Threat model / repo hygiene note (Hodor)

Repo has been public since 2026-08-05
(memory `project_repo_public.md`). Before any wave 3 commit:

- `git log --all -S "friend"` and `git log --all -S "FRIEND_TOKEN"` on
  both this repo and the vault repo.
- Friend slug names, per-friend scope patterns, and `tokens.json`
  example fixtures with realistic-looking scopes must **not** land in
  git — even for tests. Use `slug_A`, `slug_B` in fixtures.
- Every real `FRIEND_TOKEN_*` lives in `flyctl secrets` only; the
  vault-side counterpart lives in the same chmod 600 file family as
  `bridge.env`, never in git.

#### Open follow-ups (not blockers)

- Write the wave 3 brief at `prompts/wave3-sharing.md` (mirror
  `prompts/mysloodsiewnia-bridge.md` structure) — self-contained for a
  fresh Plan session.
- Contrarian sanity pass on this design before first `FRIEND_TOKEN_*`
  ships. Narada was 5/5 in one direction; that warrants a devil's
  advocate lap, even if the conclusion doesn't change.
- Storyboard tree `storyboards/mysloodsiewnia/wave3_*.yaml`: at minimum
  scoped read (positive), out-of-scope read (negative, must 403),
  `access:private` invisibility (must return 0 rows, not "denied"),
  revoked token (must fail before *and* after Fly restart lag).

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

- Narada logs (fetch full voices via `fetch_narada_result`):
  - `nar-4ac3506ab3bc` — wave 1 design (2026-08-05, 5 voices)
  - `nar-67cdd80179c2` — wave 3 sharing / friend tokens
    (2026-08-12, 5 voices: ghost, hodor, yuki-tanaka, mira-chen, maruda)
- Storyboards: `storyboards/mysloodsiewnia/*.yaml`.
- Client: `services/humanmcp_bridge.py` in the mysłoodsiewnia repo.
- Wire pattern audit (6 places for a new MCP tool): see
  `internal/web/phantom_tools_test.go`.
