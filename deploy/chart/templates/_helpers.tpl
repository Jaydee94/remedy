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

{{/* The private ranges: an egress rule for the internet leaves them out. */}}
{{- define "remedy.privateRanges" -}}
- 10.0.0.0/8
- 172.16.0.0/12
- 192.168.0.0/16
- 169.254.0.0/16
{{- end }}
