#!/usr/bin/env bash
# The one-step CI acceptance (#525), no cluster and no Docker. It proves #452
# decision 1's pactification budget (every pacto.yaml of the demo monorepo at
# most 2 lines, every caller pacto ci init writes at most 5) and that pacto ci
# and examples/ci/any-ci.sh exit with the same plan code: 0 clear, 1 blocked,
# 2 unverified. 510-S8's kind journey may call the budget section instead of
# repeating it.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
FIX="$ROOT/tests/acceptance/local/fixtures/ci-step"
WORK="$(mktemp -d)"
BIN_DIR="$WORK/bin"
trap 'rm -rf "$WORK"; [ -z "${REG_PID:-}" ] || kill "$REG_PID" 2>/dev/null || true' EXIT

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1"; exit 1; }
assert_contains() {
  if grep -Fq "$2" <<<"$1"; then
    pass "$3"
  else
    echo "--- output ---"; echo "$1"; fail "$3"
  fi
}

# The runner's own CI variables would make pacto ci resolve the runner's
# registry and write its job summary; this run is hermetic.
unset GITHUB_ACTIONS GITHUB_REPOSITORY_OWNER GITHUB_SERVER_URL GITHUB_BASE_REF GITHUB_STEP_SUMMARY \
  ACTIONS_ID_TOKEN_REQUEST_URL GITLAB_CI CI_REGISTRY_IMAGE CI_MERGE_REQUEST_DIFF_BASE_SHA CI_PROJECT_DIR \
  PACTO_REGISTRY PACTO_REGISTRY_USERNAME PACTO_REGISTRY_PASSWORD PACTO_REGISTRY_TOKEN PACTO_PUBLISH

echo "== build pacto =="
mkdir -p "$BIN_DIR"
go build -o "$BIN_DIR/pacto" "$ROOT/cmd/pacto"
go build -o "$WORK/localregistry" "$ROOT/tests/acceptance/local/localregistry"
# any-ci.sh runs the pacto on PATH, as a CI job that installed it would.
export PATH="$BIN_DIR:$PATH"

echo "== budget =="
while IFS= read -r f; do
  n="$(grep -c '[^[:space:]]' "$f" || true)"
  rel="${f#"$ROOT"/}"
  if [ "$n" -le 2 ]; then pass "$rel has $n lines (at most 2)"; else fail "$rel has $n lines, the budget is 2"; fi
done < <(find "$ROOT/examples/demo/monorepo" -name pacto.yaml | sort)
mkdir -p "$WORK/empty"
for platform in github gitlab; do
  out="$(pacto ci init --platform "$platform" --dry-run --output-format json "$WORK/empty")"
  n="$(grep -oE '"lines": *[0-9]+' <<<"$out" | grep -oE '[0-9]+$')"
  if [ "$n" -le 5 ]; then pass "$platform caller has $n lines (at most 5)"; else fail "$platform caller has $n lines, the budget is 5"; fi
done
if [ -n "$(ls -A "$WORK/empty")" ]; then fail "--dry-run wrote into the repository"; fi

echo "== exit codes =="
REG_PORT=15073
REG_HOST="127.0.0.1:${REG_PORT}"
# A registry already on the port would carry state from another run.
if curl -fsS "http://${REG_HOST}/v2/" >/dev/null 2>&1; then fail "port $REG_PORT is already serving a registry"; fi
"$WORK/localregistry" --port "$REG_PORT" >/dev/null 2>&1 &
REG_PID=$!
for _ in $(seq 1 50); do curl -fsS "http://${REG_HOST}/v2/" >/dev/null 2>&1 && break; sleep 0.2; done
kill -0 "$REG_PID" 2>/dev/null || fail "the local registry did not start on port $REG_PORT"
export PACTO_INSECURE_REGISTRIES="$REG_HOST"

code_of() { # runs "$@", prints its exit code
  set +e
  "$@" >"$WORK/out" 2>&1
  local c=$?
  set -e
  echo "$c"
}
expect_code() { # name want dir [pacto ci flags...]
  local name="$1" want="$2" dir="$3"
  shift 3
  local direct script
  direct="$(code_of pacto ci "$dir" "$@")"
  script="$(code_of sh "$ROOT/examples/ci/any-ci.sh" "$dir" "$@")"
  echo "  $name: pacto ci exit $direct, any-ci.sh exit $script"
  if [ "$direct" = "$want" ] && [ "$script" = "$want" ]; then
    pass "$name exits $want both ways"
  else
    cat "$WORK/out"; fail "$name: want $want from both, got $direct and $script"
  fi
}
expect_code "monorepo (unverified)" 2 "$ROOT/examples/demo/monorepo"
expect_code "collision (blocked)" 1 "$FIX/collision"
expect_code "declared (clear)" 0 "$FIX/declared" --registry "$REG_HOST/acme"

echo "== pacto ci init writes once =="
cp -R "$ROOT/examples/demo/monorepo" "$WORK/repo"
mkdir -p "$WORK/repo/.github"
out="$(pacto ci init "$WORK/repo")"
assert_contains "$out" "wrote .github/workflows/pacto.yml (5 lines):" "the GitHub caller is written"
if cmp -s "$WORK/repo/.github/workflows/pacto.yml" "$ROOT/examples/ci/github/pacto.yml"; then pass "the written caller is examples/ci/github/pacto.yml"; else fail "the written caller differs from examples/ci/github/pacto.yml"; fi
out="$(pacto ci init "$WORK/repo")"
assert_contains "$out" ".github/workflows/pacto.yml is up to date" "a second run leaves it as it is"

echo "ci-step acceptance passed"
