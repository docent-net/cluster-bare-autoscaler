{{- define "cluster-bare-autoscaler.name" -}}
cluster-bare-autoscaler
{{- end }}

{{- define "cluster-bare-autoscaler.fullname" -}}
{{ include "cluster-bare-autoscaler.name" . }}
{{- end }}
