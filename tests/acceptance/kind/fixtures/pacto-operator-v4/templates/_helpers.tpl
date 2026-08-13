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
Common labels
*/}}
{{- define "pacto-operator.labels" -}}
helm.sh/chart: {{ include "pacto-operator.chart" . }}
{{ include "pacto-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "pacto-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "pacto-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "pacto-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "pacto-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
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
{{- if .Values.dashboard.enabled }}
- --enable-dashboard
{{- if .Values.dashboard.ociSecrets }}
- --dashboard-oci-secrets={{ join "," .Values.dashboard.ociSecrets }}
{{- else if .Values.dashboard.ociSecret }}
- --dashboard-oci-secret={{ .Values.dashboard.ociSecret }}
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
{{- end }}
{{- end }}
