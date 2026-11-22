#!/usr/bin/env bash
# Acceptance for `pacto declare --pr` against a real Git host.
#
# A throwaway Gitea and a throwaway registry run on ephemeral loopback ports.
# The CLI built from this checkout declares a fixture folder into the registry
# (a key names no person, so it writes draft-1), then opens a pull request on
# Gitea with the person's own token from GITEA_TOKEN. The Gitea API must then
# show one open pull request holding deploy/pacto.yaml and
# deploy/pacto.inferred.yaml.
#
# Host safety follows compose-demo.sh's rules with this script's own code:
# every container name carries $RUN_ID, a name already taken is refused and
# never reclaimed, each container is recorded by ID once the daemon created
# it, and the EXIT trap removes only those IDs.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
RUN_ID="$(od -An -N3 -tx1 /dev/urandom | tr -d ' \n')"
GITEA_IMAGE="gitea/gitea:1.22.6@sha256:538658de667c5d098a274f2f63aa6ec891d88f670cdd5282cf27221ba747dda4"
REGISTRY_IMAGE="registry:2@sha256:a3d8aaa63ed8681a604f1dea0aa03f100d5895b6a58ace528858a7b332415373"
WORK="$(mktemp -d /tmp/pacto-declare-pr.XXXXXX)"
OWNED_CONTAINERS=""

fail() { echo "  FAIL: $1" >&2; exit 1; }
pass() { echo "  ok: $1"; }

cleanup() {
	local id
	for id in $OWNED_CONTAINERS; do docker rm -f "$id" >/dev/null 2>&1 || true; done
	rm -rf "$WORK"
}
trap cleanup EXIT

# run_owned NAME DOCKER-CREATE-ARGS... starts a container this run owns and
# leaves its ID in $OWNED_ID. The ID is recorded between create and start, so
# a container that never started is still removed.
OWNED_ID=""
run_owned() {
	local name="$1"
	shift
	if docker container inspect "$name" >/dev/null 2>&1; then
		fail "a container named $name already exists; this script will not delete, replace or reuse one it did not create"
	fi
	OWNED_ID="$(docker create --name "$name" "$@")" || fail "could not create the container $name"
	OWNED_CONTAINERS="$OWNED_CONTAINERS $OWNED_ID"
	docker start "$OWNED_ID" >/dev/null || fail "created but could not start $name ($OWNED_ID)"
}

# host_port ID PORT is the ephemeral loopback port Docker published for PORT.
host_port() { docker port "$1" "$2/tcp" | head -n1 | sed 's/.*://'; }

# wait_for URL waits up to 120 s for URL to answer 200.
wait_for() {
	local _
	for _ in $(seq 1 120); do
		curl -fsS -o /dev/null "$1" 2>/dev/null && return 0
		sleep 1
	done
	fail "$1 did not answer in 120 s"
}

echo "== build the CLI =="
(cd "$ROOT" && CGO_ENABLED=0 go build -o "$WORK/pacto" ./cmd/pacto) || fail "go build ./cmd/pacto failed"

echo "== start Gitea and a registry =="
run_owned "pacto-declare-gitea-$RUN_ID" --publish 127.0.0.1::3000 \
	-e GITEA__security__INSTALL_LOCK=true -e GITEA__database__DB_TYPE=sqlite3 "$GITEA_IMAGE"
GITEA_ID="$OWNED_ID"
run_owned "pacto-declare-registry-$RUN_ID" --publish 127.0.0.1::5000 "$REGISTRY_IMAGE"
REG_ID="$OWNED_ID"
GITEA="127.0.0.1:$(host_port "$GITEA_ID" 3000)"
REG="127.0.0.1:$(host_port "$REG_ID" 5000)"
wait_for "http://$GITEA/api/v1/version"
wait_for "http://$REG/v2/"
pass "Gitea on $GITEA and a registry on $REG"

echo "== create ana, her token and ana/invoices =="
PASSWORD="pacto-$RUN_ID-pass"
docker exec -u git "$GITEA_ID" gitea admin user create --admin --username ana --password "$PASSWORD" \
	--email ana@acme.com --must-change-password=false >/dev/null || fail "gitea admin user create failed"
TOKEN="$(curl -fsS -u "ana:$PASSWORD" -H 'Content-Type: application/json' \
	-d '{"name":"pacto","scopes":["write:repository","write:user"]}' "http://$GITEA/api/v1/users/ana/tokens" | jq -r '.sha1')"
[ -n "$TOKEN" ] && [ "$TOKEN" != null ] || fail "Gitea gave no access token"
curl -fsS -H "Authorization: token $TOKEN" -H 'Content-Type: application/json' \
	-d '{"name":"invoices","auto_init":true,"default_branch":"main","private":true}' "http://$GITEA/api/v1/user/repos" >/dev/null ||
	fail "could not create ana/invoices"
pass "ana/invoices exists with a main branch"

echo "== pacto declare --pr =="
openssl ecparam -name prime256v1 -genkey -noout -out "$WORK/declare.key" 2>/dev/null || fail "openssl could not make the signing key"
cp -R "$ROOT/tests/acceptance/local/declare-fixture" "$WORK/invoices"
out="$(PACTO_INSECURE_REGISTRIES="$REG" GITEA_HOST="$GITEA" GITEA_TOKEN="$TOKEN" KUBECONFIG="$WORK/no-kubeconfig" \
	"$WORK/pacto" declare invoices --dir "$WORK/invoices" --registry "$REG/acme" --sign-key "$WORK/declare.key" \
	--repository "http://$GITEA/ana/invoices" --folder deploy --git-host gitea --pr --yes 2>&1)" ||
	fail "pacto declare --pr failed: $out"
echo "$out"
case "$out" in *"$TOKEN"*) fail "pacto declare printed the token" ;; esac
case "$out" in *"opened http://"*) ;; *) fail "pacto declare did not say it opened a pull request" ;; esac
pass "pacto declare wrote the draft and opened a pull request"

echo "== the pull request on Gitea =="
api() { curl -fsS -H "Authorization: token $TOKEN" "http://$GITEA/api/v1/repos/ana/invoices$1"; }
pulls="$(api '/pulls?state=open')"
[ "$(jq 'length' <<<"$pulls")" = 1 ] || fail "want one open pull request, got: $pulls"
number="$(jq -r '.[0].number' <<<"$pulls")"
files="$(api "/pulls/$number/files" | jq -r '.[].filename' | sort | tr '\n' ' ')"
[ "$files" = "deploy/pacto.inferred.yaml deploy/pacto.yaml " ] || fail "pull request $number holds: $files"
commits="$(api "/pulls/$number/commits" | jq 'length')"
[ "$commits" = 1 ] || fail "pull request $number holds $commits commits, want 1"
pass "pull request $number holds deploy/pacto.yaml and deploy/pacto.inferred.yaml in one commit"
