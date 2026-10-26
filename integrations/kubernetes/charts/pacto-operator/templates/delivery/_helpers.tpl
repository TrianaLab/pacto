{{/*
The delivery objects' base name: the operator's fullname plus -delivery, so
the Deployment, ServiceAccount, roles and policies never collide with the
operator's own.
*/}}
{{- define "pacto-operator.delivery.fullname" -}}
{{- printf "%s-delivery" (include "pacto-operator.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels of the delivery objects. They never carry the operator's bare
selector labels, so the operator's Services and ServiceMonitor select none of
the delivery pods.
*/}}
{{- define "pacto-operator.delivery.labels" -}}
helm.sh/chart: {{ include "pacto-operator.chart" . }}
{{ include "pacto-operator.delivery.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels of the delivery objects.
*/}}
{{- define "pacto-operator.delivery.selectorLabels" -}}
app.kubernetes.io/name: {{ include "pacto-operator.name" . }}-delivery
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Refuse to render delivery where the admission policies cannot run: the
promoter trusts nothing an operation or approval says about who wrote it
without them. ValidatingAdmissionPolicy is GA from Kubernetes 1.30. Only
delivery.admissionPolicies.allowUnguarded skips the check.
*/}}
{{- define "pacto-operator.delivery.gate" -}}
{{- if not .Values.delivery.admissionPolicies.allowUnguarded }}
{{- if not (semverCompare ">=1.30.0-0" .Capabilities.KubeVersion.Version) }}
{{- fail (printf "delivery.enabled needs Kubernetes 1.30 or later for its admission policies, and this cluster is %s. Upgrade the cluster, or set delivery.admissionPolicies.allowUnguarded=true to install without that guard" .Capabilities.KubeVersion.Version) }}
{{- end }}
{{- if not (.Capabilities.APIVersions.Has "admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy") }}
{{- fail "delivery.enabled needs the admissionregistration.k8s.io/v1 ValidatingAdmissionPolicy API, and this cluster does not serve it. Enable it, or set delivery.admissionPolicies.allowUnguarded=true to install without that guard (with helm template, pass --api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy)" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
The promoter's ServiceAccount name.
*/}}
{{- define "pacto-operator.delivery.serviceAccountName" -}}
{{- if .Values.delivery.serviceAccount.create }}
{{- default (include "pacto-operator.delivery.fullname" .) .Values.delivery.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.delivery.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
The promoter's apiserver username, the one identity admission lets set
witnessed and edit the pointer.
*/}}
{{- define "pacto-operator.delivery.controllerUser" -}}
system:serviceaccount:{{ include "pacto-operator.delivery.namespace" . }}:{{ include "pacto-operator.delivery.serviceAccountName" . }}
{{- end }}

{{/*
A CEL list literal of the given strings.
*/}}
{{- define "pacto-operator.delivery.celList" -}}
[{{- range $i, $g := . }}{{ if $i }}, {{ end }}{{ $g | squote }}{{ end }}]
{{- end }}

{{/*
The recorder's ServiceAccount name.
*/}}
{{- define "pacto-operator.delivery.recorderServiceAccountName" -}}
{{- if .Values.delivery.recorder.serviceAccount.create }}
{{- default (printf "%s-recorder" (include "pacto-operator.delivery.fullname" .)) .Values.delivery.recorder.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.delivery.recorder.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
A CEL expression for one field of an object, or a default when any part of the
path is absent: celField (list "object" "status.stream.head" "''").
*/}}
{{- define "pacto-operator.delivery.celField" -}}
{{- $acc := index . 0 }}
{{- $checks := list }}
{{- range splitList "." (index . 1) }}
{{- $acc = printf "%s.%s" $acc . }}
{{- $checks = append $checks (printf "has(%s)" $acc) }}
{{- end }}
{{- printf "(%s ? %s : %s)" (join " && " $checks) $acc (index . 2) }}
{{- end }}

{{/*
A CEL expression that is true when a field is the same in object and oldObject.
*/}}
{{- define "pacto-operator.delivery.celSame" -}}
{{- printf "%s == %s" (include "pacto-operator.delivery.celField" (list "object" (index . 0) (index . 1))) (include "pacto-operator.delivery.celField" (list "oldObject" (index . 0) (index . 1))) }}
{{- end }}

{{/*
The promoter's identity policy as JSON: the one copy the promoter runs with and
every client (CLI, MCP server, dashboard) reads from the published ConfigMap.
*/}}
{{- define "pacto-operator.delivery.identitiesJSON" -}}
{{- $i := .Values.delivery.identities -}}
{{- dict "agentGroups" $i.agentGroups "breakGlassGroups" $i.breakGlassGroups "teamGroupPrefix" $i.teamGroupPrefix "requireOwnerTeam" $i.requireOwnerTeam "humanGroups" ($i.humanGroups | default list) | toJson -}}
{{- end }}

{{/*
The container image: repository:tag, pinned as repository:tag@digest when
image.digest is set. The tag defaults to the chart appVersion.
*/}}
{{- define "pacto-operator.delivery.image" -}}
{{- $ref := printf "%s:%s" .Values.delivery.image.repository (.Values.delivery.image.tag | default .Chart.AppVersion) -}}
{{- with .Values.delivery.image.digest }}{{ $ref = printf "%s@%s" $ref . }}{{ end -}}
{{- $ref -}}
{{- end }}

{{/*
The namespace delivery runs in. Never the release namespace: the operator
holds create on Deployments and Secrets there, which would let it run a pod
as the promoter or mint the promoter's token and move the pointer itself.
Never the node agent namespace either, on or off: the operator holds create on
DaemonSets, ServiceAccounts and Roles there, and the chart would render a
second Namespace of the same name.
*/}}
{{- define "pacto-operator.delivery.namespace" -}}
{{- $ns := required "delivery.namespace is required" .Values.delivery.namespace }}
{{- if eq $ns .Release.Namespace }}
{{- fail "delivery.namespace must not be the release namespace: the operator can create Deployments and Secrets there, so it could act as the promoter" }}
{{- end }}
{{- if eq $ns .Values.nodeAgent.namespace }}
{{- fail "delivery.namespace must not be the node agent namespace: the operator can create DaemonSets and ServiceAccounts there, so it could act as the promoter" }}
{{- end }}
{{- $ns }}
{{- end }}
