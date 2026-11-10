#!/usr/bin/env bash
# kind-run.sh LOGFILE CMD... — run one kind acceptance in CI, keep its whole
# output in LOGFILE for the artifact upload, and say which kind of failure it
# was:
#
#   infrastructure failed  the harness's infra helper (tests/acceptance/kind/lib.sh)
#                          gave up on cluster creation, an image pull or load, or
#                          a third-party install or rollout. Re-run the job.
#   aval verification failed  an attestation for the suite's inputs failed
#                          signature or policy verification (.github/aval/run.sh,
#                          exit 5): a security event, never a flake to re-run.
#   a check failed         anything else: a claim the scenario makes did not hold.
#
# It reads the INFRASTRUCTURE FAILED line rather than the exit status, because
# `make` turns every failing recipe into exit 2. The exit status is the command's.
set -uo pipefail
# ubuntu-26.04 runners boot with the kernel's inotify limits (128 instances):
# the image writes 1280 to /etc/sysctl.conf, which 26.04 no longer reads
# (actions/runner-images#15108). Two kind clusters run out, and kube-proxy
# dies with "too many open files". Restore what the image meant to set.
[ "${GITHUB_ACTIONS:-}" = true ] && sudo sysctl -qw fs.inotify.max_user_instances=1280 fs.inotify.max_user_watches=655360
log="$1"
shift
mkdir -p "$(dirname "$log")"
"$@" 2>&1 | tee "$log"
rc=${PIPESTATUS[0]}
[ "$rc" -eq 0 ] && exit 0

if line="$(grep -m1 'aval: SECURITY:' "$log")"; then
  title="aval verification failed"
  msg="${line#*aval: SECURITY: } — a security event, not a flake; do not re-run, see the uploaded Aval report"
elif line="$(grep -m1 'INFRASTRUCTURE FAILED' "$log")"; then
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
