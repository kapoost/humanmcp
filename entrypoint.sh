#!/bin/sh
# Sync default content to volume — only copy files that don't exist or are empty
for dir in poems personas skills; do
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
exec ./humanmcp
