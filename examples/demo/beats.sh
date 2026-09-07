# Every beat of the guided demo arc, written once.
#
# Three readers share this file. The acceptance script
# (tests/acceptance/local/demo-arc.sh) runs each beat and asserts on the bytes it
# prints. The transcript generator (release/scripts/gen_demo_transcripts.sh) runs
# the same beats and publishes those bytes to examples/demo/generated/. The guided
# tour (docs/examples/demo-tour.md) includes what was published. One copy, so a
# beat cannot be asserted one way in CI and printed another way on the page.
#
# Arguments only — no binary, no redirection, no quoting of the whole string: the
# caller supplies the binary and decides which streams it wants, and every caller
# word-splits the result, so no path here may contain a space. Paths are relative
# to the repository root because they are the paths a reader pastes; every caller
# cd's there first.
#
# shellcheck shell=bash

beat_args() {
  case "$1" in
    1)  echo "fleet search --local examples/demo/bundles" ;;
    2)  echo "fleet search --local examples/demo/bundles --target-state examples/demo/fleet-targets.yaml" ;;
    3)  echo "fleet explain identity/auth-service --local examples/demo/bundles --target-state examples/demo/fleet-targets.yaml --freshness 24h" ;;
    3-readiness)
        echo "explain examples/demo/bundles/api-gateway/v1.2.0" ;;
    4)  echo "fleet get --target commerce/orders-service --local examples/demo/bundles --target-state examples/demo/fleet-targets.yaml" ;;
    5)  echo "diff examples/demo/bundles/payments-service/v1.2.1 examples/demo/bundles/payments-service/v2.0.1" ;;
    6)  echo "diff examples/demo/bundles/payments-service/v1.0.0 examples/demo/bundles/payments-service/v1.1.0" ;;
    7)  echo "impact examples/demo/bundles/payments-service/v1.2.1 examples/demo/bundles/payments-service/v2.0.1 --local examples/demo/bundles" ;;
    8)  echo "impact examples/demo/bundles/payments-service/v1.2.1 examples/demo/bundles/payments-service/v2.0.1 --local examples/demo/bundles --traces examples/demo/traces.json" ;;
    9)  echo "impact examples/demo/bundles/payments-service/v1.2.1 examples/demo/bundles/payments-service/v2.0.1 --local examples/demo/bundles --traces examples/demo/traces.json --target-state examples/demo/fleet-targets.yaml" ;;
    10) echo "fleet reconcile --local examples/demo/bundles --traces examples/demo/traces.json" ;;
    11) echo "mcp examples/demo/bundles/payments-service/v2.1.0 --base-url http://127.0.0.1:1" ;;
    11-writes)
        echo "mcp examples/demo/bundles/payments-service/v2.1.0 --base-url http://127.0.0.1:1 --allow-writes" ;;
    *)  echo "beats.sh: no such beat: $1" >&2; exit 1 ;;
  esac
}
