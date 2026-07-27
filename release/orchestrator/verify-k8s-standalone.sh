#!/usr/bin/env bash
# Prove the RELEASED Kubernetes integration module is externally consumable at its
# Go v5 semantic-import path. From a throwaway consumer module — no go.work, no
# replace, its own throwaway module cache — `go get` the module at
# github.com/trianalab/pacto/integrations/kubernetes/v5@v5.0.0 and build a program
# that imports a stable exported symbol, exactly as an external user would. This is
# the external-consumer proof that the /v5 module path + nested tag are valid Go.
#
# Non-invasive, mirrors verify-standalone.sh: local staging tags on the current
# tree, a process-scoped git config (never the user's global) that rewrites the
# public repo URL to this checkout, and a throwaway GOMODCACHE. The tags are
# deleted on exit. No publish, no production coordinate.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

# The nested-module tag baseline for the /v5 path comes straight from the release
# plan (build-release-plan.mjs derives it from the module PATH major, so a /v5
# module never gets a v4 tag). Core is pinned by the integration go.mod; stage both
# on HEAD so the standalone module graph resolves.
plan() { node -e 'console.log(JSON.parse(require("fs").readFileSync(process.argv[1]))'"$1"')' "$ROOT/release/release-plan.json"; }
K8S_TAG="$(plan '.groups.kubernetes.tags[0]')"          # integrations/kubernetes/v5.0.0
K8S_VER="${K8S_TAG##*/v}"                                # 5.0.0
CORE_PIN="$(plan '.groups.kubernetes.goModPin.version')" # v3.0.0
MODULE="github.com/trianalab/pacto/integrations/kubernetes/v5"
echo "consume ${MODULE}@v${K8S_VER} (tag ${K8S_TAG}); core pin ${CORE_PIN}"

TMPGIT="$(mktemp)"; WORK="$(mktemp -d)"; MODCACHE="$(mktemp -d)"
printf '[url "file://%s"]\n\tinsteadOf = https://github.com/trianalab/pacto\n' "$ROOT" > "$TMPGIT"

git -C "$ROOT" tag -f "$K8S_TAG" HEAD >/dev/null 2>&1    # reproducible local staging tags
git -C "$ROOT" tag -f "$CORE_PIN" HEAD >/dev/null 2>&1
cleanup() {
  git -C "$ROOT" tag -d "$K8S_TAG" >/dev/null 2>&1 || true
  git -C "$ROOT" tag -d "$CORE_PIN" >/dev/null 2>&1 || true
  rm -rf "$WORK" "$MODCACHE" "$TMPGIT" 2>/dev/null || true
}
trap cleanup EXIT

# A throwaway consumer module: nothing from the workspace reaches it.
mkdir -p "$WORK/consumer"
cat > "$WORK/consumer/go.mod" <<EOF
module pacto-k8s-consumer

go 1.26.5
EOF
cat > "$WORK/consumer/main.go" <<'EOF'
// External consumer of the released Kubernetes integration module. It imports a
// stable exported symbol from the api/v1alpha1 package to prove the /v5 module
// resolves and compiles for an outside user.
package main

import (
	"fmt"

	apiv1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
)

func main() {
	fmt.Println(apiv1alpha1.GroupVersion.String())
}
EOF

cd "$WORK/consumer"
export GIT_CONFIG_GLOBAL="$TMPGIT" GIT_CONFIG_SYSTEM=/dev/null \
       GOWORK=off GOFLAGS=-mod=mod GOPROXY=direct \
       GOPRIVATE='github.com/trianalab/*' GONOSUMDB='github.com/trianalab/*' \
       GOMODCACHE="$MODCACHE"

echo "go get ${MODULE}@v${K8S_VER} (external, GOWORK=off, GOPROXY=direct)..."
go get "${MODULE}@v${K8S_VER}"
# Fail closed: an external consumer must resolve the module with NO replace.
[ "$(grep -c '^replace' go.mod)" = "0" ] || { echo "ERROR: consumer go.mod grew a replace directive"; exit 1; }
grep -q "${MODULE} v${K8S_VER}" go.mod || { echo "ERROR: ${MODULE} v${K8S_VER} not required after go get"; exit 1; }

echo "go build ./... (standalone consumer, no go.work, no replace)..."
go build ./...
echo "output: $(go run .)"
echo "K8S-MODULE-STANDALONE OK"
