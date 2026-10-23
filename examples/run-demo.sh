#!/usr/bin/env bash
# Deploy a broken or constrained scenario to the current cluster and diagnose it with Tuna.
#
# Usage: examples/run-demo.sh <scenario> [--keep]
#   scenarios: broken-readiness-port | service-selector-mismatch | oomkilled | failed-scheduling | pending-pvc | hpa-metrics-unavailable
set -euo pipefail

SCENARIO="${1:?usage: run-demo.sh <scenario> [--keep]}"
KEEP="${2:-}"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(dirname "$DIR")"

case "$SCENARIO" in
broken-readiness-port | service-selector-mismatch)
	KIND="service"
	NAME="$([ "$SCENARIO" = broken-readiness-port ] && echo payment || echo checkout-api)"
	WAIT=30
	;;
oomkilled)
	KIND="deployment"
	NAME="billing"
	WAIT=90 # needs a couple of OOM kills to enter CrashLoopBackOff
	;;
failed-scheduling)
	KIND="deployment"
	NAME="analytics"
	WAIT=20
	;;
pending-pvc)
	KIND="deployment"
	NAME="storage-api"
	WAIT=20
	;;
hpa-metrics-unavailable)
	KIND="deployment"
	NAME="queue-worker"
	WAIT=30
	;;
*)
	echo "unknown scenario: $SCENARIO" >&2
	exit 1
	;;
esac

echo ">>> Deploying scenario '$SCENARIO'..."
kubectl apply -f "$DIR/$SCENARIO/manifests.yaml"

echo ">>> Waiting ${WAIT}s for the failure to develop..."
sleep "$WAIT"

echo ">>> Running Tuna..."
set +e
go run "$ROOT/cmd/kubectl-tuna" inspect "$KIND" "$NAME" -n tuna-demo
status=$?
set -e

if [ "$KEEP" != "--keep" ]; then
	echo ">>> Cleaning up..."
	kubectl delete namespace tuna-demo --wait=false
fi

# Risk-only scenarios keep the focus healthy and exit 0; broken scenarios exit 2.
if [ "$SCENARIO" = "hpa-metrics-unavailable" ]; then
	[ "$status" -eq 0 ]
else
	[ "$status" -eq 2 ]
fi
