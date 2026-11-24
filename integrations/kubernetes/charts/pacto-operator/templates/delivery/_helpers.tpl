{{/*
Delivery's own helpers, layered on the chart's: names, labels, images, pods,
bindings and metrics come from templates/_helpers.tpl and
templates/_resources.tpl. Only what is particular to delivery lives here.
*/}}

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
promoter admits nothing without them. ValidatingAdmissionPolicy is GA from
Kubernetes 1.30. delivery.admissionPolicies.skipCapabilityCheck skips the
check for offline rendering, and the policies still render. The keys that
once let delivery run without its policies fail with their replacement.
*/}}
{{- define "pacto-operator.delivery.gate" -}}
{{- $ap := .Values.delivery.admissionPolicies }}
{{- if or (hasKey $ap "enabled") (hasKey $ap "allowUnguarded") }}
{{- fail "delivery.admissionPolicies.enabled and delivery.admissionPolicies.allowUnguarded were removed: delivery always installs its admission policies. Delete both keys from your values (with helm upgrade --reuse-values, use --reset-then-reuse-values). To skip only the Kubernetes 1.30 and ValidatingAdmissionPolicy API check, set delivery.admissionPolicies.skipCapabilityCheck=true" }}
{{- end }}
{{- if not $ap.skipCapabilityCheck }}
{{- if not (semverCompare ">=1.30.0-0" .Capabilities.KubeVersion.Version) }}
{{- fail (printf "Pacto delivery (delivery.enabled or profile idp) needs Kubernetes 1.30 or later for its admission policies, and this cluster is %s. Upgrade the cluster, or set delivery.admissionPolicies.skipCapabilityCheck=true to render for another cluster" .Capabilities.KubeVersion.Version) }}
{{- end }}
{{- if not (.Capabilities.APIVersions.Has "admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy") }}
{{- fail "Pacto delivery (delivery.enabled or profile idp) needs the admissionregistration.k8s.io/v1 ValidatingAdmissionPolicy API, and this cluster does not serve it. Enable it, or set delivery.admissionPolicies.skipCapabilityCheck=true to render for another cluster (with helm template, pass --api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy)" }}
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
{{- $p := dict "agentGroups" $i.agentGroups "breakGlassGroups" $i.breakGlassGroups "requireOwnerTeam" $i.requireOwnerTeam "humanGroups" ($i.humanGroups | default list) -}}
{{- /* Owner teams narrow who may answer only once teamGroupPrefix is set, even
to "" (the exact group T). Unset, whoever may update the Stage's workload answers. */ -}}
{{- if not (kindIs "invalid" $i.teamGroupPrefix) }}{{ $_ := set $p "teamGroupPrefix" $i.teamGroupPrefix }}{{ $_ := set $p "teamNarrowing" true }}{{ end -}}
{{- /* Each gate provider's type, never its URL or signers: a client plans with them. */ -}}
{{- $types := dict -}}
{{- range .Values.delivery.gates.providers }}{{ $_ := set $types .name .type }}{{ end -}}
{{- if $types }}{{ $_ := set $p "gateProviders" $types }}{{ end -}}
{{- /* Keyless approvers: a signer without subjects would let any address its
issuer vouches for approve, so it fails the render. */ -}}
{{- range $i.signers }}{{ if not .subjects }}{{ fail (printf "delivery.identities.signers: the signer for issuer %q needs subjects, the regular expression an approver's whole email must match" .issuer) }}{{ end }}{{ end -}}
{{- if $i.signers }}{{ $_ := set $p "signers" $i.signers }}{{ end -}}
{{- if include "pacto-operator.delivery.manifestInspector" . }}{{ $_ := set $p "manifestInspector" true }}{{ end -}}
{{- /* Under profile idp infrastructure reaches a cluster only as a dependency
on an offering: the inspector refuses raw infrastructure in a deploy artifact. */ -}}
{{- if and (include "pacto-operator.delivery.manifestInspector" .) (include "pacto-operator.idp" .) (not (include "pacto-operator.hardened" .)) }}{{ $_ := set $p "refuseRawInfra" true }}{{ end -}}
{{- $p | toJson -}}
{{- end }}

{{/*
The promoter inspects what a version applies before it writes the pointer:
"true" or nothing. true and false decide; auto, and an absent key, is on
wherever the delivery templates render.
*/}}
{{- define "pacto-operator.delivery.manifestInspector" -}}
{{- $v := dig "manifestInspector" "auto" .Values.delivery }}
{{- if eq (toString $v) "auto" }}{{ include "pacto-operator.deliveryEnabled" . }}
{{- else if $v }}true
{{- end }}
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
An applier switch: (list $ $value "<group/version/Kind>" $legacyDefault)
renders "true" or nothing. true and false decide; "auto" follows the served
API; an absent key (a release upgraded with --reuse-values from a chart
without it) keeps $legacyDefault, what that chart rendered.
*/}}
{{- define "pacto-operator.delivery.applierAPI" -}}
{{- $value := index . 1 }}
{{- if kindIs "invalid" $value }}
{{- if index . 3 }}true{{ end }}
{{- else if eq (toString $value) "auto" }}
{{- include "pacto-operator.apiServed" (list (index . 0) (index . 2)) }}
{{- else if $value }}true
{{- end }}
{{- end }}

{{/*
Stages may apply through Argo CD Applications: "true" or nothing. The
Application patch rule, --argocd and the two policies bounding the write all
follow this one helper.
*/}}
{{- define "pacto-operator.delivery.argocd" -}}
{{- include "pacto-operator.delivery.applierAPI" (list . (dig "argocd" "enabled" nil .Values.delivery) "argoproj.io/v1alpha1/Application" false) }}
{{- end }}

{{/*
Stages may apply through Flux OCIRepositories and Kustomizations: "true" or
nothing. An absent delivery.flux keeps the Flux access every earlier chart
rendered.
*/}}
{{- define "pacto-operator.delivery.flux" -}}
{{- $v := dig "flux" "enabled" nil .Values.delivery }}
{{- /* Under idp, auto is true: Flux may arrive with a later upgrade of this
release (idp.flux.install), and RBAC on a group not served yet is valid. */}}
{{- if and (include "pacto-operator.idp" .) (eq (toString $v) "auto") }}true
{{- else }}
{{- include "pacto-operator.delivery.applierAPI" (list . $v "source.toolkit.fluxcd.io/v1/OCIRepository" true) }}
{{- end }}
{{- end }}

{{/*
Stages may apply through Flux HelmReleases: "true" or nothing. On with
delivery.flux.enabled literally true, or with Flux on and the HelmRelease API
served, so a release reused without that API renders what it rendered.
*/}}
{{- define "pacto-operator.delivery.fluxHelm" -}}
{{- $v := dig "flux" "enabled" nil .Values.delivery }}
{{- if and (kindIs "bool" $v) $v }}true
{{- else if not (include "pacto-operator.delivery.flux" .) }}
{{- else if or (include "pacto-operator.idp" .) (include "pacto-operator.apiServed" (list . "helm.toolkit.fluxcd.io/v2/HelmRelease")) }}true
{{- end }}
{{- end }}

{{/*
Read access to what a Stage points at: the OCIRepositories with Flux, the
HelmReleases with HelmRelease delivery (their chart source is an
OCIRepository) and the Argo CD Applications with Argo CD.
*/}}
{{- define "pacto-operator.delivery.rbac.pointerReadRules" -}}
{{- if include "pacto-operator.delivery.flux" . }}
- apiGroups: [source.toolkit.fluxcd.io]
  resources: [ocirepositories]
  verbs: [get]
{{- end }}
{{- if include "pacto-operator.delivery.fluxHelm" . }}
- apiGroups: [helm.toolkit.fluxcd.io]
  resources: [helmreleases]
  verbs: [get]
{{- end }}
{{- if include "pacto-operator.delivery.argocd" . }}
- apiGroups: [argoproj.io]
  resources: [applications]
  verbs: [get]
{{- end }}
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
{{- include "pacto-operator.delivery.rbac.pointerReadRules" .ctx }}
{{- if include "pacto-operator.delivery.flux" .ctx }}
- apiGroups: [kustomize.toolkit.fluxcd.io]
  resources: [kustomizations]
  verbs: [list]
{{- end }}
- apiGroups: [pacto.trianalab.io]
  resources: [pactos]
  verbs: [get, list]
{{- end }}

{{/*
The trust config the promoter and clients verify publisher pins with: this
cluster's recorder, by its keyless identity or its public key, for the pin
streams (pins/<domain>/<name>) only. Empty when the recorder is off, when no
delivery.publishers block exists (reused 6.x values) or when an Ed25519 or
KMS recorder names no public key.
*/}}
{{- define "pacto-operator.delivery.pinSigner" -}}
{{- $r := .Values.delivery.recorder }}
{{- if and (include "pacto-operator.recorderEnabled" .) (kindIs "map" .Values.delivery.publishers) }}
{{- $k := $r.signing.keyless }}
{{- $streams := list "pins/*/*" }}
{{- if $k.enabled }}
{{- toYaml (dict "keys" (list (dict "name" "recorder" "identity" $k.identity "issuer" $k.issuer "trustedRoot" $k.trustedRoot "streams" $streams))) }}
{{- else if dig "publicKey" "" $r.signing }}
{{- toYaml (dict "keys" (list (dict "name" "recorder" "publicKey" $r.signing.publicKey "streams" $streams))) }}
{{- end }}
{{- end }}
{{- end }}

{{/*
The data of the publishers ConfigMap the promoter mounts at
/etc/pacto/publishers, as YAML: the delivery policy's signers, the Sigstore
trusted root and the pin signer, each only when set. Empty when none is.
*/}}
{{- define "pacto-operator.delivery.publishersData" -}}
{{- $p := .Values.delivery.publishers | default dict }}
{{- $data := dict }}
{{- with dig "policySigners" "" $p }}{{ $_ := set $data "policy-signers.yaml" . }}{{ end }}
{{- with dig "trustedRoot" "" $p }}{{ $_ := set $data "trusted_root.json" . }}{{ end }}
{{- with include "pacto-operator.delivery.pinSigner" . }}{{ $_ := set $data "pin-signer.yaml" . }}{{ end }}
{{- if $data }}{{ toYaml $data }}{{ end }}
{{- end }}
