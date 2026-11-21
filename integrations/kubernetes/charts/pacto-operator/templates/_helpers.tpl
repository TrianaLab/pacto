{{/*
The chart's helpers: names, labels, ServiceAccount names, images and the
shared pieces of every pod. templates/_resources.tpl renders whole objects
from them; templates/delivery/_helpers.tpl adds what only delivery needs.
*/}}

{{/*
Expand the name of the chart.
*/}}
{{- define "pacto-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "pacto-operator.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "pacto-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Labels and selector labels. Every object the chart renders takes its labels
from these two. Each takes the root context, or a dict:
  ctx        the root context
  part       a suffix for app.kubernetes.io/name, for a part of the release
             whose pods the operator's selectors must never match (delivery)
  component  app.kubernetes.io/component
global.commonLabels is added to the labels, never to a selector.
*/}}
{{- define "pacto-operator.labels" -}}
{{- $ctx := .ctx | default . -}}
helm.sh/chart: {{ include "pacto-operator.chart" $ctx }}
{{ include "pacto-operator.selectorLabels" (dict "ctx" $ctx "part" .part) }}
{{- with $ctx.Chart.AppVersion }}
app.kubernetes.io/version: {{ . | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ $ctx.Release.Service }}
{{- with .component }}
app.kubernetes.io/component: {{ . }}
{{- end }}
{{- with $ctx.Values.global.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{- define "pacto-operator.selectorLabels" -}}
{{- $ctx := .ctx | default . -}}
app.kubernetes.io/name: {{ include "pacto-operator.name" $ctx }}{{ with .part }}-{{ . }}{{ end }}
app.kubernetes.io/instance: {{ $ctx.Release.Name }}
{{- with .component }}
app.kubernetes.io/component: {{ . }}
{{- end }}
{{- end }}

{{/*
The controller's labels and selector labels: the common ones plus the
control-plane label its Deployment, pods and metrics Service share.
*/}}
{{- define "pacto-operator.controller.labels" -}}
control-plane: controller-manager
{{ include "pacto-operator.labels" . }}
{{- end }}

{{- define "pacto-operator.controller.selectorLabels" -}}
control-plane: controller-manager
{{ include "pacto-operator.selectorLabels" . }}
{{- end }}

{{/*
A ServiceAccount name from its values (create, name) and the name it takes
when created unnamed: (list .Values.serviceAccount "fallback-name").
*/}}
{{- define "pacto-operator.serviceAccountName.of" -}}
{{- $sa := index . 0 }}
{{- if $sa.create }}
{{- default (index . 1) $sa.name }}
{{- else }}
{{- default "default" $sa.name }}
{{- end }}
{{- end }}

{{/*
The controller's ServiceAccount name.
*/}}
{{- define "pacto-operator.serviceAccountName" -}}
{{- include "pacto-operator.serviceAccountName.of" (list .Values.serviceAccount (include "pacto-operator.fullname" .)) }}
{{- end }}

{{/*
The owner policy JSON (an OwnerPolicy) from the declare block, empty when
neither defaultApprovers.bootstrap nor teams holds a rule. Nil-safe: a release
upgraded with --reuse-values from 6.x has no declare key.
*/}}
{{- define "pacto-operator.ownerPolicy.json" -}}
{{- $d := .Values.declare | default dict }}
{{- $approvers := dig "defaultApprovers" "bootstrap" list $d | default list }}
{{- $teams := dig "teams" dict $d | default dict }}
{{- if or $approvers $teams }}
{{- dict "defaultApprovers" $approvers "teams" $teams "emailIssuers" (dig "emailIssuers" list $d | default list) | toJson }}
{{- end }}
{{- end }}

{{/*
The Sigstore trusted root owner changes are verified against: declare.trustedRoot,
else delivery.publishers.trustedRoot; empty uses public-good Sigstore.
*/}}
{{- define "pacto-operator.ownerPolicy.trustedRoot" -}}
{{- $d := .Values.declare | default dict }}
{{- dig "trustedRoot" "" $d | default (dig "publishers" "trustedRoot" "" (.Values.delivery | default dict)) }}
{{- end }}

{{/*
The trust config whose role: policy keys sign the delivery policy, when
delivery.publishers.policy is set: the owner check classifies signers with
the verified policy's signer class rules, as the fleet does.
*/}}
{{- define "pacto-operator.ownerPolicy.signers" -}}
{{- $p := dig "publishers" dict (.Values.delivery | default dict) }}
{{- if dig "policy" "" $p }}{{ dig "policySigners" "" $p }}{{ end }}
{{- end }}

{{/*
The trusted root the dashboard verifies the declarations it signs with: the
owner check's, only when the dashboard declares (registry is set).
*/}}
{{- define "pacto-operator.dashboardTrustedRoot" -}}
{{- if .Values.registry }}{{ include "pacto-operator.ownerPolicy.trustedRoot" . }}{{ end }}
{{- end }}

{{/*
Whether the manager mounts the owner policy ConfigMap: it holds the owner
policy, the delivery policy's signers or the dashboard's trusted root.
*/}}
{{- define "pacto-operator.ownerPolicy.mounted" -}}
{{- if or (include "pacto-operator.ownerPolicy.json" .) (include "pacto-operator.ownerPolicy.signers" .) (include "pacto-operator.dashboardTrustedRoot" .) }}true{{ end }}
{{- end }}

{{/*
The immutable owner policy ConfigMap, named after its content (the policy,
the trusted root and the delivery policy's signers) so only a chart upgrade
changes what the operator mounts.
*/}}
{{- define "pacto-operator.ownerPolicy.name" -}}
{{- printf "%s-owner-policy-%s" (include "pacto-operator.fullname" .) (printf "%s%s%s" (include "pacto-operator.ownerPolicy.json" .) (include "pacto-operator.ownerPolicy.trustedRoot" .) (include "pacto-operator.ownerPolicy.signers" .) | sha256sum | trunc 10) }}
{{- end }}

{{/*
Controller arguments derived from values
*/}}
{{- define "pacto-operator.controllerArgs" -}}
- --leader-elect={{ .Values.leaderElection.enabled }}
- --health-probe-bind-address=:8081
{{- if .Values.metrics.enabled }}
- --metrics-bind-address=:{{ .Values.metrics.service.port }}
- --metrics-secure={{ .Values.metrics.secure }}
{{- else }}
- --metrics-bind-address=0
{{- end }}
{{- if or .Values.controller.watchNamespaces .Values.controller.watchNamespace }}
- --watch-namespaces={{ include "pacto-operator.watchNamespaces" . | fromYamlArray | join "," }}
{{- end }}
{{- with .Values.registry }}
- --registry={{ . }}
{{- end }}
{{- with .Values.registryPullSecret }}
{{- if not $.Values.registry }}
{{- fail "registryPullSecret needs registry: the Secret is used only under the registry's push prefixes" }}
{{- end }}
- --registry-pull-secret={{ . }}
{{- end }}
{{- if .Values.registry }}
{{- range include "pacto-operator.pushPrefixes" . | fromYamlArray }}
- --registry-push-prefix={{ . }}
{{- end }}
{{- end }}
{{- /* .Values.declare in the guard: the declare block decides (gen_integration_docs reads guards). */}}
{{- if and .Values.declare (include "pacto-operator.ownerPolicy.json" .) }}
- --owner-policy=/etc/pacto/owner-policy/policy.json
- --dashboard-owner-policy-configmap={{ include "pacto-operator.ownerPolicy.name" . }}
{{- if include "pacto-operator.ownerPolicy.trustedRoot" . }}
- --trusted-root=/etc/pacto/owner-policy/trusted_root.json
{{- end }}
{{- end }}
{{- /* The owner check reads the publisher rule whether or not a declare block is set: without it every CI publish's owner change reads unverified. */}}
{{- with dig "publishers" "registry" "" (.Values.delivery | default dict) }}
- --publishers-registry={{ . }}
- --publishers-default-branch={{ dig "publishers" "defaultBranch" "" ($.Values.delivery | default dict) | default "main" }}
{{- end }}
{{- if include "pacto-operator.ownerPolicy.signers" . }}
- --publishers-policy={{ .Values.delivery.publishers.policy }}
- --publishers-policy-signers=/etc/pacto/owner-policy/policy-signers.yaml
{{- end }}
{{- if .Values.controller.stabilizationWindow }}
- --stabilization-window={{ .Values.controller.stabilizationWindow }}
{{- end }}
{{- with include "pacto-operator.observeMode" (list .Values.controller.probing .Values.controller.enableProbing) }}
{{- if eq . "legacy-on" }}
- --enable-probing
{{- else }}
- --enable-probing={{ . }}
{{- end }}
{{- end }}
{{- with include "pacto-operator.observeMode" (list .Values.controller.metricsObservation .Values.controller.enableMetricsObservation) }}
{{- if eq . "legacy-on" }}
- --enable-metrics-observation
{{- else }}
- --enable-metrics-observation={{ . }}
{{- end }}
{{- end }}
{{- if .Values.controller.interfaceNameMatchDiscovery }}
- --interface-name-match-discovery
{{- end }}
{{- /* Kernel evidence follows the managed node agent unless set on or "off". */}}
{{- with .Values.controller.kernelEvidence }}
{{- if include "pacto-operator.kernelEvidence" $ }}
- --enable-kernel-evidence
{{- if .namespace }}
- --kernel-evidence-namespace={{ .namespace }}
{{- end }}
{{- if .staleness }}
- --kernel-evidence-staleness={{ .staleness }}
{{- end }}
{{- range .callerIgnoreNamespaces }}
- --caller-coverage-ignore-namespace={{ . }}
{{- end }}
{{- end }}
{{- end }}
{{- /* Always passed: a disabled agent is pruned from its namespace, and kernel evidence defaults to it. */}}
- --node-agent-namespace={{ include "pacto-operator.nodeAgentNamespace" . }}
{{- with .Values.nodeAgent }}
{{- if .enabled }}
- --enable-node-agent
{{- with .image }}
- --node-agent-image={{ . }}
{{- end }}
{{- range include "pacto-operator.imagePullSecrets.list" (dict "ctx" $ "values" $.Values) | fromYamlArray }}
- --node-agent-image-pull-secret={{ .name }}
{{- end }}
{{- if .publishInterval }}
- --node-agent-publish-interval={{ .publishInterval }}
{{- end }}
{{- with .resources }}
{{- if .requests }}
{{- if .requests.cpu }}
- --node-agent-cpu-request={{ .requests.cpu }}
{{- end }}
{{- if .requests.memory }}
- --node-agent-memory-request={{ .requests.memory }}
{{- end }}
{{- end }}
{{- if .limits }}
{{- if .limits.cpu }}
- --node-agent-cpu-limit={{ .limits.cpu }}
{{- end }}
{{- if .limits.memory }}
- --node-agent-memory-limit={{ .limits.memory }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}
{{- if .Values.dashboard.enabled }}
- --enable-dashboard
{{- if .Values.dashboard.ociSecrets }}
- --dashboard-oci-secrets={{ join "," .Values.dashboard.ociSecrets }}
{{- end }}
{{- with .Values.dashboard.resources }}
{{- if .requests }}
{{- if .requests.cpu }}
- --dashboard-cpu-request={{ .requests.cpu }}
{{- end }}
{{- if .requests.memory }}
- --dashboard-memory-request={{ .requests.memory }}
{{- end }}
{{- end }}
{{- if .limits }}
{{- if .limits.cpu }}
- --dashboard-cpu-limit={{ .limits.cpu }}
{{- end }}
{{- if .limits.memory }}
- --dashboard-memory-limit={{ .limits.memory }}
{{- end }}
{{- end }}
{{- end }}
{{- with .Values.dashboard.auth }}
{{- with .oidc }}
{{- if .issuerURL }}
- --dashboard-oidc-issuer-url={{ .issuerURL }}
- --dashboard-oidc-client-id={{ required "dashboard.auth.oidc.clientID is required with an issuerURL" .clientID }}
- --dashboard-oidc-redirect-url={{ required "dashboard.auth.oidc.redirectURL is required with an issuerURL" .redirectURL }}
{{- with .internalIssuerURL }}
- --dashboard-oidc-internal-issuer-url={{ . }}
{{- end }}
{{- with .displayName }}
- {{ printf "--dashboard-oidc-display-name=%s" . | quote }}
{{- end }}
{{- with .caBundle }}
{{- if and .configMap .secret }}
{{- fail "dashboard.auth.oidc.caBundle takes a configMap or a secret, not both" }}
{{- end }}
{{- if or .configMap .secret }}
{{- with .configMap }}
- --dashboard-oidc-ca-configmap={{ . }}
{{- end }}
{{- with .secret }}
- --dashboard-oidc-ca-secret={{ . }}
{{- end }}
- --dashboard-oidc-ca-key={{ .key | default "ca.crt" }}
{{- end }}
{{- end }}
{{- with .extraScopes }}
- --dashboard-oidc-extra-scopes={{ join "," . }}
{{- end }}
{{- with .clientSecretRef }}
{{- if .name }}
- --dashboard-oidc-client-secret-name={{ .name }}
- --dashboard-oidc-client-secret-key={{ .key | default "client-secret" }}
{{- end }}
{{- end }}
{{- with .usernameClaim }}
- {{ printf "--dashboard-oidc-username-claim=%s" . | quote }}
{{- end }}
{{- with .usernamePrefix }}
- {{ printf "--dashboard-oidc-username-prefix=%s" . | quote }}
{{- end }}
{{- with .groupsClaim }}
- {{ printf "--dashboard-oidc-groups-claim=%s" . | quote }}
{{- end }}
{{- with .groupsPrefix }}
- {{ printf "--dashboard-oidc-groups-prefix=%s" . | quote }}
{{- end }}
{{- end }}
{{- /* The signing client: a second client at the same issuer, never the sign-in client. */}}
{{- $oidc := . }}
{{- with .signClientID }}
{{- if not $oidc.issuerURL }}
{{- fail "dashboard.auth.oidc.signClientID needs dashboard.auth.oidc.issuerURL: the signing login runs at the sign-in issuer" }}
{{- end }}
{{- if eq . (toString $oidc.clientID) }}
{{- fail "dashboard.auth.oidc.signClientID must differ from dashboard.auth.oidc.clientID: the session's token must never reach Fulcio" }}
{{- end }}
- --dashboard-sign-client-id={{ . }}
- --dashboard-sign-redirect-url={{ required "dashboard.auth.oidc.signRedirectURL is required with a signClientID" $oidc.signRedirectURL }}
{{- with $oidc.signClientSecretRef }}
{{- if .name }}
- --dashboard-sign-client-secret-name={{ .name }}
- --dashboard-sign-client-secret-key={{ .key | default "client-secret" }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}
{{- with .session }}
{{- with .idleTimeout }}
- --dashboard-session-idle-timeout={{ . }}
{{- end }}
{{- with .maxAge }}
- --dashboard-session-max-age={{ . }}
{{- end }}
{{- end }}
{{- end }}
{{- if include "pacto-operator.dashboardTokenSignIn" . }}
- --dashboard-token-login
{{- end }}
{{- if include "pacto-operator.dashboardAnonymous" . }}
- --dashboard-allow-anonymous
{{- end }}
{{- with .Values.dashboard.repositories }}
- --dashboard-repositories={{ join "," . }}
{{- end }}
{{- with .Values.dashboard.externalCatalog }}
- --dashboard-external-catalog={{ . }}
{{- end }}
{{- if include "pacto-operator.fingerprints" . }}
- --dashboard-fingerprints-configmap={{ include "pacto-operator.fullname" . }}-fingerprints
{{- end }}
{{- with .Values.dashboard.delivery }}
{{- with include "pacto-operator.dashboardDeliveryNamespace" $ }}
- --dashboard-delivery-namespace={{ . }}
{{- end }}
{{- with .trust }}
{{- if .configMap }}
- --dashboard-delivery-trust-configmap={{ .configMap }}
- --dashboard-delivery-trust-key={{ .key | default "trust.yaml" }}
{{- end }}
{{- end }}
{{- with .miraURL }}
- --dashboard-mira-url={{ . }}
{{- end }}
{{- end }}
{{- with (($.Values.dashboard.stats | default dict).registry) }}
- --dashboard-stats-registry={{ . }}
{{- end }}
{{- /* Declare in the dashboard: the registry it publishes to, its own push Secret and the Sigstore it signs with. */}}
{{- with .Values.registry }}
- --dashboard-registry={{ . }}
{{- end }}
{{- with ((($.Values.dashboard.declare | default dict).registryCredentials | default dict).secretName) }}
{{- if not $.Values.registry }}
{{- fail "dashboard.declare.registryCredentials needs registry: the dashboard pushes declarations only there" }}
{{- end }}
- --dashboard-declare-registry-secret={{ . }}
{{- end }}
{{- with (($.Values.declare | default dict).signing | default dict) }}
{{- with .fulcioURL }}
- --dashboard-fulcio-url={{ . }}
{{- end }}
{{- with .rekorURL }}
- --dashboard-rekor-url={{ . }}
{{- end }}
{{- with .timestampURL }}
- --dashboard-timestamp-url={{ . }}
{{- end }}
{{- end }}
{{- /* The dashboard verifies what it signs with the root the owner check uses: declare.trustedRoot, else delivery.publishers.trustedRoot. */}}
{{- if include "pacto-operator.dashboardTrustedRoot" . }}
- --dashboard-trusted-root-configmap={{ include "pacto-operator.ownerPolicy.name" . }}
{{- end }}
{{- with .Values.dashboard.observation }}
{{- range .sources }}
{{- $backing := "" }}
{{- if .existingClaim }}{{ $backing = printf "existingClaim=%s" .existingClaim }}{{ else if .configMap }}{{ $backing = printf "configMap=%s" .configMap }}{{ end }}
- --dashboard-trace-source=name={{ required "each dashboard.observation.sources entry needs a name" .name }},file={{ required "each dashboard.observation.sources entry needs a file" .file }},{{ required "each dashboard.observation.sources entry needs exactly one of existingClaim or configMap" $backing }}
{{- end }}
{{- end }}
{{- end }}
{{- if .Values.evidence.enabled }}
- --enable-evidence-server
{{- if .Values.evidence.trust.existingSecret }}
- --evidence-trust-secret={{ .Values.evidence.trust.existingSecret }}
{{- end }}
{{- with .Values.evidence.trust.configKey }}
- --evidence-trust-config-key={{ . }}
{{- end }}
{{- range (required "evidence.registry.subjects is required when evidence is enabled: the registry holding those contract revisions IS the evidence store" .Values.evidence.registry.subjects) }}
- --evidence-subject={{ . }}
{{- end }}
{{- if .Values.evidence.registry.credentialsSecret }}
- --evidence-credentials-secret={{ .Values.evidence.registry.credentialsSecret }}
{{- end }}
{{- with .Values.evidence.resources }}
{{- if .requests }}
{{- if .requests.cpu }}
- --evidence-cpu-request={{ .requests.cpu }}
{{- end }}
{{- if .requests.memory }}
- --evidence-memory-request={{ .requests.memory }}
{{- end }}
{{- end }}
{{- if .limits }}
{{- if .limits.cpu }}
- --evidence-cpu-limit={{ .limits.cpu }}
{{- end }}
{{- if .limits.memory }}
- --evidence-memory-limit={{ .limits.memory }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}

{{/*
The node agent namespace. The operator labels it privileged for Pod Security,
so it may never be the release namespace.
*/}}
{{- define "pacto-operator.nodeAgentNamespace" -}}
{{- if and .Values.nodeAgent.enabled (eq .Values.nodeAgent.namespace .Release.Namespace) }}
{{- fail "nodeAgent.namespace must not be the release namespace: the node agent namespace is labelled privileged for Pod Security" }}
{{- end }}
{{- .Values.nodeAgent.namespace }}
{{- end }}

{{/*
Whether kernel evidence is on: on or true turns it on, "off" refuses it, and
anything else (auto, false, absent) follows nodeAgent.enabled. false keeps its
6.x meaning so an upgrade that reuses values renders the same arguments.
*/}}
{{- define "pacto-operator.kernelEvidence" -}}
{{- $v := .Values.controller.kernelEvidence.enabled }}
{{- $s := ternary "" (toString $v) (kindIs "invalid" $v) }}
{{- if or (eq $s "on") (eq $s "true") }}true
{{- else if and (ne $s "off") .Values.nodeAgent.enabled }}true
{{- end }}
{{- end }}

{{/*
The mode of one observation feature (probing, metrics observation), from
(list $new $legacy): the auto/on/off key and the deprecated boolean it replaces.
Returns on, off, auto, legacy-on (the bare flag) or "" (no argument).
An upgrade that reuses a 6.x release's values keeps that release's chart
defaults, so the new key is absent and the legacy boolean present: an absent or
empty new key renders exactly what the legacy key rendered (true -> legacy-on,
false -> nothing), and with neither key set it is auto. An explicit new key
always wins. Values files parse as YAML 1.1, so a bare on or off arrives as a
bool: absence is tested with kindIs and every compare runs on toString.
510-S7's profile extends this define.
*/}}
{{- define "pacto-operator.observeMode" -}}
{{- $new := index . 0 }}
{{- $legacy := index . 1 }}
{{- $s := ternary "" (toString $new) (kindIs "invalid" $new) }}
{{- if eq $s "" }}
{{- if kindIs "invalid" $legacy }}auto
{{- else if $legacy }}legacy-on
{{- end }}
{{- else if or (eq $s "on") (eq $s "true") }}on
{{- else if or (eq $s "off") (eq $s "false") }}off
{{- else if eq $s "auto" }}auto
{{- end }}
{{- end }}

{{/*
A container's image and pull policy, from an image block (registry,
repository, tag, digest, pullPolicy): (dict "ctx" $ "image" .Values.image).
The registry defaults to global.imageRegistry, and with neither set the
repository is the whole reference. A repository that already starts with a
registry host (a first path segment holding a "." or ":" or equal to
"localhost", as values from before global.imageRegistry carry) is the whole
reference too. The tag defaults to the chart appVersion, and a digest pins
the image as repository:tag@digest.
*/}}
{{- define "pacto-operator.image" -}}
{{- $img := .image -}}
{{- $ref := printf "%s:%s" $img.repository ($img.tag | default .ctx.Chart.AppVersion) -}}
{{- $first := index (splitList "/" $img.repository) 0 -}}
{{- $hasHost := and (contains "/" $img.repository) (or (contains "." $first) (contains ":" $first) (eq $first "localhost")) -}}
{{- if not $hasHost }}{{ with ($img.registry | default .ctx.Values.global.imageRegistry) }}{{ $ref = printf "%s/%s" . $ref }}{{ end }}{{ end -}}
{{- with $img.digest }}{{ $ref = printf "%s@%s" $ref . }}{{ end -}}
image: {{ $ref | quote }}
imagePullPolicy: {{ $img.pullPolicy }}
{{- end }}

{{/*
The image pull secrets of a component: global.imagePullSecrets, then the
component's own. (dict "ctx" $ "values" .Values.delivery)
*/}}
{{- define "pacto-operator.imagePullSecrets.list" -}}
{{- concat (.ctx.Values.global.imagePullSecrets | default list) (.values.imagePullSecrets | default list) | uniq | toYaml }}
{{- end }}

{{/*
A pod template's metadata: the component's labels, its podLabels and
podAnnotations, and any annotations the chart sets itself, which win.
(dict "ctx" $ "values" .Values.delivery "labels" (include ...) "annotations" (dict ...))
*/}}
{{- define "pacto-operator.podMetadata" -}}
{{- with merge (dict) (.annotations | default dict) (.values.podAnnotations | default dict) }}
annotations:
  {{- toYaml . | nindent 2 }}
{{- end }}
labels:
  {{- .labels | nindent 2 }}
  {{- with .values.podLabels }}
  {{- toYaml . | nindent 2 }}
  {{- end }}
{{- end }}

{{/*
The head of a pod spec every chart workload shares: pull secrets,
ServiceAccount, the pod securityContext (the component's, or
global.podSecurityContext) and the grace period.
(dict "ctx" $ "values" .Values.delivery "serviceAccountName" "...")
*/}}
{{- define "pacto-operator.podSpec" -}}
{{- with include "pacto-operator.imagePullSecrets.list" . | fromYamlArray }}
imagePullSecrets:
  {{- toYaml . | nindent 2 }}
{{- end }}
serviceAccountName: {{ .serviceAccountName }}
securityContext:
  {{- toYaml (.values.podSecurityContext | default .ctx.Values.global.podSecurityContext) | nindent 2 }}
terminationGracePeriodSeconds: 10
{{- end }}

{{/*
A container's securityContext: the component's, or global.securityContext.
(dict "ctx" $ "values" .Values.delivery)
*/}}
{{- define "pacto-operator.securityContext" -}}
securityContext:
  {{- toYaml (.values.securityContext | default .ctx.Values.global.securityContext) | nindent 2 }}
{{- end }}

{{/*
The health and metrics ports every chart container serves.
*/}}
{{- define "pacto-operator.ports" -}}
ports:
  {{- if .metrics }}
  - name: metrics
    containerPort: {{ .metrics }}
    protocol: TCP
  {{- end }}
  - name: health
    containerPort: 8081
    protocol: TCP
{{- end }}

{{/*
The liveness and readiness probes on the health port. A dict may set
readinessPeriod (default 10) and readinessTimeout. The liveness probe allows
5s per answer and restarts after 2 minutes of failures, so a busy node slows
the process down without getting it killed.
*/}}
{{- define "pacto-operator.probes" -}}
livenessProbe:
  httpGet:
    path: /healthz
    port: health
  initialDelaySeconds: 15
  periodSeconds: 20
  timeoutSeconds: 5
  failureThreshold: 6
readinessProbe:
  httpGet:
    path: /readyz
    port: health
  initialDelaySeconds: 5
  periodSeconds: {{ .readinessPeriod | default 10 }}
  {{- with .readinessTimeout }}
  timeoutSeconds: {{ . }}
  {{- end }}
{{- end }}

{{/*
A pod's scheduling: nodeSelector, affinity and tolerations from a component's
values.
*/}}
{{- define "pacto-operator.scheduling" -}}
{{- with .nodeSelector }}
nodeSelector:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .affinity }}
affinity:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .tolerations }}
tolerations:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}

{{/*
The cluster's identity: the kube-system Namespace, whose UID names the
cluster in operations and records.
*/}}
{{- define "pacto-operator.rbac.clusterIdentityRule" -}}
- apiGroups: [""]
  resources: [namespaces]
  resourceNames: [kube-system]
  verbs: [get]
{{- end }}

{{/*
The namespaces the controller and the managed dashboard watch, as a YAML list:
controller.watchNamespaces with the deprecated controller.watchNamespace merged
in, empties dropped, sorted. The default list guards keep a --reuse-values
upgrade from a release without the key rendering. Empty means cluster-wide.
*/}}
{{- define "pacto-operator.watchNamespaces" -}}
{{- $list := concat (default list .Values.controller.watchNamespaces) (list .Values.controller.watchNamespace) | compact | uniq | sortAlpha }}
{{- range $list }}
{{- if eq . $.Values.nodeAgent.namespace }}
{{- fail (printf "controller.watchNamespaces must not hold the node agent namespace %s: the operator already holds its own rules there" .) }}
{{- end }}
{{- end }}
{{- toYaml $list }}
{{- end }}

{{/*
Every repository prefix a Pacto component may push under, as a YAML list: the
installation repositories <registry>/pacto-targets, pacto-index and
pacto-inferred when registry is set, then, with delivery on, the recorder's
streams and approvals repositories and the promoter's stats repository, as
set (an explicit delivery value may name any repository).
*/}}
{{- define "pacto-operator.pushPrefixes" -}}
{{- $out := list }}
{{- with .Values.registry }}
{{- range list "pacto-targets" "pacto-index" "pacto-inferred" }}
{{- $out = append $out (printf "%s/%s" $.Values.registry .) }}
{{- end }}
{{- end }}
{{- if .Values.delivery.enabled }}
{{- if .Values.delivery.recorder.enabled }}
{{- with include "pacto-operator.delivery.streamsRepository" . }}
{{- $out = append $out . }}
{{- $out = append $out (include "pacto-operator.delivery.approvalsRepository" $) }}
{{- end }}
{{- end }}
{{- with .Values.delivery.stats.repository }}
{{- $out = append $out . }}
{{- end }}
{{- end }}
{{- toYaml (uniq $out) }}
{{- end }}

{{/*
The cloud that owns a registry host, the same host predicates pkg/oci puts in
front of its cloud sources: google (*.pkg.dev, gcr.io, *.gcr.io), aws (ECR and
public.ecr.aws), azure (*.azurecr.io, .cn, .us), else empty.
*/}}
{{- define "pacto-operator.registryHostCloud" -}}
{{- $h := lower . }}
{{- if regexMatch `^(([a-z0-9-]+\.)+pkg\.dev|([a-z0-9-]+\.)*gcr\.io)$` $h }}google
{{- else if regexMatch `^([0-9]{12}\.dkr\.ecr(-fips)?\.[a-z0-9-]+\.amazonaws\.com(\.cn)?|public\.ecr\.aws)$` $h }}aws
{{- else if regexMatch `^([a-z0-9-]+\.)+azurecr\.(io|cn|us)$` $h }}azure
{{- end }}
{{- end }}

{{/*
The NOTES line that says which credential reaches the installation registry,
in order: registryPullSecret; the operator ServiceAccount annotation of the
cloud that owns the registry host (an annotation for another cloud does not
count); on upgrade, a detected cloud that owns the host; else none. Takes a
dict: root, detected (the decoded detection, possibly empty) and pushed (the
detection holds a last push result).
*/}}
{{- define "pacto-operator.registryCredential" -}}
{{- $root := .root }}
{{- $host := index (splitList "/" $root.Values.registry) 0 }}
{{- $cloud := include "pacto-operator.registryHostCloud" $host }}
{{- $keys := dict "google" "iam.gke.io/gcp-service-account" "aws" "eks.amazonaws.com/role-arn" "azure" "azure.workload.identity/client-id" }}
{{- $names := dict "google" "GKE workload identity" "aws" "EKS IRSA" "azure" "Azure workload identity" }}
{{- $clouds := dict "GKE" "google" "GCE" "google" "EKS" "aws" "AWS" "aws" "AKS" "azure" "Azure" "azure" }}
{{- $id := "" }}
{{- if $cloud }}{{ $id = get ($root.Values.serviceAccount.annotations | default dict) (get $keys $cloud) | toString }}{{ end }}
{{- $seen := "" }}
{{- $entries := get .detected "entries" | default list }}
{{- if kindIs "slice" $entries }}{{ else }}{{ $entries = list }}{{ end }}
{{- range $entries }}
{{- if and $cloud (kindIs "map" .) (eq (toString (get . "kind")) "cloud") (eq (get $clouds (toString (get . "value"))) $cloud) }}{{ $seen = toString (get . "value") }}{{ end }}
{{- end }}
{{- if $root.Values.registryPullSecret -}}
Pulls: Secret {{ $root.Values.registryPullSecret }} (registryPullSecret).{{ if not .pushed }} Pushes: unconfirmed until the first snapshot push.{{ end }}
{{- else if $id -}}
Pulls and pushes: {{ get $names $cloud }}{{ if ne $cloud "azure" }} ({{ $id }}){{ end }}. No Secret needed.
{{- else if $seen -}}
Pulls and pushes: {{ $seen }} workload identity once ServiceAccount {{ $root.Release.Namespace }}/{{ include "pacto-operator.serviceAccountName" $root }} is bound to an identity with registry access{{ if eq $cloud "aws" }} (EKS Pod Identity needs no annotation){{ end }}.
{{- else -}}
No credential found for {{ $host }}: set registryPullSecret, or the registry must allow anonymous pull and push.
{{- end }}
{{- end }}

{{/*
The recorder's streams repository: delivery.recorder.streamsRepository, else
<registry>/pacto-streams when registry is set, else empty.
*/}}
{{- define "pacto-operator.delivery.streamsRepository" -}}
{{- .Values.delivery.recorder.streamsRepository | default (ternary (printf "%s/pacto-streams" .Values.registry) "" (not (empty .Values.registry))) }}
{{- end }}

{{/*
The repository a person's client pushes signed approvals to:
delivery.recorder.approvalsRepository, else <streams repository>-approvals.
*/}}
{{- define "pacto-operator.delivery.approvalsRepository" -}}
{{- .Values.delivery.recorder.approvalsRepository | default (printf "%s-approvals" (include "pacto-operator.delivery.streamsRepository" .)) }}
{{- end }}

{{/*
The Secrets granted by name, as a YAML list of {namespace, name}, from
controller.secretGrants and registryPullSecret (both namespace/name).
*/}}
{{- define "pacto-operator.secretGrants" -}}
{{- $out := list }}
{{- range concat (default list .Values.controller.secretGrants) (compact (list .Values.registryPullSecret)) | uniq }}
{{- $parts := splitList "/" . }}
{{- $out = append $out (dict "namespace" (index $parts 0) "name" (index $parts 1)) }}
{{- end }}
{{- toYaml $out }}
{{- end }}

{{/*
The namespace the managed dashboard reads delivery objects in, or nothing:
dashboard.delivery.namespace, else delivery.namespace when delivery is on.
*/}}
{{- define "pacto-operator.dashboardDeliveryNamespace" -}}
{{- with .Values.dashboard.delivery }}
{{- .namespace | default (ternary $.Values.delivery.namespace "" $.Values.delivery.enabled) }}
{{- end }}
{{- end }}

{{/*
Token sign-in, unless dashboard.auth.tokenSignIn.enabled is explicitly false.
The key is newer than every earlier release, so an upgrade that reuses their
values never carries it and signs people in. The deprecated key it replaces is
ignored: a reused release holds it as false.
*/}}
{{- define "pacto-operator.dashboardTokenSignIn" -}}
{{- if ne (toString (((.Values.dashboard.auth | default dict).tokenSignIn | default dict).enabled)) "false" }}true{{ end }}
{{- end }}

{{/*
Anonymous access: only with allowAnonymous explicitly true, token sign-in off
and no issuer. NOTES.txt reads the same rule for its warning.
*/}}
{{- define "pacto-operator.dashboardAnonymous" -}}
{{- $auth := .Values.dashboard.auth | default dict }}
{{- if and (eq (toString $auth.allowAnonymous) "true") (not (include "pacto-operator.dashboardTokenSignIn" .)) (not ($auth.oidc | default dict).issuerURL) }}true{{ end }}
{{- end }}

{{/*
Whether a cluster-scoped need remains, so the ClusterRole renders: no
namespace list, the node agent, or the dashboard with delivery on.
*/}}
{{- define "pacto-operator.clusterScoped" -}}
{{- if or (not (include "pacto-operator.watchNamespaces" . | fromYamlArray)) .Values.nodeAgent.enabled (and .Values.dashboard.enabled (include "pacto-operator.dashboardDeliveryNamespace" .)) }}true{{ end }}
{{- end }}

{{/*
Why the operator may read the recorder's Ed25519 key Secret, or nothing: the
delivery namespace is the release namespace, is watched, or the key is
granted by name.
*/}}
{{- define "pacto-operator.delivery.operatorReadsKey" -}}
{{- $ns := .Values.delivery.namespace }}
{{- $name := .Values.delivery.recorder.signing.ed25519.secretName }}
{{- if eq $ns .Release.Namespace }}
{{- printf "%s is the release namespace" $ns }}
{{- else if has $ns (include "pacto-operator.watchNamespaces" . | fromYamlArray) }}
{{- printf "%s is in controller.watchNamespaces" $ns }}
{{- else }}
{{- range include "pacto-operator.secretGrants" . | fromYamlArray }}
{{- if and (eq .namespace $ns) (eq .name $name) }}
{{- printf "controller.secretGrants names %s/%s" $ns $name }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}

{{/*
The observation RBAC switches: "metricsMonitors" when metrics observation is
explicitly on (ServiceMonitor and PodMonitor reads), "namespaceGet" when probing
or metrics is auto (the target namespace's pacto.trianalab.io/observe label,
read by name). Returns a YAML dict.
*/}}
{{- define "pacto-operator.observeRBAC" -}}
{{- $probing := include "pacto-operator.observeMode" (list .Values.controller.probing .Values.controller.enableProbing) }}
{{- $metrics := include "pacto-operator.observeMode" (list .Values.controller.metricsObservation .Values.controller.enableMetricsObservation) }}
metricsMonitors: {{ has $metrics (list "on" "legacy-on") }}
namespaceGet: {{ or (eq $probing "auto") (eq $metrics "auto") }}
{{- end }}

{{/*
Whether the cluster serves an API at install or upgrade: (list $ "<group/version[/Kind]>")
renders "true" or nothing. helm template sees only what --api-versions names.
*/}}
{{- define "pacto-operator.apiServed" -}}
{{- if (index . 0).Capabilities.APIVersions.Has (index . 1) }}true{{- end }}
{{- end }}

{{/*
The installation's workload manager and promoter fingerprints as the YAML the
dashboard and the promoter read with --fingerprints; empty when both lists are.
*/}}
{{- define "pacto-operator.fingerprints" -}}
{{- with .Values.fingerprints }}
{{- if or .managers .promoters }}
{{- toYaml (dict "managers" (.managers | default list) "promoters" (.promoters | default list)) }}
{{- end }}
{{- end }}
{{- end }}
