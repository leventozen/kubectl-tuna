#!/usr/bin/env bash
# Deploy a broken scenario to the current cluster and diagnose it with kdiag.
#
# Usage: examples/run-demo.sh <scenario> [--keep]
#   scenarios: broken-readiness-port | service-selector-mismatch | oomkilled | failed-scheduling
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
*)
	echo "unknown scenario: $SCENARIO" >&2
	exit 1
	;;
esac

echo ">>> Deploying scenario '$SCENARIO'..."
kubectl apply -f "$DIR/$SCENARIO/manifests.yaml"

echo ">>> Waiting ${WAIT}s for the failure to develop..."
sleep "$WAIT"

echo ">>> Running kdiag..."
set +e
go run "$ROOT/cmd/kdiag" inspect "$KIND" "$NAME" -n kdiag-demo
status=$?
set -e

if [ "$KEEP" != "--keep" ]; then
	echo ">>> Cleaning up..."
	kubectl delete namespace kdiag-demo --wait=false
fi

# kdiag exits 2 when findings are present: that's success for a broken demo.
[ "$status" -eq 2 ]
