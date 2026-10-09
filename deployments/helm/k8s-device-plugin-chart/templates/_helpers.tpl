{{- define "k8s-device-plugin-chart.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "k8s-device-plugin-chart.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- include "k8s-device-plugin-chart.name" . -}}
{{- end -}}
{{- end -}}

{{- define "k8s-device-plugin-chart.namespace" -}}
{{- if .Values.namespaceOverride -}}
{{- .Values.namespaceOverride -}}
{{- else -}}
{{- .Release.Namespace -}}
{{- end -}}
{{- end -}}

{{- define "k8s-device-plugin-chart.labels" -}}
app.kubernetes.io/name: {{ include "k8s-device-plugin-chart.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "k8s-device-plugin-chart.selectorLabels" -}}
app.kubernetes.io/name: {{ include "k8s-device-plugin-chart.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- with .Values.selectorLabelsOverride }}
{{- toYaml . }}
{{- end }}
{{- end -}}

{{- define "k8s-device-plugin-chart.fullimage" -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}

{{- define "k8s-device-plugin-chart.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "k8s-device-plugin-chart.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}
