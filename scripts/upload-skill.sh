#!/usr/bin/env bash
# Upload a skill JSON to the live humanMCP server.
#
# Usage:
#   scripts/upload-skill.sh content/skills/<slug>.json
#
# Prompts for EDIT_TOKEN via `read -s` so the token never appears on
# screen or in shell history. Unsets the variable on exit.
set -euo pipefail

FILE="${1:-}"
if [[ -z "$FILE" ]]; then
  echo "usage: $0 <path/to/skill.json>" >&2
  exit 1
fi
if [[ ! -f "$FILE" ]]; then
  echo "file not found: $FILE" >&2
  exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "warning: jq not found — skipping JSON validity check" >&2
else
  jq -e . "$FILE" >/dev/null || { echo "invalid JSON: $FILE" >&2; exit 1; }
fi

DOMAIN="${HUMANMCP_DOMAIN:-kapoost-humanmcp.fly.dev}"

printf "EDIT_TOKEN: "
read -rs TOKEN
echo
if [[ -z "$TOKEN" ]]; then
  echo "no token entered" >&2
  exit 1
fi

trap 'unset TOKEN' EXIT

RESPONSE=$(curl -sS -X POST "https://${DOMAIN}/api/skills" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  --data-binary "@${FILE}")

echo "$RESPONSE"
