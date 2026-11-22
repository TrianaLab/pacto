#!/usr/bin/env bash
# Argo CD for the kind scenarios that deploy through it: the pinned core
# install and an Application whose source is an OCI manifest artifact in the
# in-cluster registry. Sourced after lib.sh, never executed directly; reads the
# caller's $ARGO_NS and $REG_HOST, and _PACTO_ROOT from lib.sh.
#
# The behaviour is gitops-argocd.sh's, which keeps its own copy until the
# Argo CD scenarios are moved onto this file (516-S6 review note R-4).
#
#   install_argocd_core              the core install, CRDs served, ready
#   argo_app_oci NAME REPO TAG       an Application syncing REPO:TAG into $APP_NS

# The same pin as gitops-argocd.sh: a release with OCI application sources
# (v3.1.0 and later). TestArgoPluginSidecarTagMatchesKindPin reads it.
ARGOCD_VERSION=v3.5.2
# What ORAS labels the manifest layer. Argo allows this media type by default
# and refuses an artifact carrying anything but exactly one content layer.
# shellcheck disable=SC2034  # read by the scenarios that source this file
ARGO_LAYER_TYPE=application/vnd.oci.image.layer.v1.tar+gzip

# install_argocd_core — Argo CD $ARGOCD_VERSION, core install (no API server,
# no UI), with its images pulled on the host and loaded into the nodes.
install_argocd_core() {
  local manifest images img hub
  manifest="$(mktemp)"
  # The manifests come from the repository at the tag: a release carries CLI
  # binaries only.
  bash "$_PACTO_ROOT/release/scripts/retry.sh" curl -fsSL -o "$manifest" \
    "https://raw.githubusercontent.com/argoproj/argo-cd/${ARGOCD_VERSION}/manifests/core-install.yaml"
  # Pulled on the host (retried) and loaded, so the node never pulls them: an
  # anonymous public.ecr.aws pull from a shared runner gets throttled, and an
  # image from public.ecr.aws/docker/library falls back to the Docker Hub
  # official image it mirrors.
  sed -i.bak 's/imagePullPolicy: Always/imagePullPolicy: IfNotPresent/' "$manifest"
  images="$(awk '$1 == "image:" { print $2 }' "$manifest" | sort -u)"
  for img in $images; do
    bash "$_PACTO_ROOT/release/scripts/retry.sh" docker pull "$img" > /dev/null && continue
    case "$img" in
      public.ecr.aws/docker/library/*)
        hub="docker.io/library/${img#public.ecr.aws/docker/library/}"
        echo "  $img: pulling $hub instead"
        bash "$_PACTO_ROOT/release/scripts/retry.sh" docker pull "$hub" > /dev/null || fail "could not pull $img or $hub"
        docker tag "$hub" "$img"
        ;;
      *) fail "could not pull $img" ;;
    esac
  done
  # shellcheck disable=SC2086  # one image reference per word
  load_images $images
  kubectl create namespace "$ARGO_NS" --dry-run=client -o yaml | kubectl apply -f - > /dev/null
  # --server-side: the Application CRD is too large for a client-side apply's
  # last-applied-configuration annotation.
  kubectl apply -n "$ARGO_NS" --server-side -f "$manifest" > /dev/null
  # The apply returns before argoproj.io is in discovery; asking for the
  # resources is the check.
  # shellcheck disable=SC2329  # invoked by name through eventually
  _argo_crds_served() {
    kubectl get appprojects.argoproj.io -A > /dev/null 2>&1 \
      && kubectl get applications.argoproj.io -A > /dev/null 2>&1
  }
  eventually 40 _argo_crds_served || fail "argoproj.io never reached discovery after the core install"
  kubectl -n "$ARGO_NS" scale deploy argocd-applicationset-controller --replicas=0 > /dev/null 2>&1 || true
  # The API server creates the default AppProject on startup, and the core
  # install has none: without it every Application sits at InvalidSpecError.
  kubectl apply -f - > /dev/null << YAML
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata: { name: default, namespace: $ARGO_NS }
spec:
  sourceRepos: ['*']
  destinations: [{ server: '*', namespace: '*' }]
  clusterResourceWhitelist: [{ group: '*', kind: '*' }]
YAML
  kubectl -n "$ARGO_NS" rollout status deploy/argocd-redis --timeout=300s
  kubectl -n "$ARGO_NS" rollout status deploy/argocd-repo-server --timeout=300s
  kubectl -n "$ARGO_NS" rollout status statefulset/argocd-application-controller --timeout=300s
}

# argo_app_oci NAME REPO TAG — the Application NAME syncing the manifest
# artifact $REG_HOST/REPO:TAG into $APP_NS with automated pruning, plus the
# repository Secret that tells Argo the registry speaks plain HTTP. Applying it
# again with another TAG moves the Application to that tag.
argo_app_oci() {
  kubectl apply -f - > /dev/null << YAML
apiVersion: v1
kind: Secret
metadata:
  name: $1-manifests
  namespace: $ARGO_NS
  labels: { argocd.argoproj.io/secret-type: repository }
stringData:
  name: $1-manifests
  type: oci
  url: oci://${REG_HOST}/$2
  insecureOCIForceHttp: "true"
---
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata: { name: $1, namespace: $ARGO_NS }
spec:
  project: default
  source:
    repoURL: oci://${REG_HOST}/$2
    targetRevision: $3
    path: .
  destination: { server: https://kubernetes.default.svc, namespace: $APP_NS }
  syncPolicy:
    automated: { prune: true }
YAML
}
