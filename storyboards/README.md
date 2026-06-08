# Storyboards

Executable scenario specs for humanmcp. Each storyboard is a YAML file
that captures one concrete behavioral contract — what the system must
do, and what failure mode is closed by enforcing it.

Inspired by [adcp/storyboards](https://github.com/adcontextprotocol/adcp)
where scenarios are first-class artifacts, additive over time, and
runnable in CI.

## Why this exists

On 2026-05-20 most of the working UI (Mission Control, artworks,
listings, keyboard shortcuts, theme toggle, PL/EN i18n) lived only
inside a deployed Fly binary — never committed. A `fly deploy` from
a stale local `master` reverted production to April. Rollback worked
because the image registry still had the layer, but the lesson stuck:

> Deploy is not documentation. The codebase is. The storyboards are
> the contract between yesterday and tomorrow.

When a feature has a storyboard:

1. Anyone reading the repo learns what the feature **must** do.
2. CI fails loudly if a refactor breaks the contract.
3. Future-you can't silently regress what past-you built.

## Structure

```
storyboards/
├── web/      # HTTP routes & owner UI flows
├── mcp/      # MCP tool calls & session bootstrap
├── signing/  # Ed25519, OpenTimestamps, content integrity
└── stats/    # Caller classification, windowed counters
```

One YAML per scenario. File name matches `id` (with `/` → folder).

## Anatomy

```yaml
id: web/listings_edit_returns_form
version: "1.0.0"
title: "Owner editing a listing sees the form, not 404"
category: web_owner_ui
summary: "One-sentence what-and-why."
narrative: |
  Multi-paragraph human prose describing the behavior in detail.
  This is read by people, not by the runner. Polish welcome.
kind: http      # http | unit | store
# kind-specific fields follow
closes_failure_modes:
  - "Free-text description of what silent breakage this prevents"
known_regressions_caught:
  - "YYYY-MM-DD — short note on the historical incident"
```

### `kind: http`

Spins up an in-memory server via `httptest`, replays scripted requests,
asserts on status/headers/body.

```yaml
kind: http
setup:
  listings:
    - {slug: s2000-parts, title: "S2000 parts", body: "spares"}
assertions:
  - name: "owner sees the edit form"
    request:
      method: GET
      path: /listings/edit/s2000-parts
      owner: true
    expect:
      status: 200
      body_contains: ["Edit Listing", "S2000 parts"]
```

### `kind: unit`

Calls a known function (by symbolic name dispatched in the runner)
with input/expected pairs.

```yaml
kind: unit
function: content.CallerFromUA
cases:
  - input: "Mozilla/5.0 (compatible; Googlebot/2.1; ...)"
    expect: "agent"
```

### `kind: store`

Save → reload → load round-trip on the content store. Used for
persistence guarantees (signature, OTSProof, frontmatter fields).

```yaml
kind: store
operations:
  - save: {slug: x, title: y, body: z, ots_proof: "AE9wZW5..."}
  - reload: true
assertions:
  - field: OTSProof
    not_empty: true
  - field: OTSProof
    starts_with: "AE9wZW5"
```

## Running

```sh
go test ./internal/storyboard/...
# or
go test ./... -run TestStoryboards
```

Each YAML file becomes a `t.Run("<id>", ...)` subtest. Output mirrors
the file layout, so `--run TestStoryboards/web/listings` filters
narrowly.

## Adding a storyboard

1. Pick category folder (web / mcp / signing / stats).
2. Filename: `<scenario_name>.yaml` matching the last path of `id`.
3. Bump `version` if you change semantics of an existing scenario.
   Adding new assertions to an existing scenario is a minor bump
   (`1.0.0` → `1.1.0`). Changing what counts as success is major.
4. Run `go test ./internal/storyboard/...` — must pass before commit.
5. Reference the storyboard in your PR description (see SUBMIT.md).

## Discipline

- **Additive only.** Once a storyboard lands, it stays. Deletions only
  with explicit justification — the failure mode it closes might still
  exist.
- **One scenario, one contract.** Don't bundle "edit + delete + image
  upload" into one file. Three small storyboards beat one fragile one.
- **Polish narrative is fine.** That's the intent layer. The
  assertions are language-neutral.
- **Storyboards before fixes.** Bug found? Write the failing storyboard
  first, then the fix. The storyboard proves the fix landed and stays
  to catch the regression.

## Anti-patterns

- Storyboards that just re-describe `handler.go` line-by-line.
- Assertions that lock implementation details (`body contains exact
  HTML snippet`) instead of behavior (`body contains the title`).
- A scenario that passes when broken because the assertions are weak.
- Adding a storyboard "for the next refactor" without a real failure
  mode in mind. Speculative scenarios bitrot.

## Provenance

This pattern is borrowed from
[adcp](https://github.com/adcontextprotocol/adcp)'s
`static/compliance/source/specialisms/*/scenarios/`, where every PR
extends a `requires_scenarios[]` and the schema is YAML-first.
