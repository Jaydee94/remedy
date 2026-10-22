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

{{/* "true" when the chart installs its own PostgreSQL (the default), "" when database.existingSecret names a database of
     your own. postgres.enabled is unset in values.yaml, so that an explicit true or false can be told from the default:
     unset means bundled unless database.existingSecret is set; true with database.existingSecret is a contradiction; false
     without it leaves no database. All three are refused here, so every caller can trust the answer. */}}
{{- define "remedy.bundledPostgres" -}}
{{- $pg := .Values.postgres | default dict -}}
{{- $external := .Values.database.existingSecret -}}
{{- if not (or (kindIs "invalid" $external) (kindIs "string" $external)) -}}
{{- fail "database.existingSecret must be the name of a Secret (text), or empty for the bundled PostgreSQL" -}}
{{- end -}}
{{- $enabled := $pg.enabled -}}
{{- if not (or (kindIs "invalid" $enabled) (kindIs "bool" $enabled)) -}}
{{- fail (printf "postgres.enabled must be true, false or unset (the default: on unless database.existingSecret is set), got the %s %v" (kindOf $enabled) $enabled) -}}
{{- end -}}
{{- if kindIs "invalid" $enabled -}}
{{- if not $external }}true{{ end -}}
{{- else if $enabled -}}
{{- if $external -}}
{{- fail "database.existingSecret and postgres.enabled: true are both set: use the bundled PostgreSQL (leave database.existingSecret empty) or a database of your own (database.existingSecret, and leave postgres.enabled unset or false), not both" -}}
{{- end -}}
true
{{- else if not $external -}}
{{- fail "no database: postgres.enabled is false and database.existingSecret is not set; set database.existingSecret to the Secret with the connection URL of your PostgreSQL, or leave postgres.enabled unset for the bundled one" -}}
{{- end -}}
{{- end }}

{{/* The Secret that holds the database connection URL of a database of your own. The chart never creates it and never
     accepts the URL as a value. Only called when remedy.bundledPostgres is empty, which means it is set. */}}
{{- define "remedy.databaseSecret" -}}
{{ required "database.existingSecret is required: the name of a Secret that holds the PostgreSQL connection URL (the chart never creates it and never takes the URL as a value, it holds a password)" .Values.database.existingSecret }}
{{- end }}

{{/* The Secret with the password of the bundled PostgreSQL. The chart never creates it: a generated password would change on
     every render (Helm's lookup does not work under Argo CD). */}}
{{- define "remedy.postgresSecret" -}}
{{- $name := (.Values.postgres | default dict).existingSecret -}}
{{- if not (and (kindIs "string" $name) $name) -}}
{{- fail "postgres.existingSecret is required for the bundled PostgreSQL (the default when database.existingSecret is not set): the name of a Secret that exists already and holds the database password under postgres.passwordKey (any characters, for example openssl rand -hex 24); the chart never creates it" -}}
{{- end -}}
{{- if not (regexMatch "^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$" $name) -}}
{{- fail (printf "postgres.existingSecret %q is not a Secret name (lower case letters, digits, dashes and dots)" $name) -}}
{{- end -}}
{{- $name -}}
{{- end }}

{{/* The key of that Secret; password when unset or empty. */}}
{{- define "remedy.postgresSecretKey" -}}
{{- $key := (.Values.postgres | default dict).passwordKey | default "password" -}}
{{- if not (and (kindIs "string" $key) (regexMatch "^[-._a-zA-Z0-9]+$" $key)) -}}
{{- fail "postgres.passwordKey is not a valid Secret key name (letters, digits, dash, underscore and dot; the value is not shown)" -}}
{{- end -}}
{{- $key -}}
{{- end }}

{{/* The defaults of postgres in values.yaml, without enabled. With a database of your own every postgres value must equal
     these (validate.yaml): a value that differs would have no effect. They must stay equal to values.yaml, which the render
     tests prove (every render with database.existingSecret and the default values compares them). */}}
{{- define "remedy.postgresDefaults" -}}
existingSecret: ""
passwordKey: password
image:
  repository: postgres
  tag: "16.15"
persistence:
  size: 5Gi
  storageClass: ""
resources:
  requests: {cpu: 50m, memory: 128Mi}
  limits: {memory: 512Mi}
backup:
  enabled: true
  schedule: "17 3 * * *"
  retentionDays: 7
  size: 2Gi
  storageClass: ""
  resources:
    requests: {cpu: 10m, memory: 64Mi}
    limits: {memory: 256Mi}
{{- end }}

{{/* The address of the bundled PostgreSQL: its headless Service in the release namespace. */}}
{{- define "remedy.postgresHost" -}}
remedy-postgres.{{ .Release.Namespace }}.svc
{{- end }}

{{/* The image of the bundled PostgreSQL, for the database and its backup. */}}
{{- define "remedy.postgresImage" -}}
{{ .Values.postgres.image.repository }}:{{ .Values.postgres.image.tag }}
{{- end }}

{{/* The labels of an object of the bundled PostgreSQL. Its own name, never remedy: no policy that selects by remedy.selector
     (the server, the runner, the refresher) may select these pods by accident. Call with (dict "ctx" . "name" "remedy-postgres"). */}}
{{- define "remedy.postgresLabels" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: {{ .ctx.Release.Name }}
app.kubernetes.io/part-of: remedy
app.kubernetes.io/managed-by: {{ .ctx.Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .ctx.Chart.Name .ctx.Chart.Version | quote }}
{{- end }}

{{/* The labels that select the pods of the bundled PostgreSQL or of its backup. Call like remedy.postgresLabels. */}}
{{- define "remedy.postgresSelector" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: {{ .ctx.Release.Name }}
{{- end }}

{{/* The pod security of the bundled PostgreSQL and its backup: the image's postgres user (999), restricted. */}}
{{- define "remedy.postgresPodSecurity" -}}
runAsNonRoot: true
runAsUser: 999
runAsGroup: 999
fsGroup: 999
fsGroupChangePolicy: OnRootMismatch
seccompProfile:
  type: RuntimeDefault
{{- end }}

{{- define "remedy.postgresContainerSecurity" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop: [ALL]
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
