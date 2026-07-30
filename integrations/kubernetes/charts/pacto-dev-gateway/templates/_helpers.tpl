{{/* Common labels for the dev-gateway resources. */}}
{{- define "pacto-dev-gateway.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: pacto
{{- end -}}
