## Summary

<!-- 1-3 sentences. Why this change, not just what. -->

## Storyboard

<!--
Every behavior change must be tied to a storyboard.

For a new feature: paste the path of the storyboard you added.
  e.g.  storyboards/web/listings_edit_returns_form.yaml

For a bug fix: paste the path of the storyboard that captures the
regression. If one didn't exist, this PR must add it.
  e.g.  storyboards/stats/googlebot_classified_as_agent.yaml

For a refactor with no behavior change: explicitly say "no new
storyboard — existing scenarios remain green" and confirm
`go test ./internal/storyboard/...` passes.

See storyboards/SUBMIT.md for the discipline.
-->

- [ ] New / updated storyboard: ___
- [ ] `go test ./internal/storyboard/...` passes locally
- [ ] `go test ./...` passes locally

## Test plan

<!--
What did you actually run to convince yourself this works?
Storyboards are the contract; this section captures the one-time
exploration that gave you confidence the contract is the right shape.
-->

-

## Closes failure modes

<!--
Mirror the `closes_failure_modes:` block of the affected storyboard.
Helps reviewers see what silent breakage this prevents — not just
what the happy path now does.
-->

-

🤖 Generated with [Claude Code](https://claude.com/claude-code)
