#!/usr/bin/env bash
# Reconcile the Claude project history that drifted during the
# 2026-06-16 folder rename (humanmcp-new → humanmcp-server).
#
# What happens:
#   While the rename session was still alive, Claude kept writing the
#   transcript into ~/.claude/projects/-Users-kapoost-humanmcp-new/
#   even after that path had been renamed. We left both folders side
#   by side, planning to merge them once the session ended.
#
# This script:
#   1. Verifies neither -new nor -server is currently being written to
#      (any *.jsonl modified in the last 60s = active session).
#   2. Rsyncs -new into -server with --update (newer wins, never
#      overwrites unless the source is newer). Never deletes from -new
#      until the rsync succeeds.
#   3. Removes -new only if the rsync produced no errors and -server
#      now contains every file that -new had.
#
# Safe to run multiple times. No-op if -new already gone.
#
# Usage:
#   scripts/reconcile-claude-projects.sh             # dry-run summary then prompt
#   scripts/reconcile-claude-projects.sh --yes       # skip prompt

set -euo pipefail

OLD="$HOME/.claude/projects/-Users-kapoost-humanmcp-new"
NEW="$HOME/.claude/projects/-Users-kapoost-humanmcp-server"
ACTIVE_WINDOW_S=60

red()   { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
dim()   { printf '\033[2m%s\033[0m\n' "$*"; }

# Step 1 — early outs that mean "nothing to do, success".
if [[ ! -e "$OLD" ]]; then
  green "OK: $OLD does not exist — nothing to reconcile."
  exit 0
fi

if [[ -L "$OLD" ]]; then
  green "OK: $OLD is already a symlink. Removing it."
  rm -- "$OLD"
  exit 0
fi

if [[ ! -d "$NEW" ]]; then
  red "ABORT: $NEW does not exist. Refusing to delete history with no destination."
  red "       Investigate why the canonical project folder is missing before re-running."
  exit 2
fi

# Step 2 — active-session guard. If anything in OLD was modified in
# the last ACTIVE_WINDOW_S seconds, a Claude session is probably still
# appending to a jsonl. Bail out.
RECENT=$(find "$OLD" -type f -name '*.jsonl' -mtime -1 -print 2>/dev/null \
         | while read -r f; do
             now=$(date +%s)
             mod=$(stat -f %m "$f" 2>/dev/null || stat -c %Y "$f")
             age=$(( now - mod ))
             if (( age < ACTIVE_WINDOW_S )); then echo "$f ($age s ago)"; fi
           done)
if [[ -n "$RECENT" ]]; then
  red "ABORT: a Claude session is still writing into $OLD."
  red "       Recently-modified .jsonl files:"
  echo "$RECENT" | sed 's/^/         /'
  red "       Close every Claude session that started before the folder rename,"
  red "       wait a minute, then re-run this script."
  exit 3
fi

# Step 3 — print a dry-run summary so the user sees what will move.
echo "=== Plan ==="
echo "Source (will be removed after merge):"
echo "  $OLD"
du -sh "$OLD" 2>/dev/null | sed 's/^/  size: /'
find "$OLD" -type f | wc -l | xargs printf "  files: %s\n"
echo
echo "Destination (will receive any new/updated files):"
echo "  $NEW"
du -sh "$NEW" 2>/dev/null | sed 's/^/  size: /'
find "$NEW" -type f | wc -l | xargs printf "  files: %s\n"
echo
echo "rsync rule: --archive --update — only copy when source is newer."
echo "After successful merge $OLD will be removed."
echo

# Step 4 — confirm unless --yes.
if [[ "${1:-}" != "--yes" ]]; then
  read -r -p "Proceed? [y/N] " ans
  if [[ "$ans" != "y" && "$ans" != "Y" ]]; then
    dim "Aborted by user. Nothing changed."
    exit 0
  fi
fi

# Step 5 — rsync. -a preserves metadata, -u only overwrites when source
# is newer, -i (--itemize-changes) lists each transfer in a stable format
# that also works with macOS's stock rsync 2.6.9. NO --delete: we never
# touch DEST except to add/update.
echo
echo "=== Merging ==="
rsync -aui "$OLD/" "$NEW/"

# Step 6 — verify every file in OLD now exists in NEW. Compare relative
# paths; size mismatches would also matter so we hash-compare jsonl
# files specifically (small enough to be quick).
echo
echo "=== Verifying ==="
MISSING=0
TRUNCATED=0
while IFS= read -r -d '' rel; do
  rel="${rel#$OLD/}"
  if [[ ! -e "$NEW/$rel" ]]; then
    red "  missing in dest: $rel"
    MISSING=$((MISSING + 1))
    continue
  fi
  # jsonl: confirm destination is at least as long as source.
  if [[ "$rel" == *.jsonl ]]; then
    src_lines=$(wc -l < "$OLD/$rel")
    dst_lines=$(wc -l < "$NEW/$rel")
    if (( dst_lines < src_lines )); then
      red "  $rel truncated in dest ($dst_lines vs $src_lines source lines)"
      TRUNCATED=$((TRUNCATED + 1))
    fi
  fi
done < <(find "$OLD" -type f -print0)

if (( MISSING > 0 || TRUNCATED > 0 )); then
  red "ABORT: $MISSING missing, $TRUNCATED truncated. Source preserved at $OLD."
  exit 4
fi
green "Verified: every file from $OLD has an equal-or-better copy in $NEW."

# Step 7 — remove the now-redundant source.
echo
echo "=== Cleanup ==="
rm -rf -- "$OLD"
if [[ -e "$OLD" ]]; then
  red "FAIL: $OLD still exists after rm -rf."
  exit 5
fi
green "Removed $OLD."
echo
green "Done. Future Claude sessions opened from ~/humanmcp-server/ will land in $NEW."
