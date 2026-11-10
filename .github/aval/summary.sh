#!/usr/bin/env bash
# Usage: summary.sh. Adds every .aval/<suite>-report.json this job wrote to the
# job summary. Only the outcome and result are printed, and only known values:
# the reason can carry text from an untrusted attestation, so it stays in the
# uploaded report. A security event (.github/aval/security.jq: a verification
# failure, as the outcome or among the rejected candidates, or flooding) is
# annotated as an error and fails the step: it is never a miss.
set -euo pipefail
shopt -s nullglob
reports=(.aval/*-report.json)
if [ ${#reports[@]} -eq 0 ]; then
  echo "### aval: no report" >> "$GITHUB_STEP_SUMMARY"
  exit 0
fi
rc=0
for report in "${reports[@]}"; do
  name=$(basename "$report" -report.json)
  outcome=$(jq -r '.outcome // ""' "$report")
  case "$outcome" in verified | miss | verification-failed | error) ;; *) outcome=unknown ;; esac
  result=$(jq -r '.result // ""' "$report")
  case "$result" in passed | failed | "") ;; *) result=unknown ;; esac
  echo "### $name: \`$outcome\` $result" >> "$GITHUB_STEP_SUMMARY"
  if jq -e -f .github/aval/security.jq "$report" > /dev/null; then
    echo "::error title=aval security event ($name)::an attestation for this fingerprint failed signature or policy verification, or the fingerprint holds more attestations than aval lists (flooding); see the SECURITY line in the log and the uploaded report"
    rc=1
  fi
done
exit $rc
