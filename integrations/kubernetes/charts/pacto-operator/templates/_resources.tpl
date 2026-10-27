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

{{/*
The Service in front of a pod the operator manages (the dashboard, the
Evidence Server): <fullname>-<component>, selecting pacto-<component> pods.
  ctx, component   the root context and the component name
  service          its values: type, port, nodePort
  targetPort       the container port
*/}}
{{- define "pacto-operator.componentService" -}}
apiVersion: v1
kind: Service
metadata:
  name: {{ include "pacto-operator.fullname" .ctx }}-{{ .component }}
  namespace: {{ .ctx.Release.Namespace }}
  labels:
    {{- include "pacto-operator.labels" (dict "ctx" .ctx "component" .component) | nindent 4 }}
spec:
  type: {{ .service.type }}
  ports:
    - name: http
      port: {{ .service.port }}
      targetPort: {{ .targetPort }}
      protocol: TCP
      {{- if and (eq .service.type "NodePort") .service.nodePort }}
      nodePort: {{ .service.nodePort }}
      {{- end }}
  selector:
    app.kubernetes.io/component: {{ .component }}
    app.kubernetes.io/name: pacto-{{ .component }}
{{- end }}

{{/*
The Ingress to a component's Service.
  ctx, component   the root context and the component name
  ingress          its values: className, annotations, hosts, tls
  port             the Service port
*/}}
{{- define "pacto-operator.ingress" -}}
{{- $service := printf "%s-%s" (include "pacto-operator.fullname" .ctx) .component -}}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ $service }}
  namespace: {{ .ctx.Release.Namespace }}
  labels:
    {{- include "pacto-operator.labels" (dict "ctx" .ctx "component" .component) | nindent 4 }}
  {{- with .ingress.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  {{- with .ingress.className }}
  ingressClassName: {{ . }}
  {{- end }}
  {{- with .ingress.tls }}
  tls:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  rules:
    {{- range .ingress.hosts }}
    - host: {{ .host | quote }}
      http:
        paths:
          {{- range .paths }}
          - path: {{ .path }}
            pathType: {{ .pathType }}
            backend:
              service:
                name: {{ $service }}
                port:
                  number: {{ $.port }}
          {{- end }}
    {{- end }}
{{- end }}

{{/*
The Gateway API HTTPRoute to a component's Service. Its rules default to one
backendRef on the Service.
  ctx, component   the root context and the component name
  route            its values: parentRefs, hostnames, rules
  port             the Service port
*/}}
{{- define "pacto-operator.httpRoute" -}}
{{- $service := printf "%s-%s" (include "pacto-operator.fullname" .ctx) .component -}}
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{ $service }}
  namespace: {{ .ctx.Release.Namespace }}
  labels:
    {{- include "pacto-operator.labels" (dict "ctx" .ctx "component" .component) | nindent 4 }}
spec:
  {{- with .route.parentRefs }}
  parentRefs:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- with .route.hostnames }}
  hostnames:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  rules:
    {{- if .route.rules }}
    {{- toYaml .route.rules | nindent 4 }}
    {{- else }}
    - backendRefs:
        - name: {{ $service }}
          port: {{ .port }}
    {{- end }}
{{- end }}

{{/*
A metrics Service on the pods' metrics port.
  name, namespace, labels   the object's
  port                      the Service port
  selector                  the pods' selector labels
*/}}
{{- define "pacto-operator.metricsService" -}}
apiVersion: v1
kind: Service
metadata:
  name: {{ .name }}
  namespace: {{ .namespace }}
  labels:
    {{- .labels | nindent 4 }}
spec:
  ports:
    - name: metrics
      port: {{ .port }}
      targetPort: metrics
      protocol: TCP
  selector:
    {{- .selector | nindent 4 }}
{{- end }}

{{/*
A ServiceMonitor scraping the metrics port of the Services it selects.
  name, namespace, labels   the object's
  selector                  the Service's labels to match
  monitor                   its values: interval, scrapeTimeout
  secure                    HTTPS with the ServiceAccount token
*/}}
{{- define "pacto-operator.serviceMonitor" -}}
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{ .name }}
  namespace: {{ .namespace }}
  labels:
    {{- .labels | nindent 4 }}
spec:
  endpoints:
    - port: metrics
      scheme: {{ if .secure }}https{{ else }}http{{ end }}
      {{- if .secure }}
      tlsConfig:
        insecureSkipVerify: true
      bearerTokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
      {{- end }}
      {{- with .monitor.interval }}
      interval: {{ . }}
      {{- end }}
      {{- with .monitor.scrapeTimeout }}
      scrapeTimeout: {{ . }}
      {{- end }}
  selector:
    matchLabels:
      {{- .selector | nindent 6 }}
{{- end }}

{{/*
A RoleBinding or ClusterRoleBinding to one ServiceAccount, or to a Group.
  kind                      RoleBinding or ClusterRoleBinding
  name, namespace, labels   the object's (no namespace for a ClusterRoleBinding)
  roleName                  the role it binds (default: name); a RoleBinding
                            binds a Role, a ClusterRoleBinding a ClusterRole
  serviceAccount, serviceAccountNamespace, or group   the subject
*/}}
{{- define "pacto-operator.binding" -}}
apiVersion: rbac.authorization.k8s.io/v1
kind: {{ .kind }}
metadata:
  name: {{ .name }}
  {{- with .namespace }}
  namespace: {{ . }}
  {{- end }}
  labels:
    {{- .labels | nindent 4 }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: {{ trimSuffix "Binding" .kind }}
  name: {{ .roleName | default .name }}
subjects:
  {{- if .group }}
  - apiGroup: rbac.authorization.k8s.io
    kind: Group
    name: {{ .group }}
  {{- else }}
  - kind: ServiceAccount
    name: {{ .serviceAccount }}
    namespace: {{ .serviceAccountNamespace }}
  {{- end }}
{{- end }}

{{/*
A controller's leader election rules: its Lease and the Events it emits.
*/}}
{{- define "pacto-operator.rbac.leaderElectionRules" -}}
- apiGroups: [coordination.k8s.io]
  resources: [leases]
  verbs: [get, list, watch, create, update, patch, delete]
- apiGroups: [""]
  resources: [events]
  verbs: [create, patch]
{{- end }}
