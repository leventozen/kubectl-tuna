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
KDIAG="$ROOT/bin/kdiag"

go build -o "$KDIAG" "$ROOT/cmd/kdiag"

echo "Kubernetes environment under test"
kubectl version -o json
kubectl get nodes -o custom-columns=NAME:.metadata.name,KUBELET:.status.nodeInfo.kubeletVersion,RUNTIME:.status.nodeInfo.containerRuntimeVersion

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
ok = res.get("health") == "healthy" and not res.get("partial") and not res.get("findings")
raise SystemExit(0 if ok else 1)
'
}

check() {
	local scenario="$1" kind="$2" name="$3" deadline_s="$4" expected_root="$5"

	echo "=== scenario: $scenario (deadline: ${deadline_s}s) ==="
	kubectl apply -f "$DIR/$scenario/manifests.yaml" >/dev/null

	local start=$SECONDS out status roots result="FAIL"
	while [ $((SECONDS - start)) -lt "$deadline_s" ]; do
		sleep 5
		set +e
		out="$("$KDIAG" inspect "$kind" "$name" -n kdiag-demo -o json 2>/dev/null)"
		status=$?
		set -e
		# 2 = findings present; anything else means not broken (yet) or error
		[ "$status" -ne 2 ] && continue
		roots="$(echo "$out" | root_causes)"
		# Exact set equality: an expected root alongside an unrelated false root
		# is a failure, not a pass. root_causes already sorts and deduplicates.
		if [ "$roots" = "$expected_root" ]; then
			result="PASS"
			break
		fi
	done

	kubectl delete namespace kdiag-demo --wait >/dev/null

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
		kubectl get pods -n kdiag-demo -o wide 2>/dev/null || true
		kubectl get events -n kdiag-demo --sort-by=.lastTimestamp 2>/dev/null | tail -20 || true
		fail=1
	fi
}

check_healthy() {
	local scenario="$1" kind="$2" name="$3" deadline_s="$4"

	echo "=== scenario: $scenario (healthy control, deadline: ${deadline_s}s) ==="
	kubectl apply -f "$DIR/$scenario/manifests.yaml" >/dev/null

	local start=$SECONDS out="" status=1 result="FAIL"
	while [ $((SECONDS - start)) -lt "$deadline_s" ]; do
		sleep 5
		set +e
		out="$("$KDIAG" inspect "$kind" "$name" -n kdiag-demo -o json 2>/dev/null)"
		status=$?
		set -e
		if [ "$status" -eq 0 ] && echo "$out" | strictly_healthy; then
			result="PASS"
			break
		fi
	done

	kubectl delete namespace kdiag-demo --wait >/dev/null

	if [ "$result" = "PASS" ]; then
		echo "PASS: strictly healthy with complete evidence and zero findings ($((SECONDS - start))s)"
	else
		echo "FAIL: healthy control did not become strictly healthy within ${deadline_s}s (exit: $status)"
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
		out="$("$KDIAG" inspect "$kind" "$name" -n kdiag-demo -o json 2>/dev/null)"
		status=$?
		set -e
		[ "$status" -ne 2 ] && continue
		roots="$(echo "$out" | root_causes)"
		if [ "$roots" = "$expected_root" ]; then
			broken="PASS"
			break
		fi
	done

	if [ "$broken" = "PASS" ]; then
		kubectl patch deployment payment -n kdiag-demo --type=json \
			-p='[{"op":"replace","path":"/spec/template/spec/containers/0/readinessProbe/httpGet/port","value":80}]' >/dev/null
		start=$SECONDS
		while [ $((SECONDS - start)) -lt "$deadline_s" ]; do
			sleep 5
			set +e
			out="$("$KDIAG" inspect "$kind" "$name" -n kdiag-demo -o json 2>/dev/null)"
			status=$?
			set -e
			if [ "$status" -eq 0 ] && echo "$out" | strictly_healthy; then
				recovered="PASS"
				break
			fi
		done
	fi

	kubectl delete namespace kdiag-demo --wait >/dev/null

	if [ "$broken" = "PASS" ] && [ "$recovered" = "PASS" ]; then
		echo "PASS: exact root '$expected_root', then strictly healthy after repair"
	else
		echo "FAIL: recovery scenario broken=$broken recovered=$recovered roots=${roots:-<none>} exit=$status"
		[ -n "$out" ] && echo "$out"
		fail=1
	fi
}

check_recovery broken-readiness-port service payment 120 readiness-probe-port-mismatch
check_healthy healthy-service service healthy-api 120
check service-selector-mismatch service checkout-api 90 service-selector-no-pods
check failed-scheduling deployment analytics 90 pod-unschedulable
check oomkilled deployment billing 240 container-oomkilled

exit "$fail"
