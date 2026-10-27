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
{{- if .Values.controller.watchNamespace }}
- --watch-namespace={{ .Values.controller.watchNamespace }}
{{- end }}
{{- if .Values.controller.stabilizationWindow }}
- --stabilization-window={{ .Values.controller.stabilizationWindow }}
{{- end }}
{{- if .Values.controller.enableProbing }}
- --enable-probing
{{- end }}
{{- if .Values.controller.enableMetricsObservation }}
- --enable-metrics-observation
{{- end }}
{{- /* Kernel evidence follows the managed node agent: either switch turns it on. */}}
{{- with .Values.controller.kernelEvidence }}
{{- if or .enabled $.Values.nodeAgent.enabled }}
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
{{- end }}
{{- with .session }}
{{- with .idleTimeout }}
- --dashboard-session-idle-timeout={{ . }}
{{- end }}
{{- with .maxAge }}
- --dashboard-session-max-age={{ . }}
{{- end }}
{{- end }}
{{- if (.tokenLogin | default dict).enabled }}
- --dashboard-token-login
{{- end }}
{{- with (.deliveryNamespace | default (ternary $.Values.delivery.namespace "" $.Values.delivery.enabled)) }}
- --dashboard-delivery-namespace={{ . }}
{{- end }}
{{- end }}
{{- with .Values.dashboard.miraURL }}
- --dashboard-mira-url={{ . }}
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
The namespace the operator reads kernel evidence (NodeRuntimeObservations and
heartbeat Leases) from.
*/}}
{{- define "pacto-operator.kernelEvidenceNamespace" -}}
{{- .Values.controller.kernelEvidence.namespace | default (include "pacto-operator.nodeAgentNamespace" .) }}
{{- end }}

{{/*
Whether kernel evidence is on: either switch turns it on.
*/}}
{{- define "pacto-operator.kernelEvidence" -}}
{{- if or .Values.controller.kernelEvidence.enabled .Values.nodeAgent.enabled }}true{{ end }}
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
