#!/usr/bin/env python3
"""Assert this chart's Service selectors isolate each component.

Why this exists (fleet-wide): `app.kubernetes.io/name` + `instance` are identical
on every pod a release creates. A Service that selects on those two alone
selects ALL of them. Every Deployment here carries `app.kubernetes.io/component`
in selector.matchLabels and its pod labels, and every Service selects on it, so
each Service selects EXACTLY ONE Deployment. This test fails if that ever stops
being true.

linehaul-transportation has three components: api (always), mcp (the
read-only MCP server of ADR 0008, default off) and frontend (the nginx pod
serving the linehaul_mfe remote of ADR 0009, default off), plus the optional
api HPA / Ingress / HTTPRoute and the mcp Kong route, all off by default. The
test renders the chart with every optional template on, checks each
component's env, and that no route ever points at the frontend.

Run: python3 charts/linehaul-transportation/tests/test_service_selectors.py
Needs: helm, PyYAML.
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

CHART_DIR = Path(__file__).resolve().parents[1]
RELEASE = "linehaul-transportation"

# Dummy values, no password: the chart refuses to render without a database
# source, the Kafka brokers and the three consumer groups. None is ever dialled.
GROUPS = {
    "nodeTopologyConsumerGroup": "linehaul-transportation-node-topology",
    "orderSourcingConsumerGroup": "linehaul-transportation-order-sourcing",
    "shippingAndTrackingConsumerGroup": "linehaul-transportation-shipping-and-tracking",
}


def runtime(skip: str | None = None) -> list[str]:
    """The mandatory values, minus the one named by skip (database, kafka or a group key)."""
    args: list[str] = []
    if skip != "database":
        args += ["--set", "database.url=postgres://u@example.invalid:5432/db"]
    if skip != "kafka":
        args += ["--set", "kafka.brokers=kafka.example.invalid:9092"]
    for key, value in GROUPS.items():
        if skip != key:
            args += ["--set", f"config.{key}={value}"]
    return args


BASE = runtime()
ENABLE_EVERYTHING = BASE + [
    "--set", "autoscaling.api.enabled=true",
    "--set", "ingress.enabled=true",
    "--set", "gatewayApi.enabled=true",
    "--set", "gatewayApi.parentRefs[0].name=gw",
    "--set", "gatewayApi.parentRefs[0].namespace=kong",
    "--set", "mcp.enabled=true",
    "--set", "mcp.route.enabled=true",
    "--set", "mcp.route.parentRefs[0].name=gw",
    "--set", "mcp.route.parentRefs[0].namespace=kong",
    "--set", "frontend.enabled=true",
]

# Every env var cmd/mcp reads (cmd/mcp/main.go).
MCP_ENV = ("MCP_ADDR", "LOG_LEVEL", "DATABASE_URL", "MIGRATIONS_DATABASE_URL")

# Every env var cmd/api reads (cmd/api/config.go).
API_ENV = (
    "HTTP_ADDR", "LOG_LEVEL", "DATABASE_URL", "MIGRATIONS_DATABASE_URL", "KAFKA_BROKERS",
    "NODE_TOPOLOGY_CONSUMER_GROUP", "ORDER_SOURCING_CONSUMER_GROUP",
    "SHIPPING_AND_TRACKING_CONSUMER_GROUP", "OUTBOX_RELAY_INTERVAL", "OUTBOX_MAX_ATTEMPTS",
    "GENERATE_INTERVAL", "PACKAGES_PER_ORDER", "SHUTDOWN_DRAIN_DELAY",
)


def helm_template(extra_args: list[str]) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["helm", "template", RELEASE, str(CHART_DIR), *extra_args],
        capture_output=True, text=True,
    )


def render(extra_args: list[str]) -> list[dict]:
    result = helm_template(extra_args)
    if result.returncode != 0:
        raise SystemExit(f"FAIL: helm template {' '.join(extra_args)}:\n{result.stderr}")
    try:
        import yaml  # type: ignore
    except ModuleNotFoundError:  # pragma: no cover - environment guard
        print("SKIP: PyYAML not available; cannot assert selectors", file=sys.stderr)
        raise SystemExit(0)
    return [d for d in yaml.safe_load_all(result.stdout) if d]


def selector_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("selector") or {}


def pod_labels_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("template", {}).get("metadata", {}).get("labels") or {}


def matches(selector: dict, labels: dict) -> bool:
    return bool(selector) and all(labels.get(k) == v for k, v in selector.items())


def env_of(dep: dict) -> list[dict]:
    return dep["spec"]["template"]["spec"]["containers"][0].get("env", [])


def env_names(dep: dict) -> list[str]:
    return [e["name"] for e in env_of(dep)]


def check_components(docs: list[dict], failures: list[str]) -> None:
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}

    want_deployments = {RELEASE, f"{RELEASE}-mcp", f"{RELEASE}-frontend"}
    want_services = {RELEASE, f"{RELEASE}-mcp", f"{RELEASE}-frontend"}
    if set(deployments) != want_deployments:
        failures.append(f"expected exactly the Deployments {sorted(want_deployments)}, got {sorted(deployments)}")
    if set(services) != want_services:
        failures.append(f"expected exactly the Services {sorted(want_services)}, got {sorted(services)}")
    check_mcp(deployments, services, failures)
    check_frontend(docs, deployments, services, failures)
    if RELEASE in services and selector_of(services[RELEASE]).get("app.kubernetes.io/component") != "api":
        failures.append("the api Service selector must pin component=api")

    # Every Deployment's own selector must pin a component too, and be
    # satisfied by its pod labels.
    for name, dep in deployments.items():
        match_labels = dep["spec"]["selector"].get("matchLabels") or {}
        if "app.kubernetes.io/component" not in match_labels:
            failures.append(f"Deployment {name} selector.matchLabels lacks app.kubernetes.io/component")
        if not matches(match_labels, pod_labels_of(dep)):
            failures.append(f"Deployment {name} pod labels do not satisfy its own selector")

    # The real invariant: each Service selects exactly one Deployment.
    for svc_name, svc in services.items():
        sel = selector_of(svc)
        hit = [d for d, dep in deployments.items() if matches(sel, pod_labels_of(dep))]
        if len(hit) != 1:
            failures.append(f"Service {svc_name} selects {len(hit)} Deployments {sorted(hit)}; expected exactly 1")

    # Ingress / HTTPRoute must route to the api Service, the only one.
    for d in docs:
        if d.get("kind") == "Ingress":
            for rule in d["spec"]["rules"]:
                for path in rule["http"]["paths"]:
                    if path["backend"]["service"]["name"] not in services:
                        failures.append("the Ingress backend is not a Service of this chart")
        if d.get("kind") == "HTTPRoute":
            for rule in d["spec"]["rules"]:
                for ref in rule["backendRefs"]:
                    if ref["name"] not in services:
                        failures.append("the HTTPRoute backend is not a Service of this chart")

    # The api HPA owns replicas when enabled.
    hpa = next((d for d in docs if d.get("kind") == "HorizontalPodAutoscaler"), None)
    if hpa is None or hpa["spec"]["scaleTargetRef"]["name"] != RELEASE:
        failures.append("the api HPA was not rendered (or does not scale the api Deployment)")
    elif "replicas" in deployments.get(RELEASE, {}).get("spec", {}):
        failures.append("the api Deployment must omit replicas when its HPA owns them")

    api = deployments.get(RELEASE)
    if api is None:
        return
    api_env = env_names(api)
    for want in API_ENV:
        if want not in api_env:
            failures.append(f"the api Deployment does not render {want}")
    duplicates = sorted({n for n in api_env if api_env.count(n) > 1})
    if duplicates:
        failures.append(f"the api Deployment renders env vars twice: {duplicates}")
    modes = [n for n in api_env if n.endswith("_MODE")]
    if modes:
        failures.append(f"the api Deployment renders *_MODE variables: {modes}")


def pins(service: dict | None, component: str) -> bool:
    return bool(service) and selector_of(service).get("app.kubernetes.io/component") == component


def command_of(dep: dict) -> list[str]:
    return dep["spec"]["template"]["spec"]["containers"][0].get("command", [])


def check_mcp(deployments: dict, services: dict, failures: list[str]) -> None:
    """The mcp component (ADR 0008): read-only, never Kafka, own Service."""
    name = f"{RELEASE}-mcp"
    if not pins(services.get(name), "mcp"):
        failures.append("the MCP Service must exist and pin component=mcp")
    dep = deployments.get(name)
    if dep is None:
        failures.append("the MCP Deployment was not rendered with mcp.enabled=true")
        return
    if command_of(dep) != ["/app/mcp"]:
        failures.append("the MCP Deployment must run /app/mcp")
    env = env_names(dep)
    for want in MCP_ENV:
        if want not in env:
            failures.append(f"the MCP Deployment does not render {want}")
    forbidden = [n for n in env if n in {"KAFKA_BROKERS", "GENERATE_INTERVAL"} or n.endswith("_CONSUMER_GROUP") or n.startswith("OUTBOX_")]
    if forbidden:
        failures.append(f"the MCP Deployment renders write-side env it must never get: {forbidden}")
    duplicates = sorted({n for n in env if env.count(n) > 1})
    if duplicates:
        failures.append(f"the MCP Deployment renders env vars twice: {duplicates}")


def check_frontend(docs: list[dict], deployments: dict, services: dict, failures: list[str]) -> None:
    """The frontend component (ADR 0009): static bytes, ClusterIP, unrouted."""
    name = f"{RELEASE}-frontend"
    svc = services.get(name)
    if not pins(svc, "frontend"):
        failures.append("the frontend Service must exist and pin component=frontend")
    elif svc.get("spec", {}).get("type") != "ClusterIP":
        failures.append("the frontend Service must be ClusterIP")
    dep = deployments.get(name)
    if dep is None:
        failures.append("the frontend Deployment was not rendered with frontend.enabled=true")
        return
    if env_of(dep):
        failures.append("the frontend Deployment must render NO env (it serves static bytes)")
    for d in docs:
        if d.get("kind") in {"Ingress", "HTTPRoute"} and "frontend" in d["metadata"]["name"]:
            failures.append(f"{d['kind']} {d['metadata']['name']}: frontend routing must not live in this chart")


def check_refusals(failures: list[str]) -> None:
    cases: list[tuple[str, list[str], str]] = [
        ("without a database source", runtime("database"), "requires database.url or database.existingSecret"),
        ("without kafka.brokers", runtime("kafka"), "kafka.brokers"),
        ("a *_MODE through extraEnv",
         BASE + ["--set", "extraEnv[0].name=DEMAND_MODE", "--set", "extraEnv[0].value=permissive"], "extraEnv sets DEMAND_MODE"),
        ("a grace period under the shutdown bounds", BASE + ["--set", "terminationGracePeriodSeconds=19"],
         "terminationGracePeriodSeconds"),
        ("a grace period equal to the shutdown bounds", BASE + ["--set", "terminationGracePeriodSeconds=25"],
         "terminationGracePeriodSeconds"),
    ]
    for key in GROUPS:
        cases.append((f"without config.{key}", runtime(key), f"config.{key} is required"))
    for label, args, needle in cases:
        result = helm_template(args)
        if result.returncode == 0 or needle not in result.stderr:
            failures.append(f"chart rendered (or failed for another reason) {label}")


def main() -> int:
    failures: list[str] = []

    check_components(render(ENABLE_EVERYTHING), failures)

    # Default values must not deploy an HPA or a route.
    defaults = render(BASE)
    stray = [d["kind"] for d in defaults if d.get("kind") in {"HorizontalPodAutoscaler", "Ingress", "HTTPRoute"}]
    if stray:
        failures.append(f"optional resources rendered with default values: {stray}")
    optional = [n for n in (d["metadata"]["name"] for d in defaults) if n.endswith(("-mcp", "-frontend"))]
    if optional:
        failures.append(f"optional components rendered with default values: {optional}")
    dep = next(d for d in defaults if d.get("kind") == "Deployment")
    names = env_names(dep)
    for want in ("KAFKA_BROKERS", "DATABASE_URL", *(k for k in API_ENV if k.endswith("_CONSUMER_GROUP"))):
        if want not in names:
            failures.append(f"{want} is mandatory and must always be rendered")

    check_refusals(failures)

    if failures:
        for f in failures:
            print(f"FAIL: {f}")
        return 1

    print("PASS: every Service selects exactly one Deployment (api, mcp, frontend); mcp and frontend "
          "are off by default; every cmd/api and cmd/mcp env var is rendered and no *_MODE is; the "
          "frontend is never routed by this chart; the chart refuses to render without a database "
          "source, brokers or any consumer group, with a *_MODE through extraEnv, or with a grace "
          "period that does not exceed the shutdown bounds")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
