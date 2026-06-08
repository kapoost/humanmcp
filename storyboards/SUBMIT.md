# Submitting a storyboard

Checklist for adding or updating a storyboard. Mirrors the discipline
in adcp's `SUBMIT.md`.

## When to write a storyboard

| Trigger | What to do |
|---|---|
| You fixed a bug | Write a storyboard that would have caught it. Filename references the date. |
| You added a feature | Write a storyboard for the happy path + at least one edge case. |
| You changed semantics of an existing route | Bump `version` of the affected storyboard. If old behavior must keep working somewhere, add a parallel scenario. |
| You found a flaky test | Don't write a storyboard. Storyboards are deterministic — fix the test first. |
| You're refactoring with no behavior change | No new storyboard, but `go test ./internal/storyboard/...` must still be green. That's the point. |

## Per-PR checklist

- [ ] Each behavior change has a corresponding storyboard (new or updated).
- [ ] `id` matches the file path (e.g. `web/listings_edit_returns_form`
      → `storyboards/web/listings_edit_returns_form.yaml`).
- [ ] `version` is bumped if semantics changed; new files start at `1.0.0`.
- [ ] `narrative` explains the **why**, not the how. Implementation
      details belong in the code, not the narrative.
- [ ] `closes_failure_modes` is non-empty and named in concrete terms
      ("Owner-only routes accessible to anonymous" beats "Auth bug").
- [ ] `known_regressions_caught` lists the date of any historical
      incident this scenario would have caught.
- [ ] `go test ./internal/storyboard/...` passes.
- [ ] PR description references the storyboard by `id`.

## Categories & their typical assertions

### `web/`
Routes, owner UI, anonymous flows, redirects, 4xx/3xx handling.
- `kind: http` is the default.
- Use `request.owner: true` to inject the test EDIT_TOKEN.
- Assert on `status` + `body_contains` (avoid exact body matches).

### `mcp/`
MCP tool invocations, session bootstrap, persona/skill discovery.
- Usually `kind: unit` against `internal/mcp/handler.go`.
- Bootstrap scenarios should cover diacritic edge cases.

### `signing/`
Ed25519, OpenTimestamps, content integrity, frontmatter persistence.
- Mix of `kind: store` (round-trip) and `kind: unit` (pure crypto).
- Long base64 payloads are fine — quote them as YAML scalars.

### `stats/`
Caller classification, event windowing, aggregation correctness.
- Usually `kind: unit` against `content.CallerFromUA` etc.
- For windowed counters: `kind: store` with deterministic event log.

## Naming

- Lowercase, snake_case, descriptive.
- Verb-noun-condition form preferred:
  - `listings_edit_returns_form` ✓
  - `listings_works` ✗ (vague)
  - `regression_2026_06_08` ✗ (date as name)
- Negative scenarios get an explicit suffix:
  - `bootstrap_rejects_unknown_code`
  - `ots_proof_persists_through_save_load`
  - `googlebot_classified_as_agent`

## After landing

The storyboard is now part of the contract. Other contributors will
break it eventually — that's healthy. When that happens:

1. If the new behavior is correct → bump `version`, update the
   storyboard, document the change in `narrative`.
2. If the new behavior is a regression → revert the code, the
   storyboard did its job.
3. Never just delete a failing storyboard. Diagnose first.
