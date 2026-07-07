#!/bin/sh
# Sync default content to volume. Files shipped in the image (/app/default-content)
# are force-refreshed on every boot — this matches kapoost's git-based workflow
# where persona/skill edits live in the repo and are expected to propagate. UI-
# added content (files that exist only in /data/content, not in /app/default-content)
# is unaffected because we only iterate default-content sources.
#
# Poems dir is preserve-only because there's no shipped defaults for it — poems
# are user-authored via /new. The loop just makes sure the dir exists.
for dir in personas skills translations rituals; do
  if [ -d "/app/default-content/$dir" ]; then
    mkdir -p "/data/content/$dir"
    cp -f /app/default-content/$dir/* /data/content/$dir/ 2>/dev/null && \
      echo "Refreshed $dir from /app/default-content"
  fi
done
mkdir -p /data/content/poems
exec ./humanmcp
