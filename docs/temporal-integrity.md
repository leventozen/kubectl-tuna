# Temporal integrity

kdiag collects several Kubernetes resource kinds through sequential API calls.
Those calls do not form an atomic transaction. This document defines what the
current collector proves, what it deliberately does not claim, and why a
blanket second collection pass is not currently the default.

## Current guarantees

For every live inspection, kdiag:

1. records collection start and completion timestamps;
2. records the focus resource's initial `resourceVersion` and `generation`;
3. reads the focus resource again after related evidence is collected;
4. suspends all rules if the focus UID/revision changed or the final read fails;
5. suspends all rules when any collected Deployment or ReplicaSet has
   `status.observedGeneration < metadata.generation`.

These checks reject a transition that is proven from available structured
state. They do not prove that every related Pod, Service, EndpointSlice, Event,
Node, and ConfigMap existed in one atomic cluster snapshot.

## Options considered

### Repeat the full collection

Two complete passes could compare the UID and `resourceVersion` of every
retained object. If every relevant resource and relationship were read in both
passes and remained identical, the observations would have an overlapping
stability interval.

That is stronger evidence, but it has material costs:

- every API request and response payload is approximately doubled;
- Deployment and Pod entry points repeat the namespace-wide Service LIST;
- EndpointSlice LIST calls grow with the number of matching Services;
- owner, Node, ConfigMap, and Event requests must also be repeated or the claim
  remains incomplete;
- active Pods, Events, and EndpointSlices can change frequently, making a
  useful diagnosis become `unknown` even when the relevant condition persists;
- the total inspection still has the same timeout budget.

Repeating only Pods or EndpointSlices would cost less but would not prove the
temporal integrity of the graph that the causal engine actually evaluates.

### Re-read every retained object with GET

Targeted GETs avoid broad second lists but create an N+1 request pattern. They
also require new `get` permissions for resources that current collectors only
need to `list`, including some Service and EndpointSlice paths. kdiag will not
widen its RBAC contract for an incomplete approximation of atomicity.

### Watch every relevant resource

Watches could detect changes after an initial resource version, but introduce
new permissions, lifecycle/timeout complexity, and per-resource-kind state.
Resource versions are opaque and must not be ordered across different kinds as
if they were one global transaction identifier.

## Current decision

kdiag does not perform a blanket second pass or request `watch` permission.
The default policy is:

- hard-fail diagnostic evaluation on a changed or unverifiable focus;
- hard-fail on stale collected Deployment/ReplicaSet controller generations;
- expose collection time bounds and focus revisions in JSON;
- keep the non-atomic related-resource boundary explicit in README and output
  contract work;
- add active-rollout, rapidly changing Pod, and EndpointSlice cases to the
  labeled corpus before paying a permanent 2× collection cost.

If corpus or operator reports demonstrate a false causal root caused by
related-resource drift, the preferred next design is dependency-aware
verification: revalidate only the exact resource revisions used by candidate
findings before correlation. That requires stable rule evidence/dependency
metadata and belongs with the evidence-contract work, not as an unbounded
collector retry.

## Reproducible namespace baseline

`make benchmark` runs a fake-client benchmark for the remaining namespace-wide
Service discovery used by Pod focus. It excludes API-server and network latency
and therefore must not be presented as cluster performance. It makes local
decode/filter/graph cost and approximate serialized ServiceList size
reproducible.

Baseline from an Apple M4 (`darwin/arm64`, five iterations per case):

| Namespace Services | Approx. ServiceList | Collector time | Allocated bytes | Allocations |
|---:|---:|---:|---:|---:|
| 10 | 1.21 KiB | 43.8 µs | 50.0 KiB | 253 |
| 1,000 | 121 KiB | 1.52 ms | 2.30 MiB | 6,215 |
| 5,000 | 609 KiB | 5.34 ms | 11.5 MiB | 30,235 |

The current Pod-focus request count stays constant as unrelated Services grow:
two focus Pod GETs, one namespace Service LIST, and one EndpointSlice LIST for
the single matching Service in the benchmark, plus API server version
discovery. Payload and local allocation are linear in namespace Service count.
A real-cluster benchmark must add API-server latency, serialization, transport,
throttling, and distribution behavior before an optimization or second-pass
policy is selected.

### Exploratory live Kind probe — weak evidence

On 2026-08-06, the same Pod-focus path was sampled five times per size against
a local, single-node Kind v0.32.0 cluster running Kubernetes v1.36.1. The
collector and API server ran on the same Apple M4 host. Object creation was
excluded from the measured interval.

| Namespace Services | Median | Slowest sample (not p95) | Median response payload | Service LIST payload | Requests |
|---:|---:|---:|---:|---:|---:|
| 10 | 5.08 ms | 5.28 ms | 10.8 KiB | 5.0 KiB | 6 |
| 1,000 | 34.5 ms | 43.0 ms | 501 KiB | 495 KiB | 6 |
| 5,000 | 28.9 ms | 54.3 ms | 2.48 MiB | 2.42 MiB | 6 |

This is an exploratory baseline, not a performance result suitable for release
claims or capacity planning. Five warm local samples cannot characterize tail
latency; the non-monotonic medians demonstrate the noise. The generated
Services were synthetic, mostly selectorless, and the cluster had no realistic
concurrent load, admission stack, API Priority and Fairness contention,
throttling, network distance, or managed-control-plane behavior.

The useful observations are deliberately narrow: the request count stayed at
six, the Service LIST payload grew approximately linearly, and no urgent local
latency bottleneck appeared. The sixth request was the GET for Kind's injected
`kube-root-ca.crt` projected ConfigMap, which the fake-client fixture does not
contain. These results justify neither a production performance claim nor an
approximate reverse-selector implementation. A larger benchmark campaign is
deferred until real timeouts, throttling, or operator reports make it relevant.
