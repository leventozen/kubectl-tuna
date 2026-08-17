# Kubernetes compatibility maintenance

Tuna keeps three claims separate:

1. `reviewedRuleWindow` is the inclusive minor-version range whose Kubernetes
   semantics have been reviewed for every built-in rule.
2. `upstreamLatestPatch` is the latest patch reported by Kubernetes for a minor.
3. `testedPatch` and `nodeImage` identify the exact digest-pinned Kind image
   exercised by CI.

The machine-readable source for all three is
[`testdata/compatibility/kubernetes-window.json`](../testdata/compatibility/kubernetes-window.json).
CI derives its e2e matrix from this manifest. A Go contract test keeps the
manifest aligned with every built-in rule's fail-closed compatibility range.
`make check-kubernetes-window` compares the recorded upstream patches with the
official stable endpoints and checks image availability in the container
registry. This maintenance check requires network access and Docker; Tuna's
normal diagnostic execution remains offline.

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
2. Update `upstreamLatestPatch` for every maintained minor.
3. Resolve the matching official `kindest/node:vX.Y.Z` image. If it exists,
   record its immutable multi-platform digest, update `testedPatch`, and set
   `imageLag` to `false`. Never commit only the mutable tag.
4. If the tag does not exist, retain the previous resolvable digest-pinned
   image and set `imageLag` to `true`. This is visible test-image lag, not
   latest-patch coverage.
5. Run `make test`, `make check-kubernetes-window`, and the full e2e matrix.
   Preserve the exact environment evidence emitted by each job.

## Adding or removing a minor

1. Confirm the branch is in Kubernetes' maintained release window. Maintenance
   status alone does not prove Tuna's rules remain correct.
2. Review every built-in rule against the new minor's API behavior, feature
   gates, deprecations, component-skew policy, and relevant release notes.
3. Change the rule constants and `reviewedRuleWindow` together. Add one sorted,
   contiguous manifest entry with a digest-pinned image.
4. Run all registry, rule-contract, corpus, race, and e2e tests. A green Kind
   matrix validates the project's scenarios only; it is not a broad support
   guarantee.
5. Update README and roadmap wording if the provisional window changes.
6. Keep selected compatibility evidence beyond ephemeral CI retention before
   treating the window as durable validation.

If a maintained minor has no usable official Kind image, keep the discrepancy
visible and decide explicitly whether to add another test distribution. Do not
silently shrink, expand, or mislabel the reviewed window.
