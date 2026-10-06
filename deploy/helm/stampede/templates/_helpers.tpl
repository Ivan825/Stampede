{{/* Chart name. */}}
{{- define "stampede.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Full name, used for the server service workers dial. */}}
{{- define "stampede.fullname" -}}
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

{{- define "stampede.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "stampede.labels" -}}
helm.sh/chart: {{ include "stampede.chart" . }}
{{ include "stampede.selectorLabels" . }}
app.kubernetes.io/version: {{ include "stampede.imageTag" . | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: stampede
{{- with .Values.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{- define "stampede.selectorLabels" -}}
app.kubernetes.io/name: {{ include "stampede.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "stampede.imageTag" -}}
{{- .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{- define "stampede.image" -}}
{{- printf "%s:%s" .Values.image.repository (include "stampede.imageTag" .) }}
{{- end }}

{{- define "stampede.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "stampede.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/* The chart-managed Secret (master key, join token, DB password/URL). */}}
{{- define "stampede.secretName" -}}
{{- include "stampede.fullname" . }}
{{- end }}

{{- define "stampede.masterKeySecret" -}}
{{- default (include "stampede.secretName" .) .Values.masterKey.existingSecret }}
{{- end }}

{{- define "stampede.joinTokenSecret" -}}
{{- default (include "stampede.secretName" .) .Values.joinToken.existingSecret }}
{{- end }}

{{- define "stampede.dbFullname" -}}
{{- printf "%s-timescaledb" (include "stampede.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "stampede.dbPasswordSecret" -}}
{{- default (include "stampede.secretName" .) .Values.timescaledb.existingSecret }}
{{- end }}

{{/* Address in-cluster workers dial. */}}
{{- define "stampede.workerAddress" -}}
{{- if eq .Values.server.ha "active" -}}
{{- printf "dns:%s-replicas.%s.svc:%d" (include "stampede.fullname" .) .Release.Namespace (int .Values.service.workerPort) }}
{{- else -}}
{{- printf "%s:%d" (include "stampede.fullname" .) (int .Values.service.workerPort) }}
{{- end }}
{{- end }}

{{/*
Values for generated secrets. A value given in values.yaml wins; otherwise the
value already stored in the cluster is reused (lookup), so upgrades keep the
same master key, join token and database password; otherwise a new random one
is generated. `helm template` has no cluster, so it always generates.
*/}}
{{- define "stampede.existingSecretData" -}}
{{- $s := lookup "v1" "Secret" .Release.Namespace (include "stampede.secretName" .) -}}
{{- if $s }}{{ toJson $s.data }}{{ else }}{}{{ end -}}
{{- end }}

{{- define "stampede.validate" -}}
{{- if and (not .Values.timescaledb.enabled) (not .Values.database.external.url) (not .Values.database.external.existingSecret) }}
{{- fail "set database.external.url or database.external.existingSecret, or enable timescaledb" }}
{{- end }}
{{- if and .Values.timescaledb.enabled (or .Values.database.external.url .Values.database.external.existingSecret) }}
{{- fail "timescaledb.enabled and database.external are mutually exclusive" }}
{{- end }}
{{- if and .Values.workerTLS.enabled (not .Values.workerTLS.secretName) }}
{{- fail "workerTLS.enabled needs workerTLS.secretName" }}
{{- end }}
{{- if and .Values.workers.enabled (eq .Values.server.executor "local") }}
{{- fail "server.executor=local never uses workers; set workers.enabled=false or use auto/workers" }}
{{- end }}
{{- end }}
