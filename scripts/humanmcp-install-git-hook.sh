#!/bin/sh
# Install the humanmcp post-commit hook globally.
#
# Kapoost's git uses core.hooksPath = ~/.config/git/hooks/ (see the
# siostrzany pre-commit that pilnuje e-maila commitów). So one install
# covers every git repo on this Mac — no per-repo dance, no clobber of
# local .git/hooks.
#
# Per-repo escape hatch: `git config humanmcp.hookOff true` (same knob the
# pre-commit uses).
#
# Rerun after upgrading humanmcp-server — it just copies the current
# script over the installed one.

set -eu

SRC="$(cd "$(dirname "$0")" && pwd)/humanmcp-post-commit-hook.sh"
if [ ! -f "$SRC" ]; then
  echo "ERROR: hook source not found at $SRC" >&2
  exit 1
fi

HOOKS_DIR=$(git config --global core.hooksPath 2>/dev/null || true)
if [ -z "$HOOKS_DIR" ]; then
  HOOKS_DIR="$HOME/.config/git/hooks"
  echo "core.hooksPath not set globally — installing to $HOOKS_DIR and setting the config."
  git config --global core.hooksPath "$HOOKS_DIR"
fi
mkdir -p "$HOOKS_DIR"

TARGET="$HOOKS_DIR/post-commit"

# If a non-humanmcp post-commit already lives here, do not clobber.
if [ -e "$TARGET" ] && ! grep -q 'humanmcp-narada-tracker' "$TARGET"; then
  echo "REFUSE — $TARGET exists and does not look like ours." >&2
  echo "Move it out of the way (or extend it manually) and rerun."  >&2
  exit 2
fi

cp -f "$SRC" "$TARGET"
chmod +x "$TARGET"

echo "installed: $TARGET"
echo "state file: ${HUMANMCP_STATE_DIR:-$HOME/.humanmcp}/narada-events.jsonl"
echo "per-repo opt-out: git config humanmcp.hookOff true"
