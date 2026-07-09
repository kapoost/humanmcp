#!/bin/sh
# humanmcp-narada-tracker
#
# Globalny post-commit hook (instalowany do ~/.config/git/hooks/ jak siostrzany
# pre-commit) — appenduje strukturalne zdarzenia do
# ~/.humanmcp/narada-events.jsonl. Dwa typy zdarzeń:
#
#   narada_impl: commit implementujący rekomendację narady
#     (message zawiera "[narada:<id>]" albo "[narada:<id>:<persona-slugs>]")
#   rollback:    commit revertujący albo poprawiający wcześniejszą zmianę
#     (message zaczyna się od Revert/rollback/undo/fix/poprawka, lub jest git-revertem)
#
# /dobranoc czyta ten log, żeby znaleźć pary (narada_impl, rollback) gdzie
# rollback wpadł w oknie 72h po implementacji w tym samym repo, i wtedy
# odpala record_persona_reflection dla każdej persony która głosowała na
# tę naradę.
#
# Hook jest celowo szybki + offline — zero MCP calls, zero sieci. Korelacja
# i refleksja lądują później, w skillu /dobranoc.
#
# Per-repo escape hatch (spójna z siostrzanym pre-commit):
#   git config humanmcp.hookOff true

set -eu

# Per-repo opt-out — spójne z global pre-commit hookiem.
if [ "$(git config humanmcp.hookOff 2>/dev/null)" = "true" ]; then
  exit 0
fi

LOG_DIR="${HUMANMCP_STATE_DIR:-$HOME/.humanmcp}"
LOG="$LOG_DIR/narada-events.jsonl"
mkdir -p "$LOG_DIR"

SHA=$(git rev-parse HEAD)
REPO=$(git rev-parse --show-toplevel)
MSG=$(git log -1 --pretty=%B)
AT=$(git log -1 --pretty=%cI)
SUBJECT=$(printf '%s' "$MSG" | head -1 | sed 's/"/\\"/g' | tr -d '\r')

# Escape JSON strings the busybox-safe way — repo path may contain spaces.
esc() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }

# Narada implementation tag: [narada:<id>] or [narada:<id>:<persona-list>]
TAG=$(printf '%s' "$MSG" | grep -oE '\[narada:[a-z0-9-]+(:[a-z0-9,\-]+)?\]' | head -1 || true)
if [ -n "$TAG" ]; then
  INNER=$(printf '%s' "$TAG" | sed 's/\[narada://; s/\]$//')
  NARADA_ID=$(printf '%s' "$INNER" | cut -d: -f1)
  PERSONAS=$(printf '%s' "$INNER" | cut -sd: -f2 || true)
  printf '{"type":"narada_impl","sha":"%s","repo":"%s","at":"%s","narada_id":"%s","personas":"%s","subject":"%s"}\n' \
    "$SHA" "$(esc "$REPO")" "$AT" "$NARADA_ID" "$(esc "$PERSONAS")" "$(esc "$SUBJECT")" >> "$LOG"
fi

# Rollback / fix signals — dopasowujemy TYLKO subject (pierwszą linijkę),
# nie cały body. Wcześniejsza wersja łapała fałszywy alarm gdy słowa
# "revert" / "rollback" / "poprawka" pojawiły się w opisie zmiany.
# Wzorzec akceptuje konwencjonalny prefiks (feat/fix/chore) przed
# słowem-kluczem — "fix(narada): ..." powinno wpaść jako rollback,
# ale "feat(auth): describe revert flow" — nie.
ROLLBACK_PATTERN='^(revert|rollback|undo|poprawka|wycofuj|przywróć)( |:|$)|^(feat|fix|chore|refactor|docs)(\([^)]*\))?: (revert|rollback|undo|poprawka|wycofuj|przywróć)( |$)|^fix( |:|\()|^this reverts commit'
FIRST_LINE=$(printf '%s' "$MSG" | head -1)
if printf '%s' "$FIRST_LINE" | grep -qiE "$ROLLBACK_PATTERN"; then
  # If git revert, the message typically contains "This reverts commit <sha>."
  REVERTED=$(printf '%s' "$MSG" | grep -oE 'This reverts commit [a-f0-9]+' | awk '{print $NF}' | head -1 || true)
  # Fallback: fix-style commits usually target the previous commit
  if [ -z "$REVERTED" ]; then
    REVERTED=$(git rev-parse HEAD~1 2>/dev/null || echo "")
  fi
  printf '{"type":"rollback","sha":"%s","repo":"%s","at":"%s","reverted":"%s","subject":"%s"}\n' \
    "$SHA" "$(esc "$REPO")" "$AT" "$REVERTED" "$(esc "$SUBJECT")" >> "$LOG"
fi

exit 0
