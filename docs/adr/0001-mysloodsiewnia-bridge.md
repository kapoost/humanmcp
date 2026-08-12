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
  gated on the brief (`prompts/wave3-sharing.md`) and a green storyboard
  tree before any `flyctl secrets set FRIEND_TOKEN_*` lands.
- **2026-08-12** — Contrarian pass on wave 3 design (post 5/5 narada, per
  "team suspiciously agreeable" heuristic). Six issues raised, four
  actioned inline in the Wave 3 section: (Z1) whitelist-only for now,
  wildcard-minus-exclusion documented as deferred; (Z2) `access:private`
  zero-rows vs out-of-scope 403 asymmetry pinned as intentional so no
  one "fixes" it; (Z3) invariant "vault has no direct public endpoint"
  pinned as load-bearing for revocation soundness; (Z4) 50 req/hr moved
  from hardcoded default to per-token config; plus a new "Horcrux vs
  sharing token" subsection (Z6) — same mechanism, different operational
  discipline, needs a separate decision before first horcrux token.
- **2026-08-12** — Wave 3 Fly-side wired up. Landed as a single commit
  under `[narada:nar-67cdd80179c2]`:
  - `internal/config/config.go` — `FriendTokenSpec` type + `FriendTokens`
    field. Prod loads from `FRIEND_TOKENS_JSON` env (base64-encoded JSON
    blob). Empty ⇒ owner-only (wave 1 behavior).
  - `internal/mcp/friend_auth.go` — `AuthorizeRequestByHeaders` returns
    `(tokenID, scopes, ok)`. Unknown / expired / malformed all return
    `("", nil, false)` — indistinguishable from anonymous by design
    (W4 + Z3). `CheckFriendTokenRateLimit` — per-tokenID sliding 1h
    window, owner bypasses, returns `retry_after` seconds on deny.
  - `internal/mcp/mysloodsiewnia_tools.go` + `internal/mcp/v2/mysloodsiewnia.go`
    — new precedence for all four tools: (1) auth (2) validate args
    (3) rate-limit (4) scope (5) liveness gate (6) enqueueScoped.
    Owner path unchanged (backward compat with wave 1 storyboards).
  - `internal/mysloodsiewnia/queue.go` + `bridge.go` — `Op.TokenID` +
    `Op.Scopes` (omitempty) propagate to vault via `EnqueueScoped` +
    the `/pending-ops` wire form. Vault worker uses these for SQL
    filter + audit write.
  - `storyboards/mysloodsiewnia/wave3_*.yaml` — four new storyboards
    (moved from `prompts/wave3-storyboards/`). All 17 assertions green.
    Each sabotage-verified per SABOTAGE_LOG.md (rate-limit, gate,
    scope, auth — each removal caught by the respective storyboard).
  - Bootstrap body (v1 + v2) mentions the friend-token model and every
    response-envelope shape (`out_of_scope`, `rate_limited`, `offline`).
  - Vault-side + `flyctl secrets set FRIEND_TOKENS_JSON` land separately
    (see `~/Documents/humanmcp-incident-playbook.txt` new WAVE 3 section
    for rotation / revocation / audit runbook). Fly-side is safe to
    deploy first — with no `FRIEND_TOKENS_JSON` set, behavior is
    identical to wave 1.

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

**Contrarian addendum (Z1)**: `doc_type` is owned by the vault, and
new document kinds ("żegluga-log," future genres) will land as
kapoost's writing evolves. A pure positive-list scope forces token
rotation on every new type — for example, a "friend gets all public
poetry" token needs a bump every time a new poetic genre appears.
Wave 3 implementation stays positive-list only (`scopes:
["literatura","note"]`), but a **wildcard-minus-exclusion** variant is
explicitly deferred as a follow-up, not rejected: `scopes: ["*"],
exclude: ["private"]` (interpreted as *all `doc_type`s* subject to the
`access != 'private'` filter of W3). Static, statically-auditable, no
expression language. Revisit if rotation becomes operational pain
within the first 30 days after wave 3 goes live. Decision-owner:
kapoost.

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

**Pinned invariant (Z2 — do not "fix")**: this response semantics is
**deliberately asymmetric** with out-of-scope `doc_type` requests. When
a scoped token requests a `doc_type` outside its scope, the tool
returns HTTP 403 with an informative body (`{"status":"out_of_scope",
"allowed":[...]}`) — the friend is *taught* what they can access. When
a scoped token would otherwise match an `access:private` document, the
tool returns zero rows with no signal that any private document
exists — the friend is *never taught* that privacy exists on any
specific artefact. Both behaviours are correct; the asymmetry is the
point. A future implementer who reads only the code and spots "403
here, silent skip there" will be tempted to unify — do not unify. The
scope filter is scope education; the privacy filter is privacy by
erasure. Different threat models, different responses.

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

**Pinned invariant (Z3 — load-bearing)**: revocation soundness depends
on the invariant that **the vault has no direct public endpoint**. Fly
is the sole ingress; if kapoost `secrets unset FRIEND_TOKEN_<slug>`
while the vault is offline (boat, power cut, laptop asleep), the vault
still has the token loaded in memory — but no request can reach it
because Fly refuses at the boundary. This is safe *only* under the
current architecture. If the vault ever gains a direct public endpoint
(ngrok tunnel, Tailscale mesh exposed to the internet, wave 4-plus
plans), the revocation logic must be reviewed *before* that endpoint
is exposed. Do not treat this invariant as an implementation detail.

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
Enforced on the vault side (last line of defence), mirrored on Fly
(fast path). Both cap counters are per-token, not shared.

**Contrarian addendum (Z4)**: no hardcoded universal default in the
code. The narada didn't justify any specific number, and "50 req/hr"
is either too generous for bulk-dump protection or too tight for a
friend running a legitimate script — depends entirely on the friend.
The `tokens.json` format already carries `rate_limit_per_hour` per
token; that is the source of truth. Config missing → default of 30
req/hr (deliberately conservative — easier to loosen per-token than
tighten). Owner `EditToken` bypasses the cap entirely. If per-token
tuning becomes annoying, revisit — but not before wave 3 has 30 days
of live observation.

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

#### Horcrux vs sharing token (Z6 — same mechanism, different discipline)

Contrarian pass flagged that the horcrux use case
(`project_horcrux` in kapoost's memory — vault access for a trusted
recipient in case something happens to kapoost) has operational
requirements that a standard sharing token doesn't satisfy. A friend
token with `expires_at: 2026-11-30` is unusable as horcrux: renewal
requires kapoost's active involvement, which is precisely the state
that horcrux is designed to survive.

Wave 3 does not implement horcrux tokens. The mechanism is the same
(same `tokens.json` shape, same scope grammar, same audit trail), but
the operational discipline differs sharply:

- **Sharing token**: `expires_at` 30–90 days out, rotate on schedule,
  narrow scope tied to a specific reason (Alice reading Q3 poetry).
- **Horcrux token**: long or open-ended TTL, renewal procedure that
  does not require kapoost's active input (dead-man switch on a
  calendar entry? auto-renew on heartbeat absence? separate ADR).

Before any token is created with `expires_at > 1 year` or without
`expires_at`, a separate decision is required — likely another narada,
because the threat model shifts again (recipient may not know they
have the token until the trigger event). Do not conflate horcrux with
sharing on the operational side, even though the code paths overlap
100%.

#### Open follow-ups (not blockers)

- Storyboard tree `storyboards/mysloodsiewnia/wave3_*.yaml`: at minimum
  scoped read (positive), out-of-scope read (negative, must 403),
  `access:private` invisibility (must return 0 rows, not "denied"),
  revoked token (must fail before *and* after Fly restart lag).
- W1 whitelist vs wildcard-minus-exclusion — revisit at 30 days if
  rotation is operationally painful.
- Horcrux ADR — do not create any long-TTL / open-TTL token until this
  lands.

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
