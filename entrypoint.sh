#!/bin/sh
# Sync default content to volume — user content dirs are preserve-only
# (don't overwrite user edits), rituals/ is always refreshed because
# ritual manifests are code-adjacent config, not user content.
for dir in poems personas skills translations; do
  if [ -d "/app/default-content/$dir" ]; then
    mkdir -p "/data/content/$dir"
    for f in /app/default-content/$dir/*; do
      fname=$(basename "$f")
      target="/data/content/$dir/$fname"
      if [ ! -s "$target" ]; then
        cp "$f" "$target"
        echo "Copied $dir/$fname"
      fi
    done
  fi
done
# Ritual manifests: force overwrite on every boot so shipped updates land.
if [ -d "/app/default-content/rituals" ]; then
  mkdir -p /data/content/rituals
  cp /app/default-content/rituals/*.json /data/content/rituals/
  echo "Refreshed rituals manifests"
fi
exec ./humanmcp
