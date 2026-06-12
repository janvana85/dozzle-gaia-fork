#!/bin/sh
set -eu

data_dir="${1:?usage: sanitize-test-notifications.sh DATA_DIR [TOPIC]}"
topic="${2:-honza}"
config="$data_dir/notifications.yml"

if [ ! -f "$config" ]; then
  echo "notification config not found: $config" >&2
  exit 1
fi

# Test copies may contain production destination and per-alert topic overrides.
# Route every ntfy path, including burst and quiet-hours delivery, to one topic.
sed -E -i \
  -e "s/^([[:space:]]+topic:).*/\\1 $topic/" \
  -e "s/^([[:space:]]+ntfyTopic:).*/\\1 $topic/" \
  -e "s/^([[:space:]]+burstNtfyTopic:).*/\\1 $topic/" \
  -e "s/^([[:space:]]+quietTopic:).*/\\1 $topic/" \
  "$config"

echo "All test ntfy topics in $config now route to $topic"
