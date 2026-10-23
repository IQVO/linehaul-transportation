#!/usr/bin/env bash
# Chart render assertions for linehaul-transportation: Postgres and Kafka are the
# only runtime, so the mandatory configuration has no silent default, no *_MODE
# variable is ever rendered, and the env cmd/api reads is the env the chart
# renders.
# Run: bash charts/linehaul-transportation/tests/render_test.sh   (needs helm)
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_dir="$(cd "$chart_dir/../.." && pwd)"
release=linehaul-transportation
# Dummy values, no password: the chart refuses to render without a database
# source, Kafka brokers and the three consumer groups. None is ever dialled.
groups=(nodeTopologyConsumerGroup:linehaul-transportation-node-topology
  orderSourcingConsumerGroup:linehaul-transportation-order-sourcing
  shippingAndTrackingConsumerGroup:linehaul-transportation-shipping-and-tracking)
base=(--set database.url=postgres://u@example.invalid:5432/db
  --set kafka.brokers=kafka.example.invalid:9092)
for g in "${groups[@]}"; do base+=(--set "config.${g%%:*}=${g#*:}"); done

fail() { echo "FAIL: $*" >&2; exit 1; }
render() { helm template "$release" "$chart_dir" "$@"; }

configmap="$(render "${base[@]}" --show-only templates/configmap.yaml)"
api_deployment="$(render "${base[@]}" --show-only templates/deployment.yaml)"
configmap_name="$(awk '/^  name:/ {print $2; exit}' <<<"$configmap")"
[[ -n "$configmap_name" ]] || fail "could not read the ConfigMap name"

env_value() { # env_value <rendered> <NAME>
  grep -A1 "name: $2\$" <<<"$1" | grep -m1 'value:' | sed -E 's/.*value: "?//; s/"$//'
}
expect_refusal() { # expect_refusal <why> <needle> <helm args...>
  local why="$1" needle="$2" err; shift 2
  if err="$(render "$@" 2>&1)"; then
    fail "the chart must refuse to render: $why"
  fi
  grep -qF -- "$needle" <<<"$err" || fail "the refusal for '$why' does not name '$needle':
$err"
}

# --- the ConfigMap is mounted with envFrom ------------------------------------------
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q 'configMapRef:' \
  || fail "api Deployment does not mount the ConfigMap via envFrom"
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q "name: $configmap_name" \
  || fail "api Deployment envFrom does not reference ConfigMap $configmap_name"
tuned="$(render "${base[@]}" --set config.outboxRelayInterval=7s --set config.outboxMaxAttempts=5 --set config.packagesPerOrder=3 --set config.generateInterval=2h --show-only templates/configmap.yaml)"
grep -q 'OUTBOX_RELAY_INTERVAL: "7s"' <<<"$tuned" || fail "config.outboxRelayInterval is not rendered into the ConfigMap"
grep -q 'OUTBOX_MAX_ATTEMPTS: "5"' <<<"$tuned" || fail "config.outboxMaxAttempts is not rendered into the ConfigMap"
grep -q 'PACKAGES_PER_ORDER: "3"' <<<"$tuned" || fail "config.packagesPerOrder is not rendered into the ConfigMap"
grep -q 'GENERATE_INTERVAL: "2h"' <<<"$tuned" || fail "config.generateInterval is not rendered into the ConfigMap"
# The binary's own defaults are the chart defaults (cmd/api/config.go).
grep -q 'OUTBOX_MAX_ATTEMPTS: "20"' <<<"$configmap" || fail "OUTBOX_MAX_ATTEMPTS must default to 20"
unlimited="$(render "${base[@]}" --set config.outboxMaxAttempts=0 --show-only templates/configmap.yaml)"
grep -q 'OUTBOX_MAX_ATTEMPTS: "0"' <<<"$unlimited" || fail "config.outboxMaxAttempts=0 is not rendered"

# --- chart vs binary: every env var cmd/api reads is rendered -----------------------
# The names are every upper-case string literal in cmd/api's non-test sources
# (config.go names them all), so a new variable fails this test until the chart
# renders it.
binary_env() {
  # shellcheck disable=SC2046
  grep -ohE '"[A-Z][A-Z0-9_]{2,}"' $(ls "$repo_dir"/cmd/api/*.go | grep -v '_test\.go$') | tr -d '"' | sort -u
}
# shellcheck disable=SC2207
api_vars=($(binary_env))
(( ${#api_vars[@]} >= 10 )) || fail "found only ${#api_vars[@]} env literals in cmd/api; the scan is looking in the wrong place"
for var in "${api_vars[@]}"; do
  grep -q "name: $var\$" <<<"$api_deployment" || fail "the api Deployment does not render $var, which cmd/api reads"
done
# Every key the ConfigMap renders is a variable cmd/api reads (no dead keys).
while read -r key; do
  [[ " ${api_vars[*]} " == *" $key "* ]] || fail "ConfigMap key $key is read by cmd/api in no place"
done < <(awk '/^data:/ {d=1; next} d && /^  [A-Z]/ {sub(/:.*/, ""); sub(/^  /, ""); print}' <<<"$configmap")
# Every mandatory variable cmd/api names (`X is required`) is rendered.
required_vars="$(grep -ho '[A-Z_]* is required' "$repo_dir"/cmd/api/config.go | sed -E 's/ is required//' | sort -u)"
[[ -n "$required_vars" ]] || fail "found no mandatory variables in cmd/api/config.go; the scan is looking in the wrong place"
for var in $required_vars; do
  grep -q "name: $var\$" <<<"$api_deployment" || fail "the api Deployment does not render $var, which cmd/api requires"
done

# --- no *_MODE, no in-memory / permissive mode anywhere -----------------------------
render_all="$(render "${base[@]}")"
if grep -qE '^ +(- name: )?[A-Z_]+_MODE\b' <<<"$render_all"; then fail "the render carries a *_MODE variable"; fi
if grep -qiE 'value: "?(permissive|log|off|memory)"?$' <<<"$render_all"; then fail "the render carries a permissive/log/off/memory value"; fi
if grep -qiE 'in-memory|silently runs|permissive' <<<"$render_all"$'\n'"$(cat "$chart_dir/templates/NOTES.txt")"; then
  fail "the render or NOTES.txt mentions an in-memory / permissive mode"
fi
expect_refusal "a *_MODE through extraEnv" "_MODE" "${base[@]}" --set 'extraEnv[0].name=DEMAND_MODE' --set 'extraEnv[0].value=permissive'
expect_refusal "a *_MODE through extraEnv (any prefix)" "_MODE" "${base[@]}" --set 'extraEnv[0].name=STORE_MODE' --set 'extraEnv[0].value=memory'
expect_refusal "extraEnv overriding a mandatory variable" "KAFKA_BROKERS" "${base[@]}" --set 'extraEnv[0].name=KAFKA_BROKERS' --set 'extraEnv[0].value=other:9092'
render "${base[@]}" --set 'extraEnv[0].name=SOME_OTHER' --set 'extraEnv[0].value=1' | grep -q 'name: SOME_OTHER' \
  || fail "an unrelated extraEnv entry must still render"

# --- row 6: DATABASE_URL from a Secret, never the ConfigMap -------------------------
grep -A4 'name: DATABASE_URL$' <<<"$api_deployment" | grep -q 'secretKeyRef:' || fail "DATABASE_URL must come from a Secret (secretKeyRef)"
if grep -qE '^  (MIGRATIONS_)?DATABASE_URL:' <<<"$configmap"; then fail "a DSN must never land in the ConfigMap"; fi
if grep -q 'postgres://' <<<"$configmap"; then fail "a DSN must never land in the ConfigMap"; fi
grep -A6 'name: MIGRATIONS_DATABASE_URL$' <<<"$api_deployment" | grep -q 'secretKeyRef:' || fail "MIGRATIONS_DATABASE_URL must come from a Secret (secretKeyRef)"
grep -A6 'name: MIGRATIONS_DATABASE_URL$' <<<"$api_deployment" | grep -q 'optional: true' || fail "MIGRATIONS_DATABASE_URL must be optional: true (the binary falls back to DATABASE_URL)"
with_secret=(--set database.existingSecret=my-db --set kafka.brokers=k:9092)
for g in "${groups[@]}"; do with_secret+=(--set "config.${g%%:*}=${g#*:}"); done
existing="$(render "${with_secret[@]}" --show-only templates/deployment.yaml)"
grep -A4 'name: DATABASE_URL$' <<<"$existing" | grep -q 'name: my-db' || fail "database.existingSecret is not the DATABASE_URL secret"
if render "${with_secret[@]}" | grep -q 'kind: Secret'; then
  fail "no Secret may be created when database.existingSecret is set"
fi

# --- row 7: pod hardening ----------------------------------------------------------
for needle in 'runAsNonRoot: true' 'readOnlyRootFilesystem: true' 'allowPrivilegeEscalation: false' 'type: RuntimeDefault'; do
  grep -q "$needle" <<<"$api_deployment" || fail "the api Deployment lacks '$needle'"
done
(( $(grep -c 'type: RuntimeDefault' <<<"$api_deployment") >= 2 )) || fail "seccomp RuntimeDefault must be set on the pod and on the container"
grep -A2 'drop:' <<<"$api_deployment" | grep -q -- '- ALL' || fail "the api container must drop ALL capabilities"
grep -q 'path: /healthz' <<<"$api_deployment" || fail "the liveness probe must hit /healthz"
grep -q 'path: /readyz' <<<"$api_deployment" || fail "the readiness probe must hit /readyz"
# Shutdown room: grace period > drain delay + HTTP drain (10s) + workers drain (10s).
# These bounds mirror shutdownTimeout / workerDrainTimeout in cmd/api: fail when they move.
grep -qE 'shutdownTimeout = 10 \* time.Second' "$repo_dir/cmd/api/serve.go" "$repo_dir/cmd/api/main.go" \
  || fail "cmd/api shutdownTimeout is no longer 10s: update the chart's grace-period arithmetic and this test"
grep -qE 'workerDrainTimeout = 10 \* time.Second' "$repo_dir/cmd/api/serve.go" "$repo_dir/cmd/api/main.go" \
  || fail "cmd/api workerDrainTimeout is no longer 10s: update the chart's grace-period arithmetic and this test"
grep -q 'terminationGracePeriodSeconds: 40' <<<"$api_deployment" || fail "the default grace period must be 40"
expect_refusal "a grace period at the 25s sum" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=25
expect_refusal "a grace period under the sum" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=19
expect_refusal "no grace period" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=0
render "${base[@]}" --set terminationGracePeriodSeconds=26 >/dev/null || fail "a 26s grace period (> 5+10+10) must render"
expect_refusal "a long drain delay with the default grace period" "terminationGracePeriodSeconds" "${base[@]}" --set config.shutdownDrainDelay=30s
expect_refusal "a long drain delay in plain seconds" "terminationGracePeriodSeconds" "${base[@]}" --set config.shutdownDrainDelay=30 --set terminationGracePeriodSeconds=50
render "${base[@]}" --set config.shutdownDrainDelay=30s --set terminationGracePeriodSeconds=51 >/dev/null || fail "drain 30s with grace 51 must render"
render "${base[@]}" --set config.shutdownDrainDelay=0 --set terminationGracePeriodSeconds=21 >/dev/null || fail "drain 0 with grace 21 must render"
expect_refusal "an unparseable drain delay" "config.shutdownDrainDelay" "${base[@]}" --set config.shutdownDrainDelay=soon

# --- row 9: mandatory values and consumer groups -------------------------------------
[[ "$(env_value "$api_deployment" KAFKA_BROKERS)" == "kafka.example.invalid:9092" ]] || fail "KAFKA_BROKERS is not rendered from kafka.brokers"
grep -q '^  KAFKA_BROKERS: "kafka.example.invalid:9092"' <<<"$configmap" || fail "the ConfigMap does not carry KAFKA_BROKERS"
for kv in 'NODE_TOPOLOGY_CONSUMER_GROUP=linehaul-transportation-node-topology' \
  'ORDER_SOURCING_CONSUMER_GROUP=linehaul-transportation-order-sourcing' \
  'SHIPPING_AND_TRACKING_CONSUMER_GROUP=linehaul-transportation-shipping-and-tracking'; do
  [[ "$(env_value "$api_deployment" "${kv%%=*}")" == "${kv#*=}" ]] || fail "the api Deployment does not render $kv from config.*"
  grep -q "^  ${kv%%=*}: \"${kv#*=}\"" <<<"$configmap" || fail "the ConfigMap does not carry $kv"
done
if grep -qE 'fieldRef|metadata.name|HOSTNAME|POD_NAME' <<<"$api_deployment"; then
  fail "no env var may derive from the pod (a consumer group must be stable)"
fi
# args that satisfy everything except the one value a case removes.
mandatory_except() { # mandatory_except <database|kafka|<group key>>
  local skip="$1" out=()
  [[ "$skip" == database ]] || out+=(--set database.url=postgres://u@h/db)
  [[ "$skip" == kafka ]] || out+=(--set kafka.brokers=k:9092)
  for g in "${groups[@]}"; do
    [[ "${g%%:*}" == "$skip" ]] || out+=(--set "config.${g%%:*}=${g#*:}")
  done
  printf '%s\n' "${out[@]}"
}
refusal_without() { # refusal_without <value to leave out> <needle>
  local args=()
  while IFS= read -r line; do args+=("$line"); done < <(mandatory_except "$1")
  expect_refusal "no $1" "$2" "${args[@]}"
}
refusal_without database "database.url or database.existingSecret"
refusal_without kafka "kafka.brokers"
for g in "${groups[@]}"; do refusal_without "${g%%:*}" "config.${g%%:*}"; done
expect_refusal "a blank consumer group" "config.nodeTopologyConsumerGroup" "${base[@]}" --set 'config.nodeTopologyConsumerGroup= '
expect_refusal "a pod-derived consumer group" "stable" "${base[@]}" --set 'config.orderSourcingConsumerGroup=svc-$(POD_NAME)'

# --- components: api only, optional ones off by default -----------------------------
for component in mcp frontend analytics-projector analytics-reports; do
  if grep -q "app.kubernetes.io/component: $component" <<<"$render_all"; then fail "this chart has no $component component"; fi
done
if grep -qE 'kind: (HorizontalPodAutoscaler|Ingress|HTTPRoute)' <<<"$render_all"; then
  fail "HPA, Ingress and HTTPRoute must be off by default"
fi
optional="$(render "${base[@]}" --set autoscaling.api.enabled=true --set ingress.enabled=true --set gatewayApi.enabled=true --set gatewayApi.parentRefs[0].name=gw --set gatewayApi.parentRefs[0].namespace=kong)"
for kind in HorizontalPodAutoscaler Ingress HTTPRoute; do
  grep -q "kind: $kind" <<<"$optional" || fail "$kind did not render when enabled"
done
if grep -q 'replicas:' <<<"$(render "${base[@]}" --set autoscaling.api.enabled=true --show-only templates/deployment.yaml)"; then
  fail "the Deployment must omit replicas when the HPA owns them"
fi

echo "PASS: linehaul-transportation chart render assertions"
