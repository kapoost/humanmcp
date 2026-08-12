# Sabotage-Verify Log — mysłoodsiewnia offline storyboards

Per ADR 0001 "Sabotage-verification protocol": offline storyboards must FAIL
when the `mysloodsiewniaGate()` check is removed from the tool. Automated CI
doesn't run this — a sabotage commit in main would be its own regression.
Instead, a git worktree is used: sabotage the copy, run the test, discard.

Log below records each verification pass. Add a new row after every
substantial change to the bridge tool bodies (`internal/mcp/mysloodsiewnia_tools.go`,
`internal/mcp/v2/mysloodsiewnia.go`).

Wave 3 sabotage-verify (2026-08-12) uses in-place edit + revert instead of
worktree because the impl was uncommitted at test time. Same guarantee: each
sabotage introduced one identifiable regression, ran the specific storyboard,
observed FAIL, reverted before continuing. Commit ref `3fd6279+wip` means
"HEAD 3fd6279 + wave 3 uncommitted work in tree".

## History

| date       | commit at test | tool sabotaged           | expected fail | actual fail | notes                                                                 |
|------------|----------------|--------------------------|---------------|-------------|-----------------------------------------------------------------------|
| 2026-08-06 | `5a23287`      | `mysloodsiewnia_search`  | yes           | yes         | Gate removed; storyboard flipped from `status:offline` to `internal_error: bridge queue not configured`. Asercja `body_contains "status: offline"` upadła. |
| 2026-08-12 | `3fd6279+wip`  | `mysloodsiewnia_search`  | yes           | yes         | Wave 3: usunięty `friendRateLimit()` w `toolMysloodsiewniaSearch`. Storyboard `wave3_rate_limit_per_token` — 4-te wywołanie slug_b zwróciło `status:offline` zamiast `status:rate_limited`. Asercje `body_contains rate_limited` + `retry_after` upadły. `[narada:nar-67cdd80179c2]` |
| 2026-08-12 | `3fd6279+wip`  | `mysloodsiewnia_search`  | yes           | yes         | Wave 3: usunięty `mysloodsiewniaGate()` w `toolMysloodsiewniaSearch`. Storyboard `wave3_scoped_offline_same_gate` — owner baseline + friend slug_a zwróciły `internal_error: bridge queue not configured` zamiast `status:offline`. Potwierdzenie że gate jest wspólnym punktem dla owner + friend. `[narada:nar-67cdd80179c2]` |
| 2026-08-12 | `3fd6279+wip`  | `mysloodsiewnia_list`    | yes           | yes         | Wave 3: usunięty `friendScope()` w `toolMysloodsiewniaList`. Storyboard `wave3_scoped_out_of_scope_403` — slug_a z `doc_type=pdf` (out of scope) zwrócił `status:offline` zamiast `status:out_of_scope` + `allowed:[literatura,note]`. Asymetria Z2 przypięta. `[narada:nar-67cdd80179c2]` |
| 2026-08-12 | `3fd6279+wip`  | `mysloodsiewnia_search`  | yes           | yes         | Wave 3: usunięty `if !ok { unauthorized }` — auth check obszedł się cicho w `toolMysloodsiewniaSearch`. Storyboard `wave3_unrecognized_token_unauthorized` — wszystkie 4 asercje (anonymous / unknown slug / revoked / malformed bearer) zwróciły `status:offline` zamiast `Unauthorized`. W4 immediate hard cut przypięte. `[narada:nar-67cdd80179c2]` |

## Procedure (copy-paste)

```bash
cd /Users/kapoost/humanmcp-server
git worktree add /tmp/sabotage-worktree HEAD

# Manually edit /tmp/sabotage-worktree/internal/mcp/mysloodsiewnia_tools.go
# — remove the `if text, stop := h.mysloodsiewniaGate(); stop { … return }`
# block from one of the tool handlers.

cd /tmp/sabotage-worktree
go test ./internal/storyboard/... -run 'TestStoryboards/mysloodsiewnia'
# Expect: --- FAIL --- (storyboard catches the removed gate)

# Cleanup — always:
cd /Users/kapoost/humanmcp-server
git worktree remove /tmp/sabotage-worktree --force
```

## When to re-run

- After changing any of the three offline storyboards
  (`storyboards/mysloodsiewnia/*_offline_*.yaml` +
  `list_offline_owner_and_defaults.yaml`).
- After refactoring `mysloodsiewniaGate` or `renderBridgeStatus`.
- Before merging a wave-2 write PR (write path introduces new gate paths).
- As part of the quarterly rotate-bridge-token task (harmless spot-check).
