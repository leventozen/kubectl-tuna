#!/usr/bin/env bash
# End-to-end test: deploy broken, healthy, and recovery scenarios to a real
# cluster (e.g. Kind) and assert exact diagnostic outcomes. Assumes kubectl
# already points at a disposable test cluster.
#
# Each scenario polls until the expected diagnosis appears (or a deadline
# passes), so the suite finishes as soon as the cluster reaches the broken
# state instead of sleeping for a fixed worst-case duration.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(dirname "$DIR")"
TUNA_BIN="$ROOT/bin/kubectl-tuna"
ARTIFACT_DIR="${TUNA_E2E_ARTIFACT_DIR:-$ROOT/artifacts/e2e}"
E2E_DISTRIBUTION="${TUNA_E2E_DISTRIBUTION:-unknown}"
E2E_CLUSTER_NAME="${TUNA_E2E_CLUSTER_NAME:-unknown}"

mkdir -p "$ARTIFACT_DIR/cases"

go build -o "$TUNA_BIN" "$ROOT/cmd/kubectl-tuna"

echo "Kubernetes environment under test"
kubectl version -o json
kubectl get nodes -o custom-columns=NAME:.metadata.name,KUBELET:.status.nodeInfo.kubeletVersion,RUNTIME:.status.nodeInfo.containerRuntimeVersion

capture_environment_artifact() {
	python3 -c '
import datetime, json, subprocess, sys

output_path, distribution, cluster_name = sys.argv[1:]
version = json.loads(subprocess.run(
    ["kubectl", "version", "-o", "json"], check=True, capture_output=True, text=True,
).stdout)
node_list = json.loads(subprocess.run(
    ["kubectl", "get", "nodes", "-o", "json"], check=True, capture_output=True, text=True,
).stdout)

nodes = []
for node in node_list.get("items", []):
    info = node.get("status", {}).get("nodeInfo", {})
    nodes.append({
        "name": node.get("metadata", {}).get("name", ""),
        "kubeletVersion": info.get("kubeletVersion", ""),
        "containerRuntimeVersion": info.get("containerRuntimeVersion", ""),
        "operatingSystem": info.get("operatingSystem", ""),
        "osImage": info.get("osImage", ""),
        "architecture": info.get("architecture", ""),
        "kernelVersion": info.get("kernelVersion", ""),
    })

artifact = {
    "schemaVersion": 1,
    "capturedAt": datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z"),
    "distribution": distribution,
    "clusterName": cluster_name,
    "apiServer": version.get("serverVersion", {}),
    "nodes": sorted(nodes, key=lambda node: node["name"]),
}
with open(output_path, "w", encoding="utf-8") as stream:
    json.dump(artifact, stream, indent=2, sort_keys=True)
    stream.write("\n")
' "$ARTIFACT_DIR/environment.json" "$E2E_DISTRIBUTION" "$E2E_CLUSTER_NAME"
}

write_case_result() {
	local case_id="$1" output="$2"
	if [ -n "$output" ]; then
		printf '%s\n' "$output" >"$ARTIFACT_DIR/cases/$case_id.json"
	fi
}

capture_environment_artifact

fail=0

root_causes() {
	python3 -c '
import json, sys
res = json.load(sys.stdin)
roots = {f["type"] for f in res["findings"] if f["rootCause"]}
print("\n".join(sorted(roots)))
'
}

strictly_healthy() {
	python3 -c '
import json, sys
res = json.load(sys.stdin)
ok = res.get("health") == "healthy" and res.get("partial") is False and res.get("findings") == []
raise SystemExit(0 if ok else 1)
'
}

service_has_ready_endpoint() {
	python3 -c '
import json, sys
slices = json.load(sys.stdin).get("items", [])
ready = any(
    endpoint.get("conditions", {}).get("ready") is not False
    for endpoint_slice in slices
    for endpoint in (endpoint_slice.get("endpoints") or [])
    if endpoint.get("addresses")
)
raise SystemExit(0 if ready else 1)
'
}

matches_incomplete_rbac_contract() {
	python3 -c '
import json, sys
res = json.load(sys.stdin)
warnings = res.get("warnings", [])
endpoint_warnings = [
    warning for warning in warnings
    if warning.get("source") == "endpointslices"
    and warning.get("affectsHealth") is True
    and "forbidden" in warning.get("message", "").lower()
]
warning_sources = sorted(warning.get("source") for warning in warnings)
false_absence = any(
    finding.get("type") == "service-no-ready-endpoints"
    for finding in res.get("findings", [])
)
ok = (
    res.get("health") == "unknown"
    and res.get("partial") is True
    and res.get("findings") == []
    and res.get("rootCauses") == []
    and warning_sources == ["endpointslices"]
    and len(endpoint_warnings) == 1
    and not false_absence
)
raise SystemExit(0 if ok else 1)
'
}

matches_multiple_workloads_contract() {
	python3 -c '
import json, sys
res = json.load(sys.stdin)
findings = res.get("findings", [])

def findings_of_type(finding_type):
    return [finding for finding in findings if finding.get("type") == finding_type]

probe_mismatches = findings_of_type("readiness-probe-port-mismatch")
probe_failures = findings_of_type("readiness-probe-failing")
not_ready = findings_of_type("pod-not-ready")
unavailable = {
    finding.get("resource", {}).get("name"): finding
    for finding in findings_of_type("deployment-unavailable")
}

expected_types = sorted([
    "deployment-unavailable",
    "deployment-unavailable",
    "pod-not-ready",
    "readiness-probe-failing",
    "readiness-probe-port-mismatch",
])
actual_types = sorted(finding.get("type") for finding in findings)

ok = False
if (
    len(probe_mismatches) == 1
    and len(probe_failures) == 1
    and len(not_ready) == 1
    and set(unavailable) == {"broken-backend", "serving-backend"}
):
    mismatch = probe_mismatches[0]
    failure = probe_failures[0]
    pod = not_ready[0]
    broken = unavailable["broken-backend"]
    serving = unavailable["serving-backend"]
    ok = (
        res.get("health") == "healthy"
        and res.get("partial") is False
        and res.get("warnings", []) == []
        and actual_types == expected_types
        and res.get("rootCauses") == [mismatch.get("id")]
        and mismatch.get("resource") == failure.get("resource") == pod.get("resource")
        and mismatch.get("causes") == [failure.get("id")]
        and failure.get("causedBy") == [mismatch.get("id")]
        and failure.get("causes") == [pod.get("id")]
        and pod.get("causedBy") == [failure.get("id")]
        and pod.get("causes") == [broken.get("id")]
        and broken.get("causedBy") == [pod.get("id")]
        and serving.get("causedBy", []) == []
        and serving.get("causes", []) == []
    )
raise SystemExit(0 if ok else 1)
'
}

matches_multi_container_contract() {
	python3 -c '
import json, sys
res = json.load(sys.stdin)
findings = res.get("findings", [])

def one(finding_type):
    matches = [finding for finding in findings if finding.get("type") == finding_type]
    return matches[0] if len(matches) == 1 else None

terminations = [
    finding for finding in findings
    if finding.get("type") in {"container-oomkilled", "container-sigkill"}
]
termination = terminations[0] if len(terminations) == 1 else None
image_pull = one("image-pull-failure")
not_ready = one("pod-not-ready")
unavailable = one("deployment-unavailable")
actual_types = sorted(finding.get("type") for finding in findings)
expected_types = sorted([
    termination.get("type") if termination else "missing-termination",
    "deployment-unavailable",
    "image-pull-failure",
    "pod-not-ready",
])

ok = all([termination, image_pull, not_ready, unavailable]) and (
    res.get("health") == "degraded"
    and res.get("partial") is False
    and res.get("warnings", []) == []
    and actual_types == expected_types
    and res.get("rootCauses") == [image_pull.get("id")]
    and termination.get("subject", {}).get("container") == "memory-sidecar"
    and termination.get("impact") == "historical"
    and termination.get("rootCause") is False
    and termination.get("causes", []) == []
    and termination.get("causedBy", []) == []
    and image_pull.get("subject", {}).get("container") == "app"
    and image_pull.get("causes") == [not_ready.get("id")]
    and image_pull.get("causedBy", []) == []
    and not_ready.get("causedBy") == [image_pull.get("id")]
    and not_ready.get("causes") == [unavailable.get("id")]
    and unavailable.get("resource", {}).get("name") == "multi-container"
    and unavailable.get("causedBy") == [not_ready.get("id")]
)
raise SystemExit(0 if ok else 1)
'
}

matches_broken_contract() {
	local expected_roots="$1"
	python3 -c '
import json, sys
res = json.load(sys.stdin)
actual_roots = sorted({f["type"] for f in res.get("findings", []) if f.get("rootCause")})
expected_roots = sorted(filter(None, sys.argv[1].splitlines()))
ok = res.get("health") == "degraded" and res.get("partial") is False and actual_roots == expected_roots
raise SystemExit(0 if ok else 1)
' "$expected_roots"
}

matches_stale_event_identity_contract() {
	python3 -c '
import json, sys
res = json.load(sys.stdin)
findings = res.get("findings", [])

def one(finding_type):
    matches = [finding for finding in findings if finding.get("type") == finding_type]
    return matches[0] if len(matches) == 1 else None

image_pull = one("image-pull-failure")
not_ready = one("pod-not-ready")
actual_types = sorted(finding.get("type") for finding in findings)
expected_types = ["image-pull-failure", "pod-not-ready"]
false_readiness = any(
    finding.get("type") == "readiness-probe-failing"
    for finding in findings
)

ok = all([image_pull, not_ready]) and (
    res.get("health") == "degraded"
    and res.get("partial") is False
    and res.get("warnings", []) == []
    and actual_types == expected_types
    and res.get("rootCauses") == [image_pull.get("id")]
    and image_pull.get("subject", {}).get("container") == "app"
    and image_pull.get("causes") == [not_ready.get("id")]
    and image_pull.get("causedBy", []) == []
    and not_ready.get("causedBy") == [image_pull.get("id")]
    and not_ready.get("causes", []) == []
    and not false_readiness
)
raise SystemExit(0 if ok else 1)
'
}

matches_multiple_replicaset_rollout_contract() {
	local target_pod="$1" old_pod="$2" target_rs="$3"
	python3 -c '
import json, sys

target_pod, old_pod, target_rs = sys.argv[1:4]
res = json.load(sys.stdin)
findings = res.get("findings", [])

def by_type_name(finding_type, name):
    matches = [
        finding for finding in findings
        if finding.get("type") == finding_type
        and finding.get("resource", {}).get("name") == name
    ]
    return matches[0] if len(matches) == 1 else None

unavailable = by_type_name("deployment-unavailable", "rollout-boundary")
rollout = by_type_name("rollout-stuck", "rollout-boundary")
image_pull = by_type_name("image-pull-failure", target_pod)
target_not_ready = by_type_name("pod-not-ready", target_pod)
old_readiness = by_type_name("readiness-probe-failing", old_pod)
old_not_ready = by_type_name("pod-not-ready", old_pod)

actual_types = sorted(
    (finding.get("type"), finding.get("resource", {}).get("name"))
    for finding in findings
)
expected_types = sorted([
    ("deployment-unavailable", "rollout-boundary"),
    ("rollout-stuck", "rollout-boundary"),
    ("image-pull-failure", target_pod),
    ("pod-not-ready", target_pod),
    ("readiness-probe-failing", old_pod),
    ("pod-not-ready", old_pod),
])
root_ids = sorted(res.get("rootCauses") or [])
expected_roots = sorted([image_pull.get("id"), old_readiness.get("id")]) if image_pull and old_readiness else []
target_evidence = any(
    evidence.get("source") == f"ReplicaSet/{target_rs} (target)"
    for evidence in (rollout.get("evidence") if rollout else []) or []
)

ok = all([unavailable, rollout, image_pull, target_not_ready, old_readiness, old_not_ready]) and (
    res.get("health") == "degraded"
    and res.get("partial") is False
    and res.get("warnings", []) == []
    and len(findings) == 6
    and actual_types == expected_types
    and root_ids == expected_roots
    and image_pull.get("causes") == [target_not_ready.get("id")]
    and image_pull.get("causedBy", []) == []
    and old_readiness.get("causes") == [old_not_ready.get("id")]
    and old_readiness.get("causedBy", []) == []
    and sorted(target_not_ready.get("causes") or []) == sorted([rollout.get("id"), unavailable.get("id")])
    and target_not_ready.get("causedBy") == [image_pull.get("id")]
    and old_not_ready.get("causes") == [unavailable.get("id")]
    and old_not_ready.get("causedBy") == [old_readiness.get("id")]
    and rollout.get("causedBy") == [target_not_ready.get("id")]
    and rollout.get("id") not in (old_not_ready.get("causes") or [])
    and sorted(unavailable.get("causedBy") or []) == sorted([
        target_not_ready.get("id"), old_not_ready.get("id"), rollout.get("id"),
    ])
    and target_evidence
)
raise SystemExit(0 if ok else 1)
' "$target_pod" "$old_pod" "$target_rs"
}

pod_has_unhealthy_event_for_named_uid() {
	local name="$1" uid="$2"
	python3 -c '
import json, sys
name, uid = sys.argv[1:3]
events = json.load(sys.stdin).get("items", [])
ok = any(
    event.get("reason") == "Unhealthy"
    and event.get("involvedObject", {}).get("uid") == uid
    and event.get("involvedObject", {}).get("kind") == "Pod"
    and event.get("involvedObject", {}).get("name") == name
    for event in events
)
raise SystemExit(0 if ok else 1)
' "$name" "$uid"
}

pod_has_unhealthy_event_for_uid() {
	local uid="$1"
	python3 -c '
import json, sys
uid = sys.argv[1]
events = json.load(sys.stdin).get("items", [])
ok = any(
    event.get("reason") == "Unhealthy"
    and event.get("involvedObject", {}).get("uid") == uid
    and event.get("involvedObject", {}).get("kind") == "Pod"
    and event.get("involvedObject", {}).get("name") == "stale-event"
    for event in events
)
raise SystemExit(0 if ok else 1)
' "$uid"
}

check() {
	local scenario="$1" kind="$2" name="$3" deadline_s="$4" expected_root="$5"

	echo "=== scenario: $scenario (deadline: ${deadline_s}s) ==="
	kubectl apply -f "$DIR/$scenario/manifests.yaml" >/dev/null

	local start=$SECONDS out status roots result="FAIL"
	while [ $((SECONDS - start)) -lt "$deadline_s" ]; do
		sleep 5
		set +e
		out="$("$TUNA_BIN" inspect "$kind" "$name" -n tuna-demo -o json 2>/dev/null)"
		status=$?
		set -e
		# 2 = findings present; anything else means not broken (yet) or error
		[ "$status" -ne 2 ] && continue
		roots="$(echo "$out" | root_causes)"
		# Exact set equality: an expected root alongside an unrelated false root
		# is a failure, not a pass. Partial evidence also fails the contract.
		if echo "$out" | matches_broken_contract "$expected_root"; then
			result="PASS"
			break
		fi
	done

	write_case_result "$scenario" "${out:-}"

	if [ "$result" = "PASS" ]; then
		echo "PASS: root cause = $expected_root ($((SECONDS - start))s)"
	else
		echo "FAIL: expected root cause '$expected_root' within ${deadline_s}s; last roots: ${roots:-<none>} (exit: ${status:-?})"
		if [ -n "${out:-}" ]; then
			echo "$out" | python3 -c "import json,sys
res=json.load(sys.stdin)
print('findings:')
for f in res.get('findings',[]):
    flags=[]
    if f.get('rootCause'): flags.append('root')
    if f.get('causedBy'): flags.append('symptom')
    r=f['resource']; ns=r.get('namespace') or ''
    print('  - %s [%s] %s/%s/%s: %s' % (f['type'], ','.join(flags) or 'standalone', r['kind'], ns, r['name'], f['summary']))
" 2>/dev/null || echo "$out"
		fi
		# Cluster breadcrumbs help debug flaky scenarios (OOM timing, image pulls).
		echo "--- cluster status ---"
		kubectl get pods -n tuna-demo -o wide 2>/dev/null || true
		kubectl get events -n tuna-demo --sort-by=.lastTimestamp 2>/dev/null | tail -20 || true
		fail=1
	fi
	kubectl delete namespace tuna-demo --wait >/dev/null
}

check_healthy() {
	local scenario="$1" kind="$2" name="$3" deadline_s="$4"

	echo "=== scenario: $scenario (healthy control, deadline: ${deadline_s}s) ==="
	kubectl apply -f "$DIR/$scenario/manifests.yaml" >/dev/null

	local start=$SECONDS out="" status=1 result="FAIL"
	while [ $((SECONDS - start)) -lt "$deadline_s" ]; do
		sleep 5
		set +e
		out="$("$TUNA_BIN" inspect "$kind" "$name" -n tuna-demo -o json 2>/dev/null)"
		status=$?
		set -e
		if [ "$status" -eq 0 ] && echo "$out" | strictly_healthy; then
			result="PASS"
			break
		fi
	done

	write_case_result "$scenario" "$out"

	if [ "$result" = "PASS" ]; then
		echo "PASS: strictly healthy with complete evidence and zero findings ($((SECONDS - start))s)"
	else
		echo "FAIL: healthy control did not become strictly healthy within ${deadline_s}s (exit: $status)"
		[ -n "$out" ] && echo "$out"
		fail=1
	fi
	kubectl delete namespace tuna-demo --wait >/dev/null
}

check_incomplete_rbac() {
	local scenario="incomplete-rbac" deadline_s=120
	echo "=== scenario: $scenario (healthy resource, EndpointSlice RBAC denied) ==="
	kubectl apply -f "$DIR/$scenario/manifests.yaml" >/dev/null

	local start=$SECONDS out="" status=1 ready="FAIL"
	while [ $((SECONDS - start)) -lt "$deadline_s" ]; do
		sleep 5
		set +e
		out="$("$TUNA_BIN" inspect service healthy-api -n tuna-demo -o json 2>/dev/null)"
		status=$?
		set -e
		if [ "$status" -eq 0 ] && echo "$out" | strictly_healthy; then
			ready="PASS"
			break
		fi
	done

	if [ "$ready" != "PASS" ]; then
		echo "FAIL: incomplete-RBAC control never became strictly healthy with admin credentials"
		[ -n "$out" ] && echo "$out"
		fail=1
		kubectl delete namespace tuna-demo --wait >/dev/null
		kubectl delete clusterrolebinding tuna-e2e-node-reader --ignore-not-found >/dev/null
		kubectl delete clusterrole tuna-e2e-node-reader --ignore-not-found >/dev/null
		return
	fi

	local restricted_kubeconfig cluster token result="FAIL"
	restricted_kubeconfig="$(mktemp)"
	kubectl config view --minify --raw --flatten >"$restricted_kubeconfig"
	cluster="$(kubectl config view --minify -o jsonpath='{.contexts[0].context.cluster}')"
	token="$(kubectl create token tuna-limited -n tuna-demo --duration=10m)"
	kubectl config set-credentials tuna-limited --token="$token" --kubeconfig="$restricted_kubeconfig" >/dev/null
	kubectl config set-context tuna-limited --cluster="$cluster" --user=tuna-limited --namespace=tuna-demo \
		--kubeconfig="$restricted_kubeconfig" >/dev/null
	kubectl config use-context tuna-limited --kubeconfig="$restricted_kubeconfig" >/dev/null

	set +e
	out="$("$TUNA_BIN" --kubeconfig "$restricted_kubeconfig" inspect service healthy-api -n tuna-demo -o json 2>/dev/null)"
	status=$?
	set -e
	write_case_result "$scenario" "$out"
	if [ "$status" -eq 1 ] && echo "$out" | matches_incomplete_rbac_contract; then
		result="PASS"
	fi

	rm -f "$restricted_kubeconfig"
	kubectl delete namespace tuna-demo --wait >/dev/null
	kubectl delete clusterrolebinding tuna-e2e-node-reader --ignore-not-found >/dev/null
	kubectl delete clusterrole tuna-e2e-node-reader --ignore-not-found >/dev/null

	if [ "$result" = "PASS" ]; then
		echo "PASS: denied EndpointSlice evidence produced unknown health without a false finding"
	else
		echo "FAIL: incomplete-RBAC contract was not met (exit: $status)"
		[ -n "$out" ] && echo "$out"
		fail=1
	fi
}

check_multiple_workloads() {
	local scenario="multiple-workloads-one-service" deadline_s=120
	echo "=== scenario: $scenario (shared Service, isolated workload causality) ==="
	kubectl apply -f "$DIR/$scenario/manifests.yaml" >/dev/null

	local endpoint_start=$SECONDS ready_endpoint="FAIL"
	while [ $((SECONDS - endpoint_start)) -lt "$deadline_s" ]; do
		if kubectl get endpointslices -n tuna-demo \
			-l kubernetes.io/service-name=shared-api -o json 2>/dev/null | service_has_ready_endpoint; then
			ready_endpoint="PASS"
			break
		fi
		sleep 2
	done
	if [ "$ready_endpoint" != "PASS" ]; then
		echo "FAIL: serving workload did not produce a ready Service endpoint"
		kubectl get pods -n tuna-demo -o wide 2>/dev/null || true
		fail=1
		kubectl delete namespace tuna-demo --wait >/dev/null
		return
	fi

	local start=$SECONDS out="" status=1 result="FAIL"
	while [ $((SECONDS - start)) -lt "$deadline_s" ]; do
		sleep 5
		set +e
		out="$("$TUNA_BIN" inspect service shared-api -n tuna-demo -o json 2>/dev/null)"
		status=$?
		set -e
		if [ "$status" -eq 0 ] && echo "$out" | matches_multiple_workloads_contract; then
			result="PASS"
			break
		fi
	done

	write_case_result "$scenario" "$out"
	kubectl delete namespace tuna-demo --wait >/dev/null

	if [ "$result" = "PASS" ]; then
		echo "PASS: shared Service stayed healthy and the broken Pod explained only its owning Deployment"
	else
		echo "FAIL: multiple-workload isolation contract was not met (exit: $status)"
		[ -n "$out" ] && echo "$out"
		fail=1
	fi
}

check_multi_container() {
	local scenario="multi-container-isolation" deadline_s=180
	echo "=== scenario: $scenario (container identity and historical isolation) ==="
	kubectl apply -f "$DIR/$scenario/manifests.yaml" >/dev/null

	local start=$SECONDS out="" status=1 result="FAIL"
	while [ $((SECONDS - start)) -lt "$deadline_s" ]; do
		sleep 5
		set +e
		out="$("$TUNA_BIN" inspect deployment multi-container -n tuna-demo -o json 2>/dev/null)"
		status=$?
		set -e
		if [ "$status" -eq 2 ] && echo "$out" | matches_multi_container_contract; then
			result="PASS"
			break
		fi
	done

	write_case_result "$scenario" "$out"
	kubectl delete namespace tuna-demo --wait >/dev/null

	if [ "$result" = "PASS" ]; then
		echo "PASS: recovered sidecar termination stayed historical and did not explain the app image failure"
	else
		echo "FAIL: multi-container isolation contract was not met (exit: $status)"
		[ -n "$out" ] && echo "$out"
		fail=1
	fi
}

check_recovery() {
	local scenario="$1" kind="$2" name="$3" deadline_s="$4" expected_root="$5"

	echo "=== scenario: $scenario (broken -> recovered, deadline per phase: ${deadline_s}s) ==="
	kubectl apply -f "$DIR/$scenario/manifests.yaml" >/dev/null

	local start=$SECONDS out="" status=1 roots="" broken="FAIL" recovered="FAIL"
	while [ $((SECONDS - start)) -lt "$deadline_s" ]; do
		sleep 5
		set +e
		out="$("$TUNA_BIN" inspect "$kind" "$name" -n tuna-demo -o json 2>/dev/null)"
		status=$?
		set -e
		[ "$status" -ne 2 ] && continue
		roots="$(echo "$out" | root_causes)"
		if echo "$out" | matches_broken_contract "$expected_root"; then
			broken="PASS"
			break
		fi
	done
	write_case_result "$scenario-broken" "$out"

	if [ "$broken" = "PASS" ]; then
		kubectl patch deployment payment -n tuna-demo --type=json \
			-p='[{"op":"replace","path":"/spec/template/spec/containers/0/readinessProbe/httpGet/port","value":80}]' >/dev/null
		start=$SECONDS
		while [ $((SECONDS - start)) -lt "$deadline_s" ]; do
			sleep 5
			set +e
			out="$("$TUNA_BIN" inspect "$kind" "$name" -n tuna-demo -o json 2>/dev/null)"
			status=$?
			set -e
			if [ "$status" -eq 0 ] && echo "$out" | strictly_healthy; then
				recovered="PASS"
				break
			fi
		done
		write_case_result "$scenario-recovered" "$out"
	fi

	kubectl delete namespace tuna-demo --wait >/dev/null

	if [ "$broken" = "PASS" ] && [ "$recovered" = "PASS" ]; then
		echo "PASS: exact root '$expected_root', then strictly healthy after repair"
	else
		echo "FAIL: recovery scenario broken=$broken recovered=$recovered roots=${roots:-<none>} exit=$status"
		[ -n "$out" ] && echo "$out"
		fail=1
	fi
}

check_stale_event_identity() {
	local scenario="stale-event-identity" deadline_s=180
	echo "=== scenario: $scenario (same-name Pod must not inherit stale Events) ==="
	kubectl apply -f "$DIR/$scenario/old-pod.yaml" >/dev/null

	local old_uid=""
	local start=$SECONDS
	while [ $((SECONDS - start)) -lt 60 ]; do
		old_uid="$(kubectl get pod stale-event -n tuna-demo -o jsonpath='{.metadata.uid}' 2>/dev/null || true)"
		[ -n "$old_uid" ] && break
		sleep 1
	done
	if [ -z "$old_uid" ]; then
		echo "FAIL: old Pod UID was never observed"
		kubectl get pods -n tuna-demo -o wide 2>/dev/null || true
		fail=1
		kubectl delete namespace tuna-demo --wait >/dev/null
		return
	fi

	local unhealthy="FAIL"
	start=$SECONDS
	while [ $((SECONDS - start)) -lt "$deadline_s" ]; do
		if kubectl get events -n tuna-demo -o json \
			--field-selector "involvedObject.kind=Pod,involvedObject.name=stale-event,involvedObject.uid=${old_uid}" \
			2>/dev/null | pod_has_unhealthy_event_for_uid "$old_uid"; then
			unhealthy="PASS"
			break
		fi
		sleep 2
	done
	if [ "$unhealthy" != "PASS" ]; then
		echo "FAIL: Unhealthy Event for the old Pod UID was not observed within ${deadline_s}s"
		kubectl get pod stale-event -n tuna-demo -o yaml 2>/dev/null || true
		kubectl get events -n tuna-demo --sort-by=.lastTimestamp 2>/dev/null | tail -30 || true
		fail=1
		kubectl delete namespace tuna-demo --wait >/dev/null
		return
	fi

	kubectl delete pod stale-event -n tuna-demo --wait --timeout=60s >/dev/null
	kubectl apply -f "$DIR/$scenario/current-pod.yaml" >/dev/null

	local new_uid=""
	start=$SECONDS
	while [ $((SECONDS - start)) -lt 60 ]; do
		new_uid="$(kubectl get pod stale-event -n tuna-demo -o jsonpath='{.metadata.uid}' 2>/dev/null || true)"
		[ -n "$new_uid" ] && break
		sleep 1
	done
	if [ -z "$new_uid" ] || [ "$new_uid" = "$old_uid" ]; then
		echo "FAIL: new Pod UID must be non-empty and different from the old Pod UID"
		kubectl get pod stale-event -n tuna-demo -o yaml 2>/dev/null || true
		fail=1
		kubectl delete namespace tuna-demo --wait >/dev/null
		return
	fi

	if ! kubectl get events -n tuna-demo -o json \
		--field-selector "involvedObject.kind=Pod,involvedObject.name=stale-event,involvedObject.uid=${old_uid}" \
		2>/dev/null | pod_has_unhealthy_event_for_uid "$old_uid"; then
		echo "FAIL: old-UID Unhealthy Event did not persist after same-name recreation; stale evidence was not reproduced"
		kubectl get events -n tuna-demo --sort-by=.lastTimestamp 2>/dev/null | tail -30 || true
		fail=1
		kubectl delete namespace tuna-demo --wait >/dev/null
		return
	fi

	local out="" status=1 result="FAIL"
	start=$SECONDS
	while [ $((SECONDS - start)) -lt "$deadline_s" ]; do
		sleep 5
		set +e
		out="$("$TUNA_BIN" inspect pod stale-event -n tuna-demo -o json 2>/dev/null)"
		status=$?
		set -e
		if [ "$status" -eq 2 ] && echo "$out" | matches_stale_event_identity_contract; then
			result="PASS"
			break
		fi
	done

	write_case_result "$scenario" "${out:-}"
	if [ "$result" != "PASS" ]; then
		echo "FAIL: stale-event identity contract was not met (exit: ${status:-?})"
		if [ -n "${out:-}" ]; then
			echo "$out" | python3 -c "import json,sys
res=json.load(sys.stdin)
print('findings:')
for f in res.get('findings',[]):
    flags=[]
    if f.get('rootCause'): flags.append('root')
    if f.get('causedBy'): flags.append('symptom')
    r=f['resource']; ns=r.get('namespace') or ''
    print('  - %s [%s] %s/%s/%s: %s' % (f['type'], ','.join(flags) or 'standalone', r['kind'], ns, r['name'], f['summary']))
" 2>/dev/null || echo "$out"
		fi
		echo "--- cluster status ---"
		kubectl get pods -n tuna-demo -o wide 2>/dev/null || true
		kubectl get events -n tuna-demo --sort-by=.lastTimestamp 2>/dev/null | tail -30 || true
		fail=1
	fi
	kubectl delete namespace tuna-demo --wait >/dev/null
	if [ "$result" = "PASS" ]; then
		echo "PASS: stale Unhealthy Event was excluded; only image-pull explained Pod NotReady"
	fi
}

check_multiple_replicaset_rollout() {
	local scenario="multiple-replicaset-rollout" deadline_s=240
	echo "=== scenario: $scenario (current-template RS owns rollout-stuck) ==="
	kubectl apply -f "$DIR/$scenario/manifests.yaml" >/dev/null

	local start=$SECONDS ready="FAIL"
	while [ $((SECONDS - start)) -lt 120 ]; do
		if kubectl rollout status deployment/rollout-boundary -n tuna-demo --timeout=5s >/dev/null 2>&1; then
			ready="PASS"
			break
		fi
		sleep 2
	done
	if [ "$ready" != "PASS" ]; then
		echo "FAIL: initial rollout-boundary Deployment never became ready"
		kubectl get pods -n tuna-demo -o wide 2>/dev/null || true
		kubectl get rs -n tuna-demo -o wide 2>/dev/null || true
		fail=1
		kubectl delete namespace tuna-demo --wait >/dev/null
		return
	fi

	local old_pod old_rs old_uid
	old_pod="$(kubectl get pods -n tuna-demo -l app=rollout-boundary -o jsonpath='{.items[0].metadata.name}')"
	old_uid="$(kubectl get pod "$old_pod" -n tuna-demo -o jsonpath='{.metadata.uid}')"
	old_rs="$(python3 -c '
import json, subprocess, sys
pod = json.loads(subprocess.run(
    ["kubectl", "get", "pod", sys.argv[1], "-n", "tuna-demo", "-o", "json"],
    check=True, capture_output=True, text=True,
).stdout)
for owner in pod.get("metadata", {}).get("ownerReferences") or []:
    if owner.get("kind") == "ReplicaSet" and owner.get("controller") is True:
        print(owner["name"])
        raise SystemExit(0)
raise SystemExit("old Pod has no controller ReplicaSet owner")
' "$old_pod")"

	kubectl patch deployment rollout-boundary -n tuna-demo --type=json \
		-p='[{"op":"replace","path":"/spec/template/spec/containers/0/image","value":"invalid@@image"}]' >/dev/null

	local target_pod="" target_rs="" target_uid=""
	start=$SECONDS
	while [ $((SECONDS - start)) -lt "$deadline_s" ]; do
		eval "$(python3 -c '
import json, subprocess, sys

dep = json.loads(subprocess.run(
    ["kubectl", "get", "deployment", "rollout-boundary", "-n", "tuna-demo", "-o", "json"],
    check=True, capture_output=True, text=True,
).stdout)
rss = json.loads(subprocess.run(
    ["kubectl", "get", "rs", "-n", "tuna-demo", "-o", "json"],
    check=True, capture_output=True, text=True,
).stdout).get("items", [])
pods = json.loads(subprocess.run(
    ["kubectl", "get", "pods", "-n", "tuna-demo", "-o", "json"],
    check=True, capture_output=True, text=True,
).stdout).get("items", [])

old_rs = sys.argv[1]
dep_uid = dep["metadata"]["uid"]
desired_image = dep["spec"]["template"]["spec"]["containers"][0]["image"]

def owned_by_dep(rs):
    for owner in rs.get("metadata", {}).get("ownerReferences") or []:
        if owner.get("kind") == "Deployment" and owner.get("uid") == dep_uid and owner.get("controller") is True:
            return True
    return False

def template_image(obj):
    containers = obj.get("spec", {}).get("template", {}).get("spec", {}).get("containers") or []
    return containers[0]["image"] if containers else ""

candidates = []
for rs in rss:
    if not owned_by_dep(rs):
        continue
    if rs["metadata"]["name"] == old_rs:
        continue
    if template_image(rs) != desired_image:
        continue
    candidates.append(rs)

candidates.sort(key=lambda rs: (
    rs["metadata"].get("creationTimestamp", ""),
    rs["metadata"]["name"],
))
if not candidates:
    raise SystemExit(0)

target_rs_obj = candidates[0]
target_rs_name = target_rs_obj["metadata"]["name"]
target_rs_uid = target_rs_obj["metadata"]["uid"]
target_pod_obj = None
for pod in pods:
    for owner in pod.get("metadata", {}).get("ownerReferences") or []:
        if owner.get("kind") == "ReplicaSet" and owner.get("uid") == target_rs_uid and owner.get("controller") is True:
            target_pod_obj = pod
            break
    if target_pod_obj is not None:
        break
if target_pod_obj is None:
    raise SystemExit(0)

waiting = None
for status in target_pod_obj.get("status", {}).get("containerStatuses") or []:
    if status.get("name") == "app":
        waiting = (status.get("state") or {}).get("waiting") or {}
        break
if not waiting or waiting.get("reason") != "InvalidImageName":
    raise SystemExit(0)

print("target_rs=" + target_rs_name)
print("target_pod=" + target_pod_obj["metadata"]["name"])
print("target_uid=" + target_pod_obj["metadata"]["uid"])
' "$old_rs")"
		if [ -n "${target_pod:-}" ] && [ -n "${target_rs:-}" ]; then
			break
		fi
		sleep 2
	done
	if [ -z "${target_pod:-}" ] || [ -z "${target_rs:-}" ] || [ "$target_pod" = "$old_pod" ] || [ "$target_rs" = "$old_rs" ]; then
		echo "FAIL: target ReplicaSet/Pod with InvalidImageName was not observed"
		kubectl get pods -n tuna-demo -o wide 2>/dev/null || true
		kubectl get rs -n tuna-demo -o wide 2>/dev/null || true
		fail=1
		kubectl delete namespace tuna-demo --wait >/dev/null
		return
	fi

	kubectl exec -n tuna-demo "$old_pod" -- rm /state/ready >/dev/null

	local old_not_ready="FAIL"
	start=$SECONDS
	while [ $((SECONDS - start)) -lt 60 ]; do
		local ready_status
		ready_status="$(kubectl get pod "$old_pod" -n tuna-demo -o jsonpath='{range .status.conditions[?(@.type=="Ready")]}{.status}{end}' 2>/dev/null || true)"
		if [ "$ready_status" = "False" ] && kubectl get events -n tuna-demo -o json \
			--field-selector "involvedObject.kind=Pod,involvedObject.name=${old_pod},involvedObject.uid=${old_uid}" \
			2>/dev/null | pod_has_unhealthy_event_for_named_uid "$old_pod" "$old_uid"; then
			old_not_ready="PASS"
			break
		fi
		sleep 2
	done
	if [ "$old_not_ready" != "PASS" ]; then
		echo "FAIL: old Pod did not become Ready=False with an exact-UID Unhealthy Event"
		kubectl get pod "$old_pod" -n tuna-demo -o yaml 2>/dev/null || true
		kubectl get events -n tuna-demo --sort-by=.lastTimestamp 2>/dev/null | tail -30 || true
		fail=1
		kubectl delete namespace tuna-demo --wait >/dev/null
		return
	fi

	local deadline_hit="FAIL"
	start=$SECONDS
	while [ $((SECONDS - start)) -lt "$deadline_s" ]; do
		if python3 -c '
import json, subprocess, sys
dep = json.loads(subprocess.run(
    ["kubectl", "get", "deployment", "rollout-boundary", "-n", "tuna-demo", "-o", "json"],
    check=True, capture_output=True, text=True,
).stdout)
ok = any(
    cond.get("type") == "Progressing"
    and cond.get("status") == "False"
    and cond.get("reason") == "ProgressDeadlineExceeded"
    for cond in dep.get("status", {}).get("conditions") or []
)
raise SystemExit(0 if ok else 1)
'; then
			deadline_hit="PASS"
			break
		fi
		sleep 2
	done
	if [ "$deadline_hit" != "PASS" ]; then
		echo "FAIL: Progressing=False/ProgressDeadlineExceeded was not observed"
		kubectl get deployment rollout-boundary -n tuna-demo -o yaml 2>/dev/null || true
		fail=1
		kubectl delete namespace tuna-demo --wait >/dev/null
		return
	fi

	local out="" status=1 result="FAIL"
	start=$SECONDS
	while [ $((SECONDS - start)) -lt "$deadline_s" ]; do
		sleep 5
		set +e
		out="$("$TUNA_BIN" inspect deployment rollout-boundary -n tuna-demo -o json 2>/dev/null)"
		status=$?
		set -e
		if [ "$status" -eq 2 ] && echo "$out" | matches_multiple_replicaset_rollout_contract "$target_pod" "$old_pod" "$target_rs"; then
			result="PASS"
			break
		fi
	done

	write_case_result "$scenario" "${out:-}"
	if [ "$result" != "PASS" ]; then
		echo "FAIL: multiple-ReplicaSet rollout contract was not met (exit: ${status:-?})"
		if [ -n "${out:-}" ]; then
			echo "$out" | python3 -c "import json,sys
res=json.load(sys.stdin)
print('findings:')
for f in res.get('findings',[]):
    flags=[]
    if f.get('rootCause'): flags.append('root')
    if f.get('causedBy'): flags.append('symptom')
    r=f['resource']; ns=r.get('namespace') or ''
    print('  - %s [%s] %s/%s/%s: %s' % (f['type'], ','.join(flags) or 'standalone', r['kind'], ns, r['name'], f['summary']))
    print('    causes=%s causedBy=%s' % (f.get('causes'), f.get('causedBy')))
" 2>/dev/null || echo "$out"
		fi
		echo "--- cluster status ---"
		echo "old_pod=$old_pod old_rs=$old_rs target_pod=$target_pod target_rs=$target_rs"
		kubectl get pods -n tuna-demo -o wide 2>/dev/null || true
		kubectl get rs -n tuna-demo -o wide 2>/dev/null || true
		kubectl get deployment rollout-boundary -n tuna-demo -o yaml 2>/dev/null || true
		kubectl get events -n tuna-demo --sort-by=.lastTimestamp 2>/dev/null | tail -40 || true
		fail=1
	fi
	kubectl delete namespace tuna-demo --wait >/dev/null
	if [ "$result" = "PASS" ]; then
		echo "PASS: old Pod explained unavailability only; target Pod explained rollout-stuck ($((SECONDS - start))s)"
	fi
}

check_recovery broken-readiness-port service payment 120 readiness-probe-port-mismatch
check_healthy healthy-service service healthy-api 120
check_incomplete_rbac
check_multiple_workloads
check_multi_container
check_stale_event_identity
check_multiple_replicaset_rollout
check service-selector-mismatch service checkout-api 90 service-selector-no-pods
check failed-scheduling deployment analytics 90 pod-unschedulable
check oomkilled deployment billing 240 container-oomkilled

exit "$fail"
