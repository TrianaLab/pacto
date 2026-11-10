#!/usr/bin/env bash
# Usage: publish.sh DIR (ci.yml job aval-publish: on a push to main, or a
# maintainer-authored and maintainer-triggered pull request from a branch of
# this repository).
# DIR holds one directory per suite job artifact (aval-<job>/), each with the
# job's .aval/<suite>-{result,report}.json. Signs keyless and pushes to
# AVAL_REGISTRY every result this run produced and may be reused: it arrived
# in the artifact of the job that runs its suite (the table below), the suite
# ran (the report is not a verified hit), passed, may be reused by a policy
# (not non-deterministic or external-dependencies, which main.yaml and pr.yaml
# never allow), and its report shows no security event. Any suite job runs
# code nobody verifies (downloaded CLIs, upstream manifests); the table keeps
# such code from getting a result for another suite signed. A failing result
# is never pushed: it would win ties against passing ones at the same trust
# level. `aval push` recomputes the fingerprint from this checkout and refuses
# a result recorded for other inputs. Each push gets a fresh GitHub OIDC
# token. Every result is tried; the step fails at the end if any push failed,
# a result arrived in the wrong artifact or a security event was found.
set -euo pipefail
dir=${1:?usage: publish.sh DIR}
: "${AVAL_REGISTRY:?}" "${GH_TOKEN:?}" "${ACTIONS_ID_TOKEN_REQUEST_URL:?}" "${ACTIONS_ID_TOKEN_REQUEST_TOKEN:?}"

# One line per artifact, then the suites its job runs. tests/architecture
# (TestPublishTrustsEachArtifactOnlyForItsOwnSuites) keeps it equal to what
# ci.yml and delivery-demo.yml upload.
allowed='
aval-ci-dashboard ui
aval-ci-e2e-compose compose-selftest compose-browser runtime-docker runtime-run
aval-ci-e2e-envtest k8s-envtest
aval-ci-engine unit examples-go demo-validate integration acceptance-go fleet-graph demo-arc
aval-ci-gates gates gates-sumdb
aval-ci-integration-kubernetes k8s-unit k8s-chart
aval-ci-oci release-detect
aval-ci-perf-envtest perf-envtest
aval-dashboard-e2e browser
aval-delivery-demo-agent delivery-demo-agent
aval-delivery-demo-browser delivery-demo-browser
aval-delivery-demo-freeze delivery-demo-freeze
aval-delivery-demo-reach delivery-demo-reach
aval-delivery-demo-rollback delivery-demo-rollback
aval-delivery-demo-story delivery-demo-story
aval-kind-dashboard kind-dashboard
aval-kind-delivery-core kind-delivery-core
aval-kind-delivery-health kind-delivery-health
aval-kind-delivery-identity kind-delivery-identity
aval-kind-delivery-ladder kind-delivery-ladder
aval-kind-delivery-ordering kind-delivery-ordering
aval-kind-delivery-publish kind-delivery-publish
aval-kind-delivery-records kind-delivery-records
aval-kind-evidence kind-evidence
aval-kind-gitops-argocd kind-gitops-argocd
aval-kind-gitops-flux kind-gitops-flux
aval-kind-observation kind-observation
aval-kind-operational-graph kind-operational-graph
aval-kind-reconcile kind-reconcile
aval-kind-runtime-conformance kind-runtime-conformance
aval-kind-upgrade kind-upgrade
aval-release-version-test release-version
'

aval=".aval/aval-$(sed -n 's/^AVAL_VERSION=//p' .github/aval/install.sh)/aval"
export DOCKER_CONFIG="$RUNNER_TEMP/docker"
echo "$GH_TOKEN" | docker login ghcr.io -u "$GITHUB_ACTOR" --password-stdin
shopt -s nullglob
rc=0
seen=" "
for adir in "$dir"/*/; do
  artifact=$(basename "$adir")
  suites=" $(awk -v a="$artifact" '$1 == a { $1 = ""; print }' <<< "$allowed") "
  for report in "$adir"*-report.json; do
    suite=$(basename "$report" -report.json)
    result="$adir$suite-result.json"
    if [[ "$suites" != *" $suite "* ]]; then
      echo "::error::$artifact may not carry a result for $suite (its job does not run it); not pushing it"
      rc=1
      continue
    fi
    if [[ "$seen" == *" $suite "* ]]; then
      echo "::error::$suite arrived twice; not pushing it again"
      rc=1
      continue
    fi
    seen+="$suite "
    if [ ! -f "aval.$suite.yaml" ]; then
      echo "::error::$report names no contract at the repository root; not pushing it"
      rc=1
      continue
    fi
    if jq -e -f .github/aval/security.jq "$report" > /dev/null; then
      echo "::error::$suite: a security event in this run (verification failure or flooding); nothing is pushed beside it"
      rc=1
      continue
    fi
    if jq -e '.outcome == "verified"' "$report" > /dev/null; then
      echo "$suite: reused a verified result, nothing to push"
      continue
    fi
    # A lookup error (registry private or unreachable) is not a miss: the
    # fingerprint may already hold results. Pushing again from every pull
    # request piles attestations past aval's 64 per fingerprint, and the
    # lookup then reports flooding (exit 5). main still publishes.
    if [ "${GITHUB_EVENT_NAME:-}" = pull_request ] && jq -e '.outcome == "error"' "$report" > /dev/null; then
      echo "$suite: lookup failed (registry unreachable or private), not pushing from a pull request; main publishes"
      continue
    fi
    if [ ! -f "$result" ]; then
      echo "$suite: no recorded result (see the suite job's log), nothing to push"
      continue
    fi
    if ! jq -e '.result == "passed"' "$result" > /dev/null; then
      echo "$suite: did not pass, nothing to push"
      continue
    fi
    # Skip what main.yaml and pr.yaml never reuse (no allowedCacheability):
    # pushing it adds nothing and is one more way to fail main. Drop
    # external-dependencies here only if a policy ever opts in.
    if jq -e '.test.cacheability == "non-deterministic" or .test.cacheability == "external-dependencies"' "$result" > /dev/null; then
      echo "$suite: $(jq -r .test.cacheability "$result"), never reused by main.yaml or pr.yaml, nothing to push"
      continue
    fi
    SIGSTORE_ID_TOKEN=$(curl -fsS --max-time 30 -H "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
      "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=sigstore" | jq -r .value)
    export SIGSTORE_ID_TOKEN
    if ! "$aval" push -f "aval.$suite.yaml" --registry "$AVAL_REGISTRY" --result-file "$result"; then
      echo "::error::$suite: aval push failed"
      rc=1
    fi
  done
done
exit $rc
