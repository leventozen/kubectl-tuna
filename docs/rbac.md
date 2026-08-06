# RBAC and data-access contract

Tuna is read-only, but “read-only” is not precise enough for a diagnostic
tool. This document records the API operations the current collectors perform
and the data they deliberately do not request.

The contract is pre-release and may become narrower. A change that adds an API
resource or verb must update this document and add a collector test.

## Current operations

Before evaluating rules, Tuna also requests the API server's non-resource
`GET /version` endpoint. The reported minor version is included in output and
is checked against each rule's reviewed compatibility range. If version
discovery fails, version-scoped rules are skipped and health is `unknown`
unless other collected evidence already proves a current focus problem.

The focus Service, Deployment, or Pod is fetched at both the beginning and end
of collection using the same `get` permission. The second read verifies its
UID, `resourceVersion`, and `generation`. If the read fails or the revision
changed, rule evaluation is suspended because the related evidence is known or
could be mixed across a transition. Every collected Deployment and ReplicaSet
is also required to have `status.observedGeneration` at least as new as
`metadata.generation`; this applies to related owner controllers as well as a
Deployment focus and does not add API calls or permissions.
The reason Tuna does not issue blanket second reads or request `watch` access
is documented in [`temporal-integrity.md`](temporal-integrity.md).

| API group | Resource | Verbs | Why |
|---|---|---|---|
| core | `services` | `get`, `list` | fetch a Service focus; discover Services selecting a Pod/workload |
| core | `pods` | `get`, `list` | fetch a Pod focus; discover selected or owned Pods |
| core | `configmaps` | `get` | establish whether a non-optional referenced ConfigMap exists |
| core | `events` | `list` | fetch supporting events with exact kind/name field selectors for related non-Ready Pods |
| apps | `deployments` | `get` | fetch a Deployment focus or resolve a Pod owner |
| apps | `replicasets` | `get`, `list` | resolve Deployment→ReplicaSet→Pod ownership |
| discovery.k8s.io | `endpointslices` | `list` | inspect endpoint readiness for a Service |
| core, cluster-scoped | `nodes` | `get` | inspect pressure conditions for the exact Node hosting a collected Pod |

Tuna does not request create, update, patch, delete, watch, exec, logs, metrics,
or port-forward access.

### Operations by entry point

The aggregated Role below is convenient when all three inspect commands are
allowed. A narrower grant can follow this table (`—` means no current call):

| Resource / endpoint | Service focus | Deployment focus | Pod focus |
|---|---|---|---|
| non-resource `/version` | `get` | `get` | `get` |
| `services` | `get` | `list` | `list` |
| `pods` | `list` | `list` | `get` |
| `configmaps` | `get` | `get` | `get` |
| `events` | `list` | `list` | `list` |
| `deployments` | `get` | `get` | `get` |
| `replicasets` | `get` | `list` | `get` |
| `endpointslices.discovery.k8s.io` | `list` | `list` | `list` |
| cluster-scoped `nodes` | optional `get` | optional `get` | optional `get` |

Service discovery for Deployment and Pod focus remains a namespace list
because Kubernetes has no reverse query over `Service.spec.selector`.
EndpointSlice and Event lists use label/field selectors, but RBAC still
expresses their verb as `list`.

## Secret policy

Tuna does **not** request `get`, `list`, or `watch` access to Secrets. The
typed Kubernetes Secret GET returns the data payload, which is unnecessary for
the current rules. A non-optional Secret reference is retained in the graph
with existence `unknown`, and the report contains an incomplete-evidence note.

This is the permanent default-policy tradeoff: Tuna will not diagnose a
missing Secret from an existence lookup. Even a metadata-only content request
requires granting Secret `get`, and that RBAC permission lets the caller fetch
the full payload outside Tuna. A future explicitly enabled metadata-only mode
would therefore be an additional permission and threat-model decision, not a
silent change to this default contract.

## Namespace-scoped example

The namespaced permissions can be granted with a Role. Replace `NAMESPACE` and
the binding subject before applying it.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: tuna-reader
  namespace: NAMESPACE
rules:
  - apiGroups: [""]
    resources: ["services", "pods"]
    verbs: ["get", "list"]
  - apiGroups: [""]
    resources: ["configmaps"]
    verbs: ["get"]
  - apiGroups: [""]
    resources: ["events"]
    verbs: ["list"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get"]
  - apiGroups: ["apps"]
    resources: ["replicasets"]
    verbs: ["get", "list"]
  - apiGroups: ["discovery.k8s.io"]
    resources: ["endpointslices"]
    verbs: ["list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: tuna-reader
  namespace: NAMESPACE
subjects:
  - kind: User
    name: YOUR_KUBECONFIG_USER
    apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: Role
  name: tuna-reader
  apiGroup: rbac.authorization.k8s.io
```

Node pressure enrichment is optional for most Pod/Service diagnoses but Node is
cluster-scoped. Grant it separately so namespaced read access is not widened:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: tuna-node-reader
rules:
  - apiGroups: [""]
    resources: ["nodes"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: tuna-node-reader
subjects:
  - kind: User
    name: YOUR_KUBECONFIG_USER
    apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: ClusterRole
  name: tuna-node-reader
  apiGroup: rbac.authorization.k8s.io
```

Without Node access, Tuna should continue with partial evidence rather than
fail the inspection. Node-pressure correlation will be unavailable.

Authenticated Kubernetes identities normally receive safe API discovery
permissions from the default `system:discovery` binding. A hardened cluster
that denies `/version` needs a ClusterRole with `nonResourceURLs: ["/version"]`
and `verbs: ["get"]`; a namespaced Role cannot grant a non-resource endpoint.
Tuna reports the missing permission rather than running version-scoped rules.

## Known broad reads

The retained graph is focused, but one reverse-discovery operation is broader
than ideal:

- Services are listed across the namespace to discover which ones select a
  Pod or Deployment-owned Pod.

Deployment-owned ReplicaSets and Pods, and Service EndpointSlices, already use
label selectors. Pod Events use exact involved-object field selectors and are
requested only for related Pods that are not Ready. Future work should measure
the cost of the remaining broad read and narrow it where the Kubernetes API
supports a reliable query.

## Failure semantics

- Failure to fetch the focus object initially is a command error. Failure to
  re-read it after related collection is health-blocking incomplete evidence;
  rules are suspended and health is `unknown`.
- A changed focus identity/revision or stale collected Deployment/ReplicaSet
  generation suspends rule evaluation instead of turning a reconciliation
  window into a diagnosis.
- Failure to determine the API server version skips version-scoped rules and is
  health-blocking incomplete evidence.
- EndpointSlice failure for a Service focus makes health `unknown` because
  endpoint health cannot be established.
- Failure to discover or enrich related Pods, ReplicaSets, Services, Nodes,
  ConfigMaps, or Events is emitted as partial evidence. A rule must not treat
  that failure as object absence.
- A definitive ConfigMap `NotFound` response is evidence of a missing
  reference; a timeout or `Forbidden` response is not.
