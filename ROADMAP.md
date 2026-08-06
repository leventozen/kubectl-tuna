# kdiag roadmap

This roadmap is organized around proof and trust, not version numbers. kdiag
will not be submitted to Krew or assigned a supported release version until the
current diagnostic surface has earned that step.

## Product thesis

> kdiag turns disconnected Kubernetes observations into a compact causal
> diagnosis by combining a directed resource graph, deterministic rules,
> visible evidence, and explicit unknown state.

The valuable part is not the number of checks. It is whether an operator can
trust the distinction between current problem, risk, history, propagated
symptom, and evidence kdiag could not collect.

## Non-negotiable principles

1. A false confident root cause is worse than an `unknown` result.
2. Every causal edge must name the Kubernetes mechanism and require an exact
   directed relationship; generic graph proximity is not sufficient.
3. API failure, RBAC denial, and deliberately uncollected data are first-class
   output states, never proof of absence.
4. Every rule needs positive, negative, boundary, and adversarial coverage.
5. Event messages enrich structured state; they do not become an unrestricted
   string-matching inference engine.
6. Read-only must include data minimization. Diagnostic convenience does not
   justify reading Secret payloads.
7. Kubernetes compatibility is explicit and fail-closed. A new server minor,
   component skew, or unavailable capability is not assumed equivalent.
8. Extensibility must not let third-party rules bypass evidence, causality,
   data-access, timeout, or provenance boundaries.
9. Multi-object collection is not an atomic Kubernetes snapshot. Collection
   time bounds, focus-resource stability, and controller freshness must be
   visible; proven transition windows suspend diagnosis instead of producing a
   mixed-time causal chain.

## Current execution order

The latest trust audit puts evidence quality ahead of new resource families or
external rule formats:

1. Define and benchmark the remaining bounded stability policy for related Pods
   and EndpointSlices. Focus revision and related Deployment/ReplicaSet
   controller freshness are now covered.
2. Measure the remaining namespace-wide Service scan and large-namespace
   latency/partial-result behavior.
3. Build a labeled corpus with at least 25 realistic cases, including at least
   10 healthy/recovered controls and positive/negative coverage for every rule.
4. Validate results with 3–5 external operators and turn every wrong or missed
   diagnosis into a regression case.
5. Stabilize the JSON/evidence contract only after the corpus exposes which
   fields integrations actually need; external rule packs remain later.

## Gate A — Reliability baseline (in progress)

The current goal is to make the existing Service, Deployment, and Pod slice
defensible before adding more resource kinds.

Completed:

- [x] Remove `pdb-blocks-rollout`. Kubernetes workload rolling upgrades do not
  use the Eviction API and are not constrained by PDBs.
- [x] Replace finding-type + undirected-proximity correlation with directed,
  typed predicates (`selects`, `owns`, `scheduled-on`).
- [x] Add container identity to component-level findings and reject
  cross-container causal links.
- [x] Interpret EndpointSlice `conditions.ready: null` as ready, per the API
  contract.
- [x] Sort nodes, edges, events, findings, IDs, and JSON-relevant collections
  deterministically.
- [x] Represent partial collection and `unknown` health; suppress endpoint
  conclusions when EndpointSlice collection fails.
- [x] Stop fetching Secret objects. Secret existence remains explicitly
  unknown until a safe metadata-only approach is selected.
- [x] Separate `impact` (`current`, `risk`, `historical`) from severity and
  confidence.
- [x] Treat an undeclared numeric port as risk, not proof of an outage; require
  a matching connection-refused event for a confidence upgrade.
- [x] Correct OOM language: `OOMKilled` without a container limit does not prove
  a cgroup limit kill; a recovered Ready Pod receives historical impact.
- [x] Report ambiguous `Error/137` as `container-sigkill`; OOM remains an
  investigation hypothesis rather than a confirmed finding.
- [x] Cover cross-node, cross-workload, cross-container, EndpointSlice-null,
  API-denial, Secret-data, event-order, and deterministic-output cases.
- [x] Discover and report the Kubernetes API server version for live
  inspections.
- [x] Put built-in rules behind a validated registry with unique IDs, family,
  description, exclusive finding-type ownership, originating rule ID on
  findings, and explicit reviewed Kubernetes ranges. Undeclared emitted types
  are discarded as health-blocking engine errors.
- [x] Skip rules outside their reviewed Kubernetes range instead of assuming a
  new or old minor is compatible; expose every skip and make missing rule
  coverage health-blocking.

Still required:

- [x] Build a registry-parity rule matrix where every built-in rule has direct
  positive and meaningful negative/boundary tests, and every finding type it
  declares has a positive contract. Adding a rule or finding type without a
  contract now fails the suite.
- [x] Add exact tests for init-container image-pull paths, optional references,
  selector matches with scaled-to-zero workloads, multiple EndpointSlices,
  `publishNotReadyAddresses`, terminating endpoints, EventSeries timing, and
  projected ConfigMap references.
- [x] Cover init-container crash/OOM paths with container-specific correlation.
- [x] Collapse duplicate missing-config references per container while keeping
  every usage as evidence.
- [x] Extend EndpointSlice boundary coverage to address type, missing address,
  per-Service-port matching, and multi-port slices.
- [x] Document the current API/RBAC contract, including the absence of Secret
  permissions and the separate cluster-scoped Node permission.
- [x] Replace the namespace-wide Event read with exact involved-Pod field
  selectors, requested only for related Pods that are not Ready.
- [x] Derive and document per-entry-point operations so Service, Deployment,
  and Pod users do not need to copy the aggregated Role blindly.
- [x] Remove the namespace-wide candidate-Deployment list and the heuristic
  `service-selector-mismatch` claim. Report the factual
  `service-selector-no-pods` state without guessing operator intent.
- [ ] Measure and reduce the remaining namespace-wide Service list used for
  reverse selector discovery on Deployment and Pod entry points. Kubernetes
  exposes no reliable server-side query over `Service.spec.selector`, so a
  narrower implementation must not approximate with metadata labels. Record
  API call count, payload size, collection duration, and timeout/partial
  behavior against small and large synthetic namespaces before choosing an
  optimization.
- [x] Keep Secret existence permanently `unknown` in the default collector and
  request no Secret permissions. A future opt-in metadata-only experiment must
  remain outside the default RBAC contract and prove that it never decodes
  payload data.
- [x] Group equivalent replica-level findings in console output only when an
  exact Deployment→ReplicaSet→Pod path, container identity, and causal shape
  match. Keep every affected Pod visible and preserve per-Pod evidence in full
  grouped findings; JSON findings, evidence, and causal IDs remain unchanged.
- [x] Refactor CLI exit handling so health-to-status mapping is unit-testable
  without `os.Exit` in the diagnostic execution path.
- [x] Bound version discovery by the inspection context and cover version
  deadline, canceled focus request, and throttled EndpointSlice behavior.
- [x] Record live collection start/end timestamps and the focus resource's
  start/end `resourceVersion` and `generation` in JSON.
- [x] Re-read the focus resource after collecting related evidence. If its
  identity/revision changed, or the final read cannot establish stability,
  suspend all rule evaluation and return explicit `unknown` health.
- [x] Suspend all rule evaluation while any collected Deployment or ReplicaSet
  has `status.observedGeneration < metadata.generation`; this covers related
  owner controllers as well as a Deployment focus. An unobserved spec update is
  a controller transition, not evidence of an unavailable or stuck rollout.
- [ ] Define and test a bounded temporal-integrity policy for related Pods and
  EndpointSlices. Focus stability plus controller freshness is useful evidence
  but is not an atomic multi-resource snapshot and must not be described as
  one. Compare the cost and permission impact of bounded second LISTs before
  adding extra requests or `watch` access.

Gate A passes only when there are no known factually invalid rules, no known
cross-resource false causal edges, and every missing evidence source is visible
in both console and JSON output.

## Gate B — Validation corpus and real-cluster precision

Unit fixtures are necessary but easy to make self-fulfilling. This gate tests
whether the model survives Kubernetes behavior it was not authored around.

- [x] Make Kind e2e assert the exact root-cause type set, not merely that an
  expected type appears somewhere.
- [x] Add healthy and recovery e2e cases so the suite measures false positives,
  not only detection.
- [x] Run the full healthy, broken, and recovery e2e suite against every minor
  in the provisional window. Today that means 1.34, 1.35, and 1.36; keep it a
  rolling window aligned with maintained Kubernetes release branches. The
  configured matrix was observed green on both initial and follow-up `main`
  workflow runs.
- [x] Configure CI with Kind v0.32.0 digest-pinned images for Kubernetes 1.34.8,
  1.35.5, and 1.36.1, running the exact same e2e contract on each. The gate
  is now mechanically green; corpus precision and environment diversity remain
  open and are not implied by that result.
- [ ] Persist the exact API server version, Node kubelet versions, container
  runtime, and distribution as machine-readable artifacts for every
  corpus/e2e case. CI logs already print API server, kubelet, and runtime data;
  durable case metadata remains open.
- [ ] Create a sanitized corpus of real `get/describe/events` snapshots,
  including incomplete RBAC, stale-event, active-rollout, and focus-change
  cases. Start with at least 25 labeled cases and at least 10 healthy/recovered
  controls.
- [ ] Give every registered rule at least one positive and one meaningful
  negative real-cluster/corpus case; fixtures alone do not validate Kubernetes
  behavior outside the author's assumptions.
- [ ] Maintain a regression case for every reported wrong or missed diagnosis.
- [ ] Record rule precision by corpus case. Do not market a global accuracy
  percentage without a representative labeled dataset.
- [ ] Test clusters with multiple workloads behind one Service, multi-container
  Pods, multiple ReplicaSets, and mixed-version rollouts.
- [ ] Add explicit mixed kubelet-version cases; API server version alone cannot
  establish component-level semantics during upgrades.
- [ ] Run a structured field trial with 3–5 external operators on clusters the
  author did not construct; record useful chains, false roots, misses, partial
  evidence, collection duration, and whether kdiag shortened diagnosis time.

Gate B passes when the known corpus has no high-confidence false root cause,
healthy/recovered cases remain healthy, and at least a small number of external
operators have validated results on clusters the author did not construct.

## Gate C — Stable evidence contract

The machine-readable output can become more durable than the console and is a
better integration surface than prematurely building a TUI or AI layer.

- [ ] Publish a JSON Schema with explicit schema versioning.
- [ ] Stabilize resource references, subject/component identity, impact,
  confidence, evidence, partial warnings, collection bounds/focus stability,
  rule suspension, and causal-edge representation.
- [ ] Add golden compatibility tests and document which fields are stable.
- [ ] Add rule metadata (ID, family, trigger, evidence requirements, causal
  predicates, compatibility/capability requirements) and generate the rule
  reference from code. ID, family, description, owned finding types, and minor
  range are already in the internal registry; the full contract remains open.
- [ ] Publish a normalized, versioned, data-minimized graph snapshot that rule
  consumers can use without importing kdiag internals or obtaining a
  Kubernetes client.
- [ ] Evaluate a small MCP or stdin/stdout adapter only after the JSON contract
  is stable; it must expose structured evidence, not free-form guesses.

Gate C passes when another program can consume results without importing
kdiag's Go internals or scraping console text.

## Gate D — External rule authoring pilot

Only after Gates A and C, with Gate B providing the validation loop:

- [ ] Write an ADR comparing bounded CEL/declarative packs, WASM, and a
  versioned out-of-process protocol. Native Go `plugin` shared objects are not
  a candidate.
- [ ] Define namespaced external rule IDs, pack/schema compatibility,
  provenance (name/version/checksum), duplicate handling, and strict parsing.
- [ ] Let the first pilot emit standalone findings only. External rules cannot
  override/suppress built-ins, upgrade confidence, request cluster data, or
  create arbitrary causal edges.
- [ ] Enforce evaluation time/cost and output-size limits; never auto-download
  or auto-execute packs from a cluster resource.
- [ ] Add a constrained causal vocabulary later only if real rules need it;
  predicates remain owned and verified by the core engine.
- [ ] Test malicious, incompatible, slow, duplicate-ID, unknown-field, and
  non-deterministic packs before calling the format usable.

Gate D passes when an independently authored pack can add a useful finding
without gaining cluster credentials, weakening a built-in diagnosis, or making
the same snapshot produce unstable output.

## Gate E — Carefully selected coverage

More rules come after precision, and only when they deepen a causal chain.

Likely next slices:

- PVC Pending / unbound storage into Pod scheduling or startup symptoms.
- HPA metric unavailable and at-limit state into Deployment capacity symptoms.
- StatefulSet ownership and rollout semantics, designed independently from
  Deployment assumptions.
- Job/CronJob failure lifecycle.

Each slice requires its own API semantics review, permission impact, positive
and negative fixtures, and real-cluster reproduction. NetworkPolicy, RBAC, and
Ingress are large enough to require separate design documents.

## Gate F — Public preview and distribution

Only after Gates A and B, and the stable core of Gate C:

- [ ] Choose a plugin name after checking Krew naming and existing plugin
  collisions; `diag` may be too generic.
- [ ] Add security, support, contributing, compatibility, and RBAC documents.
- [ ] Cut the first explicitly pre-1.0 preview tag.
- [ ] Validate release artifacts on Linux and macOS before publishing them.
- [ ] Document and CI-test the contributor/source-install Go toolchain policy;
  binary releases should prevent the current new compiler requirement from
  becoming unnecessary end-user friction.
- [ ] Submit to Krew only after the binary and name are stable enough that
  discovery does not amplify a misleading tool.

The existing GoReleaser and Krew files are inactive scaffolding, not evidence
that a release exists.

## Explicitly deferred

These are not current work:

- Manifest linting, SARIF, and a GitHub Action. This is a crowded category and
  does not prove the live causal-diagnosis thesis.
- Prometheus integration. First make Kubernetes-native evidence honest.
- Native Go shared-object plugins. Their ABI/platform coupling and in-process
  credential access are the wrong trust boundary.
- Shipping an external rule-pack format before Gate C. Declarative packs are a
  Gate D experiment, not a current feature.
- AI explanation. If added later, it may explain structured findings but must
  never create or upgrade them.
- TUI, broad observability features, automated remediation, and cluster writes.

## Decision log

| Decision | Reason |
|---|---|
| No release version or Krew submission yet | Distribution multiplies both value and mistakes. Precision gates come first. |
| Remove PDB rollout diagnosis | Deployment/StatefulSet rolling updates are not Eviction API operations; the rule encoded a false mechanism. |
| Replace selector-mismatch inference with selector-no-pods | A label-near Deployment does not prove operator intent; the empty selector result is factual and needs no namespace-wide Deployment scan. |
| Directional typed correlation | Undirected proximity allowed unrelated workloads, Pods, and Nodes to contaminate one another. |
| Container-level subjects | A Pod is too coarse for multi-container causal claims. |
| Explicit unknown/partial state | Missing evidence and evidence of absence are different facts. |
| Do not GET Secrets | The typed call returns data, which is unnecessary and expands RBAC/security risk. |
| Secret existence stays unknown by default | Even a metadata-only request needs Secret `get` permission, which grants the caller access to full payloads outside kdiag. |
| Live diagnosis before manifest mode | Runtime cross-resource causality is the product hypothesis; static manifest linting is already crowded. |
| Go rules before a DSL | The rule and evidence model must emerge from enough correct cases before being generalized. |
| Version ranges fail closed | API stability reduces risk but does not prove unchanged diagnostics, feature gates, events, or component behavior. |
| In-tree registry before external packs | Contributors can improve coverage now without freezing an unsafe public execution contract. |
| No native Go plugins | ABI fragility and in-process access to cluster credentials are unacceptable extension boundaries. |
| Suspend on focus transition or stale Deployment generation | Sequential GET/LIST calls can span a rollout; mixed-time evidence cannot support a trust-first diagnosis. |
| Focus stability is not an atomic snapshot claim | Kubernetes does not provide a single transaction across the resource kinds kdiag reads; related-resource temporal policy remains an explicit open gate. |

## Success signals

In order of importance:

1. Zero known high-confidence false root causes in the regression corpus.
2. Healthy and recovered cases remain healthy across supported Kubernetes
   versions.
3. Partial RBAC produces explicit unknowns instead of false absence findings.
4. An external operator says the chain shortened time-to-diagnosis and can show
   which evidence made it useful.
5. Wrong/missed diagnoses become reproducible regression cases quickly.
6. Only then: repeat users, external contributions, stars, and distribution.
