#!/usr/bin/env zsh
# deploy.zsh — safe deploy for humanmcp
# Run from: ~/humanmcp/humanmcp/
# Usage:    zsh deploy.zsh "commit message"

set -e  # stop on any error

MSG=${1:-"chore: update"}
WEB=internal/web

# ── 1. Remove stale handler.go if it crept back in ────────────────────────────
if [[ -f "$WEB/handler.go" ]]; then
  echo "⚠️  Removing stale $WEB/handler.go"
  rm "$WEB/handler.go"
  git rm --cached "$WEB/handler.go" 2>/dev/null || true
fi

# ── 2. Build ───────────────────────────────────────────────────────────────────
echo "🔨 Building..."
go build ./...
echo "   ✓ build clean"

# ── 3. Test ───────────────────────────────────────────────────────────────────
echo "🧪 Testing..."
go test ./...
echo "   ✓ all tests pass"

# ── 4. Commit & push ──────────────────────────────────────────────────────────
echo "📦 Committing..."
git add -A
git commit -m "$MSG"
git push

# ── 5. Deploy ─────────────────────────────────────────────────────────────────
echo "🚀 Deploying..."
fly deploy --build-arg CACHEBUST=$(date +%s) --app kapoost-humanmcp

echo "✅ Done — https://kapoost-humanmcp.fly.dev"
