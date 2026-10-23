# Kubernetes compatibility maintenance

Tuna keeps three claims separate:

1. `reviewedRuleWindow` is the inclusive minor-version range whose Kubernetes
   semantics have been reviewed for every built-in rule.
2. `upstreamLatestPatch` is the latest patch reported by Kubernetes for a minor.
3. `upstreamEndOfLife` records when upstream's maintenance period ends.
4. `testedPatch` and `nodeImage` identify the exact digest-pinned Kind image
   exercised by CI.

The machine-readable source for all three is
[`testdata/compatibility/kubernetes-window.json`](../testdata/compatibility/kubernetes-window.json).
CI derives its e2e matrix from this manifest. A Go contract test keeps the
manifest aligned with every built-in rule's fail-closed compatibility range.
`make check-kubernetes-window` compares the recorded upstream patches with the
official stable endpoints and checks image availability and pinned digests
through the read-only container registry API. This maintenance check requires
network access; Tuna's normal diagnostic execution remains offline.

The same reviewed minor window applies to declared rule version sources, not
only the API server. Every rule checks the API server; rules that interpret
kubelet-produced status or Events also validate all related Node kubelet
versions. Mixed kubelet minors are accepted when each is inside the window and
no newer than the API server. Missing, malformed, newer, or unreviewed kubelet
versions skip only those kubelet-dependent rules and make health `unknown`.

Every network operation in the maintenance check is bounded. The global stable
release detects an unreviewed new minor, lifecycle metadata detects an expired
maintenance branch, and per-minor stable endpoints detect patch drift. Each
registry inspection also fails instead of waiting indefinitely when its timeout
expires. A timeout, authentication failure, rate limit, or other registry error
is not interpreted as proof that an image is absent.

`imageLag: true` is intentional when Kubernetes has released newer patches but
none of their `kindest/node` tags are available. It is not a waiver to use a
floating tag or invent a digest. The scheduled check examines every patch
between the tested and upstream versions and fails as soon as any newer tag
becomes resolvable, making the lag actionable without claiming CI runs a patch
that it cannot actually run.

## Advancing a patch

1. Read the Kubernetes patch release notes and inspect changes relevant to
   watched APIs, conditions, ownership, EndpointSlices, Events, and version
   skew.
2. Update `upstreamLatestPatch` for every non-EOL regular or maintenance-mode
   minor in Tuna's reviewed window.
3. Resolve the matching official `kindest/node:vX.Y.Z` image. If it exists,
   record its immutable multi-platform digest, update `testedPatch`, and set
   `imageLag` to `false`. Never commit only the mutable tag.
4. If the tag does not exist, retain the previous resolvable digest-pinned
   image and set `imageLag` to `true`. This is visible test-image lag, not
   latest-patch coverage.
5. Run `make test`, `make check-kubernetes-window`, and the full e2e matrix.
   Preserve the exact environment evidence emitted by each job.

## Adding or removing a minor

1. Confirm the branch is not end-of-life. Kubernetes' regular release branches
   and maintenance-mode branches have different patch policies; neither status
   alone proves Tuna's rules remain correct.
2. Review every built-in rule against the new minor's API behavior, feature
   gates, deprecations, component-skew policy, and relevant release notes.
3. Change the rule constants and `reviewedRuleWindow` together. Add one sorted,
   contiguous manifest entry with upstream lifecycle metadata and a
   digest-pinned image.
4. Run all registry, rule-contract, corpus, race, and e2e tests. A green Kind
   matrix validates the project's scenarios only; it is not a broad support
   guarantee.
5. Update README and roadmap wording if the provisional window changes.
6. Keep selected compatibility evidence beyond ephemeral CI retention before
   treating the window as durable validation.

If a non-EOL minor has no usable official Kind image, keep the discrepancy
visible and decide explicitly whether to add another test distribution. Do not
silently shrink, expand, or mislabel the reviewed window.

## Review record for Kubernetes 1.37

The 1.37 window advance reviewed the stable API surfaces Tuna actually reads:
core/v1 Pods, Services, Nodes, ConfigMaps, and Events; apps/v1 Deployments and
ReplicaSets; and discovery.k8s.io/v1 EndpointSlices. Tuna does not consume an
alpha or beta Kubernetes API.

The [Kubernetes 1.37 release notes](https://kubernetes.io/blog/2026/08/26/kubernetes-v1-37-release/)
were checked against each rule family. The relaxed Service-name validation does
not change selector, port, or EndpointSlice semantics used by traffic rules.
The new Pod resource-health status is additional state that Tuna does not yet
claim to diagnose; it does not change the existing conditions and container
statuses used by current rules. Static Pods can no longer reference ConfigMaps
or Secrets, but Tuna neither creates static Pods nor relies on that invalid
configuration to establish a finding. The kube-dns and cgroup v1 deprecations
do not change the structured fields used by the current diagnostic surface.

The project intentionally retains the v0.36 Kubernetes Go libraries while the
server window is 1.34–1.37. They exactly model 1.36 and Tuna reads only stable
API objects shared across the window. Keeping 1.34 in the matrix while it is in
maintenance mode also prevents the project from withdrawing an already-reviewed
minor before its upstream end of life. This is not a blanket client compatibility
claim: adding a field or API introduced after 1.36 requires a separate dependency
decision and compatibility test.

The review establishes eligibility for the matrix, not support. The full e2e
contract must still pass on the exact 1.34.11, 1.35.8, 1.36.4, and 1.37.0 images
before the window advance is considered mechanically validated.
