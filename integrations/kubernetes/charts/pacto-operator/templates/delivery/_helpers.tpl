{{/*
The delivery objects' base name: the operator's fullname plus -delivery, so
the Deployment, ServiceAccount, roles and policies never collide with the
operator's own.
*/}}
{{- define "pacto-operator.delivery.fullname" -}}
{{- printf "%s-delivery" (include "pacto-operator.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Labels and selector labels of the delivery objects: the common ones with
app.kubernetes.io/name <name>-delivery, never the operator's bare name, so the
operator's Services and ServiceMonitor select none of the delivery pods. Each
takes the root context, or (dict "ctx" $ "component" "delivery-recorder").
*/}}
{{- define "pacto-operator.delivery.labels" -}}
{{- include "pacto-operator.labels" (dict "ctx" (.ctx | default .) "part" "delivery" "component" .component) }}
{{- end }}

{{- define "pacto-operator.delivery.selectorLabels" -}}
{{- include "pacto-operator.selectorLabels" (dict "ctx" (.ctx | default .) "part" "delivery" "component" .component) }}
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
{{- include "pacto-operator.serviceAccountName.of" (list .Values.delivery.serviceAccount (include "pacto-operator.delivery.fullname" .)) }}
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
{{- include "pacto-operator.serviceAccountName.of" (list .Values.delivery.recorder.serviceAccount (include "pacto-operator.delivery.recorderName" .)) }}
{{- end }}

{{/*
The recorder's base name: its Deployment, roles, ConfigMaps and metrics.
*/}}
{{- define "pacto-operator.delivery.recorderName" -}}
{{- printf "%s-recorder" (include "pacto-operator.delivery.fullname" .) }}
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
{{- $p := dict "agentGroups" $i.agentGroups "breakGlassGroups" $i.breakGlassGroups "teamGroupPrefix" $i.teamGroupPrefix "requireOwnerTeam" $i.requireOwnerTeam "humanGroups" ($i.humanGroups | default list) -}}
{{- /* Each gate provider's type, never its URL or signers: a client plans with them. */ -}}
{{- $types := dict -}}
{{- range .Values.delivery.gates.providers }}{{ $_ := set $types .name .type }}{{ end -}}
{{- if $types }}{{ $_ := set $p "gateProviders" $types }}{{ end -}}
{{- $p | toJson -}}
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

{{/*
Read access to what a Stage points at: the OCIRepositories, and the Argo CD
Applications with delivery.argocd.enabled.
*/}}
{{- define "pacto-operator.delivery.rbac.pointerReadRules" -}}
- apiGroups: [source.toolkit.fluxcd.io]
  resources: [ocirepositories]
  verbs: [get]
{{- if .Values.delivery.argocd.enabled }}
- apiGroups: [argoproj.io]
  resources: [applications]
  verbs: [get]
{{- end }}
{{- end }}

{{/*
The cluster's identity: the kube-system Namespace, whose UID names the
cluster in operations and records.
*/}}
{{- define "pacto-operator.delivery.rbac.clusterIdentityRule" -}}
- apiGroups: [""]
  resources: [namespaces]
  resourceNames: [kube-system]
  verbs: [get]
{{- end }}

{{/*
What an agent may do: propose, read and cancel operations, read Stages and
approvals and what they point at, and read the Pactos and Kustomizations
planning needs. (dict "ctx" $ "approver" true) adds creating Approvals.
*/}}
{{- define "pacto-operator.delivery.rbac.agentRules" -}}
- apiGroups: [delivery.pacto.trianalab.io]
  resources: [operations]
  verbs: [get, list, watch, create, delete]
{{- if .approver }}
- apiGroups: [delivery.pacto.trianalab.io]
  resources: [approvals]
  verbs: [get, list, watch, create]
- apiGroups: [delivery.pacto.trianalab.io]
  resources: [stages]
  verbs: [get, list, watch]
{{- else }}
- apiGroups: [delivery.pacto.trianalab.io]
  resources: [stages, approvals]
  verbs: [get, list, watch]
{{- end }}
{{ include "pacto-operator.delivery.rbac.pointerReadRules" .ctx }}
- apiGroups: [kustomize.toolkit.fluxcd.io]
  resources: [kustomizations]
  verbs: [list]
- apiGroups: [pacto.trianalab.io]
  resources: [pactos]
  verbs: [get, list]
{{- end }}
