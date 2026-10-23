{{/*
Expand the name of the chart.
*/}}
{{- define "linehaul-transportation.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "linehaul-transportation.fullname" -}}
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
Chart name and version as used by the chart label.
*/}}
{{- define "linehaul-transportation.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "linehaul-transportation.labels" -}}
helm.sh/chart: {{ include "linehaul-transportation.chart" . }}
{{ include "linehaul-transportation.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels. The Deployment and the Service ALSO pin
app.kubernetes.io/component (api): a Service selecting on name/instance alone
would select every component a future change adds.
*/}}
{{- define "linehaul-transportation.selectorLabels" -}}
app.kubernetes.io/name: {{ include "linehaul-transportation.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "linehaul-transportation.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "linehaul-transportation.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Name of the Secret holding DATABASE_URL: the operator's own
(database.existingSecret) or the one this chart creates from database.url.
*/}}
{{- define "linehaul-transportation.databaseSecretName" -}}
{{- if .Values.database.existingSecret }}
{{- .Values.database.existingSecret }}
{{- else }}
{{- include "linehaul-transportation.fullname" . }}-database
{{- end }}
{{- end }}

{{/*
Image reference.
*/}}
{{- define "linehaul-transportation.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{/*
MANDATORY: cmd/api exits at boot without DATABASE_URL; there is no in-memory
mode. Fails the render naming the values instead of crash-looping.
*/}}
{{- define "linehaul-transportation.requireDatabase" -}}
{{- if not (or .Values.database.url .Values.database.existingSecret) -}}
{{- fail "linehaul-transportation requires database.url or database.existingSecret to be set (DATABASE_URL): the service runs on real Postgres only and exits at boot without it." -}}
{{- end -}}
{{- end -}}

{{/*
MANDATORY: cmd/api exits at boot without KAFKA_BROKERS (outbox relay, the
three consumers and the /readyz broker check). There is no default broker and
no way to switch Kafka off.
*/}}
{{- define "linehaul-transportation.requireKafka" -}}
{{- $_ := required "linehaul-transportation requires kafka.brokers to be set (KAFKA_BROKERS, comma-separated): the service runs on real Kafka only and exits at boot without it." .Values.kafka.brokers -}}
{{- end -}}

{{/*
The three consumers always run, each under a STABLE consumer group id that
must come from values (cmd/api exits at boot without NODE_TOPOLOGY_CONSUMER_GROUP,
ORDER_SOURCING_CONSUMER_GROUP and SHIPPING_AND_TRACKING_CONSUMER_GROUP). A
stable id lets a rescheduled pod resume from its committed offsets; an id
derived from the pod name would replay or skip events on every reschedule.
*/}}
{{- define "linehaul-transportation.requireConsumerGroups" -}}
{{- range $c := list (dict "key" "nodeTopologyConsumerGroup" "env" "NODE_TOPOLOGY_CONSUMER_GROUP") (dict "key" "orderSourcingConsumerGroup" "env" "ORDER_SOURCING_CONSUMER_GROUP") (dict "key" "shippingAndTrackingConsumerGroup" "env" "SHIPPING_AND_TRACKING_CONSUMER_GROUP") -}}
{{- $v := trim (toString (index $.Values.config $c.key)) -}}
{{- if or (eq $v "") (eq $v "<nil>") -}}
{{- fail (printf "config.%s is required (%s): the consumer always runs on real Kafka and cmd/api exits at boot without a group. Use a STABLE, service-prefixed id, never the pod name." $c.key $c.env) -}}
{{- end -}}
{{- if or (contains "$(" $v) (contains "HOSTNAME" $v) (contains "POD_NAME" $v) -}}
{{- fail (printf "config.%s is %q - a consumer group id must be stable, never derived from the pod name or hostname." $c.key $v) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
extraEnv may not bring back an in-memory/permissive mode (no *_MODE variable at
all: the binary has none) and may not re-set a variable that has a dedicated
value, so a mandatory value cannot be bypassed.
*/}}
{{- define "linehaul-transportation.requireSaneExtraEnv" -}}
{{- $owned := list "HTTP_ADDR" "LOG_LEVEL" "DATABASE_URL" "MIGRATIONS_DATABASE_URL" "KAFKA_BROKERS" "NODE_TOPOLOGY_CONSUMER_GROUP" "ORDER_SOURCING_CONSUMER_GROUP" "SHIPPING_AND_TRACKING_CONSUMER_GROUP" "OUTBOX_RELAY_INTERVAL" "OUTBOX_MAX_ATTEMPTS" "GENERATE_INTERVAL" "PACKAGES_PER_ORDER" "SHUTDOWN_DRAIN_DELAY" -}}
{{- range .Values.extraEnv -}}
{{- $name := toString .name -}}
{{- if hasSuffix "_MODE" $name -}}
{{- fail (printf "extraEnv sets %s - *_MODE variables do not exist in linehaul-transportation: it runs on real Postgres and real Kafka only, with no permissive or in-memory mode." $name) -}}
{{- end -}}
{{- if has $name $owned -}}
{{- fail (printf "extraEnv sets %s - it has a dedicated chart value (config.*, database.*, kafka.brokers); set that instead." $name) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
config.shutdownDrainDelay in whole seconds, rounded up. The binary accepts any
Go duration or plain seconds; the chart accepts plain seconds ("5"), "5s",
"1500ms" and "1m" so it can do the grace-period arithmetic, and refuses the rest.
*/}}
{{- define "linehaul-transportation.drainSeconds" -}}
{{- $d := trim (toString .Values.config.shutdownDrainDelay) -}}
{{- if not (regexMatch "^[0-9]+(\\.[0-9]+)?(s|ms|m)?$" $d) -}}
{{- fail (printf "config.shutdownDrainDelay is %q - want plain seconds (\"5\") or a number with s, ms or m (\"5s\", \"1500ms\", \"1m\") so the chart can size terminationGracePeriodSeconds." $d) -}}
{{- end -}}
{{- $num := float64 (regexFind "^[0-9]+(\\.[0-9]+)?" $d) -}}
{{- $unit := trimPrefix (regexFind "^[0-9]+(\\.[0-9]+)?" $d) $d -}}
{{- if eq $unit "ms" -}}{{- ceil (divf $num 1000.0) | int -}}
{{- else if eq $unit "m" -}}{{- ceil (mulf $num 60.0) | int -}}
{{- else -}}{{- ceil $num | int -}}
{{- end -}}
{{- end -}}

{{/*
The graceful shutdown is sequential (cmd/api/serve.go): SHUTDOWN_DRAIN_DELAY,
then the HTTP drain (shutdownTimeout 10s), then the workers - outbox relay,
horizon roll and the three consumers stop together on one context, bounded by
workerDrainTimeout 10s. The grace period must EXCEED the sum, or the kubelet
SIGKILLs the pod between "listener drained" and "relay/consumers stopped",
stranding an uncommitted offset or an in-flight outbox batch.
*/}}
{{- define "linehaul-transportation.requireGracePeriod" -}}
{{- $http := 10 -}}
{{- $workers := 10 -}}
{{- $drain := int (include "linehaul-transportation.drainSeconds" .) -}}
{{- $sum := add $drain $http $workers -}}
{{- if le (int .Values.terminationGracePeriodSeconds) (int $sum) -}}
{{- fail (printf "terminationGracePeriodSeconds is %v - it must exceed %v (SHUTDOWN_DRAIN_DELAY %vs + HTTP drain %vs + workers drain %vs: outbox relay, horizon roll, consumers): a shorter grace period SIGKILLs them mid-drain." .Values.terminationGracePeriodSeconds $sum $drain $http $workers) -}}
{{- end -}}
{{- end -}}

{{/*
Every render-time requirement of the real runtime, in one include.
*/}}
{{- define "linehaul-transportation.requireRealRuntime" -}}
{{- include "linehaul-transportation.requireDatabase" . -}}
{{- include "linehaul-transportation.requireKafka" . -}}
{{- include "linehaul-transportation.requireConsumerGroups" . -}}
{{- include "linehaul-transportation.requireSaneExtraEnv" . -}}
{{- include "linehaul-transportation.requireGracePeriod" . -}}
{{- end -}}
