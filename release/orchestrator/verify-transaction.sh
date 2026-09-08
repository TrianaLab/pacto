#!/usr/bin/env bash
# Every unit the transaction fired for must have a publisher job that SUCCEEDED.
#
# The point is to make a no-op release loud. A skipped job poisons its entire
# downstream closure — not only the jobs that need it directly — so a single
# skipped node upstream of the publishers takes the whole Kubernetes line with it
# while the run still reports success. That has happened twice, and both times the
# only symptom was a version that quietly did not exist. This check reads what the
# run actually did rather than modelling what it should have done, which is what
# makes it independent of the wiring gate in tests/release/dag_test.go.
#
# Reads the unit -> job mapping from the `# pacto-publishes: <unit>` markers in the
# workflow, so it cannot drift from the jobs it is checking.
# tests/release/one_publisher_test.go already proves every unit has exactly one.
#
# Usage: verify-transaction.sh <workflow-file>
#   NEEDS  JSON object of the `needs` context ({"job": {"result": "..."}, ...})
#   UNITS  JSON array of the transaction's changed units
set -euo pipefail

workflow="${1:?usage: verify-transaction.sh <workflow-file>}"
: "${NEEDS:?NEEDS (toJSON(needs)) is required}"
: "${UNITS:?UNITS (units_json) is required}"

# unit<TAB>job, one per marker: the job key is the next top-level `name:` line
# after the marker, skipping any comment lines between them. POSIX awk only —
# GNU's three-argument match() is not available on every runner image.
publishers="$(awk '
  /^[[:space:]]*# pacto-publishes:/ {
    unit = $0
    sub(/^.*# pacto-publishes:[[:space:]]*/, "", unit)
    sub(/[^a-z0-9-].*$/, "", unit)
    next
  }
  unit != "" && /^  [a-z0-9-]+:[[:space:]]*$/ {
    job = $0
    sub(/^  /, "", job)
    sub(/:[[:space:]]*$/, "", job)
    print unit "\t" job
    unit = ""
  }
' "$workflow")"

echo "units: $UNITS"
echo "job results:"
echo "$NEEDS" | jq -r 'to_entries[] | "  \(.key): \(.value.result)"'

missing=0
while read -r unit; do
  [ -n "$unit" ] || continue
  job="$(printf '%s\n' "$publishers" | awk -F'\t' -v u="$unit" '$1 == u { print $2; exit }')"
  if [ -z "$job" ]; then
    echo "::error::unit '$unit' is in the transaction but no job declares 'pacto-publishes: $unit'"
    missing=1
    continue
  fi
  result="$(printf '%s' "$NEEDS" | jq -r --arg j "$job" '.[$j].result // "absent"')"
  if [ "$result" != "success" ]; then
    echo "::error::unit '$unit' did not publish: job '$job' is '$result'"
    missing=1
  else
    echo "  ok: $unit -> $job"
  fi
done <<< "$(printf '%s' "$UNITS" | jq -r '.[]')"

if [ "$missing" -ne 0 ]; then
  echo "::error::the release fired but did not publish every unit it claimed."
  echo "::error::A 'skipped' above is the usual shape: look for a skipped job UPSTREAM"
  echo "::error::of the publisher, including one it does not need directly."
  exit 1
fi

echo "every unit in the transaction published"
