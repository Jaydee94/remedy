{{/* The labels every object carries. */}}
{{- define "remedy.labels" -}}
app.kubernetes.io/name: remedy
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | quote }}
{{- end }}

{{/* The labels that select the pods of a component. Call with (dict "ctx" . "component" "server"). */}}
{{- define "remedy.selector" -}}
app.kubernetes.io/name: remedy
app.kubernetes.io/instance: {{ .ctx.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{- define "remedy.serverImage" -}}
{{ .Values.image.server.repository }}:{{ .Values.image.server.tag | default .Chart.AppVersion }}
{{- end }}

{{- define "remedy.runnerImage" -}}
{{ .Values.image.runner.repository }}:{{ .Values.image.runner.tag | default .Chart.AppVersion }}
{{- end }}

{{/* The Secret with the three application secrets. The chart never creates it. */}}
{{- define "remedy.secretName" -}}
{{ required "existingSecret.name is required: the chart never creates the admin password, the runner token or the master key" .Values.existingSecret.name }}
{{- end }}

{{/* The Secret that holds the database connection URL. The chart never creates it and never accepts the URL as a value. */}}
{{- define "remedy.databaseSecret" -}}
{{- if not (kindIs "string" .Values.database.existingSecret) -}}
{{- fail "database.existingSecret is required and must be the name of a Secret (text)" -}}
{{- end -}}
{{ required "database.existingSecret is required: the name of a Secret that holds the PostgreSQL connection URL (the chart never creates it and never takes the URL as a value, it holds a password)" .Values.database.existingSecret }}
{{- end }}

{{/* The key of that Secret; url when unset or empty. A Secret key is letters, digits, dash, underscore and dot. */}}
{{- define "remedy.databaseSecretKey" -}}
{{- $key := .Values.database.secretKey | default "url" -}}
{{- if not (and (kindIs "string" $key) (regexMatch "^[-._a-zA-Z0-9]+$" $key)) -}}
{{- /* The value is never echoed: a connection URL pasted into this field would show its password. */ -}}
{{- fail "database.secretKey is not a valid Secret key name (letters, digits, dash, underscore and dot; the value is not shown)" -}}
{{- end -}}
{{- $key -}}
{{- end }}

{{/* The private ranges: an egress rule for the internet leaves them out. */}}
{{- define "remedy.privateRanges" -}}
- 10.0.0.0/8
- 172.16.0.0/12
- 192.168.0.0/16
- 169.254.0.0/16
{{- end }}

{{/* The control plane's account: the read identity when the cluster is on, otherwise one with no rights. */}}
{{- define "remedy.serverAccount" -}}
{{- if .Values.cluster.enabled -}}remedy-read{{- else -}}remedy-server{{- end -}}
{{- end }}

{{/* The pod of the token refresher, for the CronJob and for the hook Job. */}}
{{- define "remedy.refreshPod" -}}
restartPolicy: Never
serviceAccountName: remedy-token-refresher
automountServiceAccountToken: true
securityContext:
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
  seccompProfile:
    type: RuntimeDefault
containers:
  - name: refresh
    image: {{ include "remedy.serverImage" . | quote }}
    imagePullPolicy: {{ .Values.image.pullPolicy }}
    command: ["/remedy-tokenrefresh"]
    args:
      - --namespace={{ .Release.Namespace }}
      - --account=remedy-write
      - --secret=remedy-write-token
      - --lifetime={{ .Values.cluster.write.tokenRefresh.lifetime }}
      - --api={{ .Values.cluster.api }}
    securityContext:
      allowPrivilegeEscalation: false
      readOnlyRootFilesystem: true
      capabilities:
        drop: [ALL]
{{- end }}

{{/* Checks a port of a networkPolicy value and prints it as an integer. Call with (dict "what" "networkPolicy.hosts[0]" "port" <value>).
     Helm delivers a number from YAML as float64, so a whole float is accepted and a fractional one, a string and a missing port are not. */}}
{{- define "remedy.checkPort" -}}
{{- $p := .port -}}
{{- if not (or (kindIs "float64" $p) (kindIs "int" $p) (kindIs "int64" $p)) -}}
{{- fail (printf "%s.port must be an integer from 1 to 65535, got %v" .what (default "nothing" $p)) -}}
{{- end -}}
{{- if or (ne (float64 $p) (float64 (int64 $p))) (lt (int64 $p) 1) (gt (int64 $p) 65535) -}}
{{- fail (printf "%s.port must be an integer from 1 to 65535, got %v" .what $p) -}}
{{- end -}}
{{- int64 $p -}}
{{- end }}
