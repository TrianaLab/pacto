{{/*
Expand the name of the chart.
*/}}
{{- define "pacto-delivery.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "pacto-delivery.fullname" -}}
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
Common labels
*/}}
{{- define "pacto-delivery.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "pacto-delivery.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "pacto-delivery.selectorLabels" -}}
app.kubernetes.io/name: {{ include "pacto-delivery.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
The promoter's ServiceAccount name.
*/}}
{{- define "pacto-delivery.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "pacto-delivery.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
The promoter's apiserver username, the one identity admission lets set
witnessed and edit the pointer.
*/}}
{{- define "pacto-delivery.controllerUser" -}}
system:serviceaccount:{{ .Release.Namespace }}:{{ include "pacto-delivery.serviceAccountName" . }}
{{- end }}

{{/*
A CEL list literal of the given strings.
*/}}
{{- define "pacto-delivery.celList" -}}
[{{- range $i, $g := . }}{{ if $i }}, {{ end }}{{ $g | squote }}{{ end }}]
{{- end }}

{{/*
The recorder's ServiceAccount name.
*/}}
{{- define "pacto-delivery.recorderServiceAccountName" -}}
{{- if .Values.recorder.serviceAccount.create }}
{{- default (printf "%s-recorder" (include "pacto-delivery.fullname" .)) .Values.recorder.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.recorder.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
A CEL expression for one field of an object, or a default when any part of the
path is absent: celField (list "object" "status.stream.head" "''").
*/}}
{{- define "pacto-delivery.celField" -}}
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
{{- define "pacto-delivery.celSame" -}}
{{- printf "%s == %s" (include "pacto-delivery.celField" (list "object" (index . 0) (index . 1))) (include "pacto-delivery.celField" (list "oldObject" (index . 0) (index . 1))) }}
{{- end }}

{{/*
The container image: repository:tag, pinned as repository:tag@digest when
image.digest is set. The tag defaults to the chart appVersion.
*/}}
{{- define "pacto-delivery.image" -}}
{{- $ref := printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) -}}
{{- with .Values.image.digest }}{{ $ref = printf "%s@%s" $ref . }}{{ end -}}
{{- $ref -}}
{{- end }}
