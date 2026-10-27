{{/*
Shared resource templates. Each renders one whole object from a dict, so the
operator, dashboard, Evidence Server, node agent and delivery objects of a kind
come from one template.
*/}}

{{/*
A ServiceAccount.
  name, namespace, labels   the object's
  sa                        its values: annotations, and automount when set
*/}}
{{- define "pacto-operator.serviceAccount" -}}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ .name }}
  namespace: {{ .namespace }}
  labels:
    {{- .labels | nindent 4 }}
  {{- with .sa.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
{{- if hasKey .sa "automount" }}
automountServiceAccountToken: {{ .sa.automount }}
{{- end }}
{{- end }}
