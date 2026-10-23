#!/usr/bin/env bash
# Fast, cluster-free contract tests for the E2E scenario selector.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
E2E="$ROOT/examples/e2e.sh"

expected_cases=$'broken-readiness-port\nhealthy-service\nincomplete-rbac\nmultiple-workloads-one-service\nmulti-container-isolation\nstale-event-identity\nmultiple-replicaset-rollout\nservice-selector-mismatch\nfailed-scheduling\nhpa-metrics-unavailable\nhpa-scale-up-limited\npending-pvc\nstatefulset-unavailable\njob-failed\ncronjob-latest-run\noomkilled'
actual_cases="$("$E2E" --list)"
[[ "$actual_cases" == "$expected_cases" ]] || {
	printf 'unexpected E2E case list:\n%s\n' "$actual_cases" >&2
	exit 1
}

help="$("$E2E" --help)"
[[ "$help" == *'./examples/e2e.sh CASE'* ]] || {
	printf 'E2E help does not describe targeted execution\n' >&2
	exit 1
}
[[ "$help" == *'./examples/e2e.sh --all'* ]] || {
	printf 'E2E help does not describe explicit full execution\n' >&2
	exit 1
}

expect_usage_error() {
	local status
	set +e
	"$E2E" "$@" >/dev/null 2>&1
	status=$?
	set -e
	if [ "$status" -ne 2 ]; then
		printf 'expected usage exit 2 for arguments:' >&2
		printf ' %q' "$@" >&2
		printf '; got %s\n' "$status" >&2
		exit 1
	fi
}

expect_usage_error
expect_usage_error unknown-scenario
expect_usage_error healthy-service oomkilled
expect_usage_error --all healthy-service

metrics_server_manifest="$ROOT/examples/hpa-scale-up-limited/metrics-server.yaml"
expected_metrics_server_image='registry.k8s.io/metrics-server/metrics-server:v0.9.0@sha256:d9862115e7c7881280d3d75ca26bda8ffc0fc213315979575bf23ce9826205c0'
[[ "$(grep -cF "image: $expected_metrics_server_image" "$metrics_server_manifest")" -eq 1 ]] || {
	printf 'metrics-server fixture must use the reviewed digest-pinned image\n' >&2
	exit 1
}
grep -qF -- '- --kubelet-insecure-tls' "$metrics_server_manifest" || {
	printf 'metrics-server fixture must support disposable Kind kubelet certificates\n' >&2
	exit 1
}

printf 'E2E selection contract passed without cluster access\n'
