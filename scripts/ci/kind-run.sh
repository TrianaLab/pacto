#!/usr/bin/env bash
# kind-run.sh LOGFILE CMD... — run one kind acceptance in CI, keep its whole
# output in LOGFILE for the artifact upload, and say which kind of failure it
# was:
#
#   infrastructure failed  the harness's infra helper (tests/acceptance/kind/lib.sh)
#                          gave up on cluster creation, an image pull or load, a
#                          chart install or a rollout wait. Re-run the job.
#   a check failed         anything else: a claim the scenario makes did not hold.
#
# It reads the INFRASTRUCTURE FAILED line rather than the exit status, because
# `make` turns every failing recipe into exit 2. The exit status is the command's.
set -uo pipefail
log="$1"
shift
mkdir -p "$(dirname "$log")"
"$@" 2>&1 | tee "$log"
rc=${PIPESTATUS[0]}
[ "$rc" -eq 0 ] && exit 0

if line="$(grep -m1 'INFRASTRUCTURE FAILED' "$log")"; then
  title="infrastructure failed"
  msg="${line#*INFRASTRUCTURE FAILED: } — the environment broke, not a check; re-run the job"
else
  title="a check failed"
  msg="$(grep -m1 'FAIL:' "$log" | sed 's/^ *FAIL: *//')"
  msg="${msg:-exit $rc with no FAIL line; see the log artifact}"
fi
echo "::error title=${title}::${msg}"
[ -z "${GITHUB_STEP_SUMMARY:-}" ] || echo "**${title}**: ${msg}" >> "$GITHUB_STEP_SUMMARY"
exit "$rc"
