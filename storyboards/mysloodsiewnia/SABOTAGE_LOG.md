# Sabotage-Verify Log — mysłoodsiewnia offline storyboards

Per ADR 0001 "Sabotage-verification protocol": offline storyboards must FAIL
when the `mysloodsiewniaGate()` check is removed from the tool. Automated CI
doesn't run this — a sabotage commit in main would be its own regression.
Instead, a git worktree is used: sabotage the copy, run the test, discard.

Log below records each verification pass. Add a new row after every
substantial change to the bridge tool bodies (`internal/mcp/mysloodsiewnia_tools.go`,
`internal/mcp/v2/mysloodsiewnia.go`).

## History

| date       | commit at test | tool sabotaged           | expected fail | actual fail | notes                                                                 |
|------------|----------------|--------------------------|---------------|-------------|-----------------------------------------------------------------------|
| 2026-08-06 | `5a23287`      | `mysloodsiewnia_search`  | yes           | yes         | Gate removed; storyboard flipped from `status:offline` to `internal_error: bridge queue not configured`. Asercja `body_contains "status: offline"` upadła. |

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
