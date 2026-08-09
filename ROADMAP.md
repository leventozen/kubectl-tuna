# Tuna roadmap

This roadmap is organized around proof and trust, not version numbers. Tuna
will not be submitted to Krew or assigned a supported release version until the
current diagnostic surface has earned that step.

## Product thesis

> Tuna turns disconnected Kubernetes observations into a compact causal
> diagnosis by combining a directed resource graph, deterministic rules,
> visible evidence, and explicit unknown state.

The valuable part is not the number of checks. It is whether an operator can
trust the distinction between current problem, risk, history, propagated
symptom, and evidence Tuna could not collect.

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

## Current status

Tuna has a strong internal reliability baseline but almost no independent
product validation:

- 15 built-in rules own 17 finding types with direct positive and negative
  contracts.
- Eight author-constructed fixture snapshots provide nine inspections and one
  healthy control.
- Ten author-constructed Kind scenarios provide eleven phases: five distinct
  live root-cause patterns (one also exercises cross-container isolation; one
  also exercises same-name Pod Event identity; one also exercises a
  multiple-ReplicaSet rollout boundary), one healthy control, one recovery
  phase, one incomplete-RBAC abstention, and one cross-workload isolation
  boundary.
  Repeating them across three Kubernetes minors validates compatibility
  mechanics; it does not create 30 independent cases.
- There are no sanitized field incidents and no completed external-operator
  trials yet.

The honest product status is therefore: well-engineered, narrow prototype with
a credible thesis, not a validated diagnostic product. Further framework work
has sharply diminishing returns until evidence comes from clusters and
operators the author did not construct.

## Current execution order

1. Keep the public source explicitly experimental: no supported version, Krew
   submission, or broad public-alpha announcement yet.
2. Run owner-led field tests on clusters and failures not encoded as fixtures.
   Record these as author observations, not independent validation.
3. Work through the remaining non-racy real-cluster boundary cases in Gate B2.
   Incomplete RBAC, shared-Service cross-workload isolation, multi-container
   identity, same-name Pod stale-Event identity, and multiple-ReplicaSet
   rollout-target causality are already covered. Universal same-Pod Event
   age/recency is not claimed solved by UID matching.
4. Prepare a friction-light evaluator path without actively recruiting yet:
   the minimal evaluator guide, `CONTRIBUTING.md`, structured diagnostic
   feedback form, and explicit Go toolchain policy are in place; unsigned
   snapshot binaries with checksums remain open.
5. Start external operator trials after the owner-led pass and evaluator path
   are usable. This is a sequencing delay, not a replacement for independent
   validation. Treat
   every rejected root, miss, confusing `unknown`, permission objection, and
   installation failure as product evidence; grow the trial to 3–5 operators
   and build the labeled corpus iteratively with them.
6. Stabilize the JSON/evidence result contract only after the corpus exposes
   which fields real consumers need. External rule packs and additional public
   graph models remain later.

## Publication plan

### Soft-public source — complete

“Soft-public” means the source repository becomes visible and can be shared
directly with selected Kubernetes operators. It does **not** mean a supported
release, version tag, Krew submission, compatibility guarantee, or broad launch
announcement. README must continue to say experimental alpha.

Do not keep the repository private merely to complete every validation goal.
Open it as soon as these minimum safety and honesty blockers are complete:

- [x] All current local work is on remote `main`, and build/unit/race plus the
  full Kubernetes 1.34/1.35/1.36 e2e matrix are green.
- [x] `SECURITY.md` exists with private vulnerability-reporting instructions;
  README remains explicit about experimental status, unsupported versions,
  runtime data boundaries, and the absence of a release/Krew installation.
- [x] Repository history and tracked files have been checked for credentials,
  cluster identities, private incident data, and other material that must not
  become public. Credential, kubeconfig, and private-incident scans are clean;
  commit author and committer e-mails have been rewritten to the maintainer's
  GitHub noreply address.
- [x] GitHub repository description, license display, private vulnerability
  reporting, secret scanning/push protection, and default-branch protection
  are ready for outside readers and contributors.

These are important but do not block source visibility; complete them in
parallel after soft-public:

- [ ] CI follows the latest patch for each maintained minor and records exact
  environment evidence.
- [ ] Snapshot evaluator binaries exist for Linux and macOS on amd64 and arm64,
  with checksums and an explicit “unreleased/unsupported” label.
- [x] `CONTRIBUTING.md`, evaluator guide with anonymization guidance, structured
  diagnostic feedback issue form, and an explicit Go toolchain policy grounded
  in `go.mod` exist.
- [ ] Repository topics and remaining general issue templates exist.
- [x] Deterministic incomplete-RBAC behavior has a real-cluster reproduction;
  partial evidence must not become false absence.
- [ ] External operator outreach is intentionally paused until the owner-led
  field pass and evaluator path are usable. Once started, every factual or
  severe usability failure becomes a regression case or explicit roadmap
  decision.

A broad public-alpha announcement still requires Gate B's minimum independent
evidence. Version tags and Krew remain behind Gates B and C.

## Gate A — Reliability baseline (internal baseline complete)

The Service, Deployment, and Pod implementation has completed its internal
reliability checklist. This status is provisional rather than irreversible:
any corpus or field case that exposes a factual rule error or false causal edge
reopens Gate A immediately.

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

Additional completed reliability work:

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
- [x] Measure the remaining namespace-wide Service list used for
  reverse selector discovery on Deployment and Pod entry points. Kubernetes
  exposes no reliable server-side query over `Service.spec.selector`, so a
  narrower implementation must not approximate with metadata labels. A small,
  local Kind probe found linear response payload growth but no urgent latency
  signal; its sample size and environment are too weak for production claims.
  Keep the correct single LIST until field evidence, timeouts, or throttling
  justify reopening optimization work.
- [x] Add an exact Pod-focus request-budget test and reproducible fake-client
  benchmark for namespaces with 10, 1,000, and 5,000 Services. The benchmark
  reports approximate ServiceList size and local time/allocation growth; it
  intentionally excludes API-server/network latency.
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
- [x] Define the bounded temporal-integrity policy for independently changing
  related resources. Do not add a blanket second collection pass or `watch`
  permission: partial revalidation cannot prove graph atomicity, while a full
  pass doubles broad reads and all other evidence calls. Keep the limitation
  explicit and revisit dependency-aware verification only when active-rollout
  corpus cases or field reports demonstrate a drift-caused false root. See
  `docs/temporal-integrity.md`.

Gate A is internally passed because there are currently no known factually
invalid rules or cross-resource false causal edges, and missing evidence is
visible in console and JSON. This is an internal evidence claim, not proof of
field precision.

## Gate B — Validation corpus and real-cluster precision

Unit fixtures are necessary but easy to make self-fulfilling. This gate tests
whether the model survives Kubernetes behavior it was not authored around.

### Gate B1 — Reproducibility and provenance (mostly implemented)

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
- [x] Add a machine-readable seed-corpus manifest and harness that asserts
  exact root-cause sets, health, and partial state across existing fixtures.
  All eight snapshots are explicitly labeled `synthetic-fixture`; they do not
  count as real-cluster validation or satisfy the corpus-size target.
- [x] Persist the exact API server build, Node kubelet versions, container
  runtimes, operating-system details, and distribution label with every e2e
  matrix run. CI retains the minimized environment record and each completed
  scenario phase's result JSON as per-minor artifacts; assertion expectations
  remain in the executable e2e contract. Sanitized field cases must carry the
  same provenance when they are added.
- [ ] Keep the matrix on the latest patch of each maintained minor and define a
  repeatable review checklist for adding a new minor. Fail-closed compatibility
  becomes an adoption failure if a solo maintainer cannot advance the window
  promptly.
- [ ] Preserve selected compatibility baselines beyond the 30-day CI artifact
  lifetime. Ephemeral workflow artifacts improve debugging but are not a
  durable validation corpus.

### Gate B2 — Independent precision and usefulness (started internally)

- [x] Run a deterministic incomplete-RBAC live reproduction using a restricted
  ServiceAccount. Denied EndpointSlice evidence must produce `unknown` health,
  one explicit warning, and no false zero-endpoint finding.
- [x] Add a deterministic multiple-workloads-behind-one-Service reproduction.
  One ready Pod keeps the Service healthy while two Deployment-unavailable
  findings coexist; the broken Pod must explain only its exact owner through
  Deployment→ReplicaSet→Pod, never the other workload selected by the Service.
- [x] Add a deterministic multi-container Pod reproduction. A recovered,
  memory-limited sidecar termination must remain historical and standalone
  while another container's invalid image alone explains Pod NotReady and
  Deployment unavailable. Accept the runtime's `OOMKilled` or ambiguous
  `Error/137` classification without upgrading the latter into an OOM claim.
- [x] Add a deterministic same-name Pod stale-Event live reproduction. An old
  Pod's `Unhealthy` Event must still exist after recreation, and Tuna must
  diagnose only `image-pull-failure` → `pod-not-ready` with no false
  readiness finding. Exact UID field selection and graph filtering cover this
  object-identity boundary; they do not claim a universal Event freshness
  window.
- [x] Add a deterministic multiple-ReplicaSet live reproduction.
  An old ReplicaSet Pod's readiness failure may explain Deployment
  unavailability but must not explain `rollout-stuck`; only the
  current-template ReplicaSet Pod may. Keep broader recovery/rollout
  boundaries non-gating if controller timing cannot be made deterministic; do
  not encode a race as a passing contract.
- [ ] Run structured trials with 3–5 external operators. Record installation
  friction, useful chains, false roots, misses, abstentions, partial evidence,
  collection time, and whether Tuna shortened diagnosis.
- [ ] Create a sanitized corpus of real `get/describe/events` snapshots,
  including incomplete RBAC, stale-event, active-rollout, and focus-change
  cases. Start with at least 25 labeled cases and at least 10 healthy/recovered
  controls, but treat rule/mechanism coverage as more important than hitting an
  arbitrary count.
- [ ] Give every registered rule at least one positive and one meaningful
  negative real-cluster/corpus case; fixtures alone do not validate Kubernetes
  behavior outside the author's assumptions.
- [ ] Maintain a regression case for every reported wrong or missed diagnosis.
- [ ] Record outcomes per rule and confidence bucket: false roots, misses,
  healthy-control false positives, actionable-chain coverage, and
  `unknown`/abstention rate. Precision alone rewards a tool that always says
  `unknown`; report usefulness and coverage beside it.
- [ ] Add explicit mixed kubelet-version cases; API server version alone cannot
  establish component-level semantics during upgrades.

Gate B passes only when the minimum corpus has no high-confidence false root,
healthy/recovered controls remain healthy, confidence-bucket results and
abstention are reported with their denominators, and 3–5 external operators
have evaluated clusters the author did not construct. At least two must be able
to identify a specific case where the evidence chain shortened diagnosis; a
perfect false-positive score produced mostly by `unknown` does not pass.

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
- [ ] Evaluate a small MCP or stdin/stdout adapter only after the JSON contract
  is stable and a real consumer asks for it; it must expose structured
  evidence, not free-form guesses.

Gate C passes when another program can consume results without importing
Tuna's Go internals or scraping console text.

## Gate D — External rule authoring pilot

Only after Gates A and C, with Gate B providing the validation loop:

- [ ] Write an ADR comparing bounded CEL/declarative packs, WASM, and a
  versioned out-of-process protocol. Native Go `plugin` shared objects are not
  a candidate.
- [ ] Publish a normalized, versioned, data-minimized graph snapshot that rule
  consumers can evaluate without importing Tuna internals or obtaining a
  Kubernetes client. This is an extension-system input, not part of the first
  stable result JSON contract.
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

## Gate F — Public preview release and distribution

Making the source repository soft-public does not pass this gate. A versioned
preview and broad announcement happen only after Gates A and B, and the stable
core of Gate C:

- [x] Use **Tuna** as the product name, `kubectl-tuna` as the repository and
  executable, and `kubectl tuna` as the operator command. Do not ship a bare
  `tuna` executable or retain the already-colliding `kubectl diag` name.
- [x] Remove inactive GoReleaser/Krew scaffolding that hard-coded the unchosen
  `diag` command and placeholder release URLs. Recreate one generated release
  path only after the final repository, binary, and Krew names are settled.
- [ ] Finalize support and compatibility policies. Basic security,
  contributing, evaluator, and RBAC guidance already belong to the soft-public
  checklist.
- [ ] Cut the first explicitly pre-1.0 preview tag.
- [ ] Validate signed release artifacts and checksums on Linux and macOS before
  publishing them. Evaluator snapshots are not release artifacts.
- [ ] Submit to Krew only after the binary and name are stable enough that
  discovery does not amplify a misleading tool.

Release and Krew configuration will be created at this gate, after the name and
artifact contract are stable; placeholder distribution files are deliberately
not kept on `main` in the meantime.

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
- A larger namespace-Service performance campaign. The exploratory local Kind
  sample is not production evidence and did not reveal an urgent bottleneck;
  repeat on remote, throttled, or managed clusters only when field data makes
  this a real problem.

## Decision log

| Decision | Reason |
|---|---|
| No release version or Krew submission yet | Distribution multiplies both value and mistakes. Precision gates come first. |
| Brand Tuna; ship only as `kubectl-tuna` / `kubectl tuna` | The former project name and `kubectl diag` collide with existing Kubernetes diagnostic tools. Bare `tuna` is also an established Linux command; the kubectl-prefixed identity preserves the personal brand without creating an executable collision. |
| Remove PDB rollout diagnosis | Deployment/StatefulSet rolling updates are not Eviction API operations; the rule encoded a false mechanism. |
| Replace selector-mismatch inference with selector-no-pods | A label-near Deployment does not prove operator intent; the empty selector result is factual and needs no namespace-wide Deployment scan. |
| Directional typed correlation | Undirected proximity allowed unrelated workloads, Pods, and Nodes to contaminate one another. |
| Container-level subjects | A Pod is too coarse for multi-container causal claims. |
| Explicit unknown/partial state | Missing evidence and evidence of absence are different facts. |
| Do not GET Secrets | The typed call returns data, which is unnecessary and expands RBAC/security risk. |
| Secret existence stays unknown by default | Even a metadata-only request needs Secret `get` permission, which grants the caller access to full payloads outside Tuna. |
| Live diagnosis before manifest mode | Runtime cross-resource causality is the product hypothesis; static manifest linting is already crowded. |
| Go rules before a DSL | The rule and evidence model must emerge from enough correct cases before being generalized. |
| Version ranges fail closed | API stability reduces risk but does not prove unchanged diagnostics, feature gates, events, or component behavior. |
| In-tree registry before external packs | Contributors can improve coverage now without freezing an unsafe public execution contract. |
| No native Go plugins | ABI fragility and in-process access to cluster credentials are unacceptable extension boundaries. |
| Suspend on focus transition or stale Deployment generation | Sequential GET/LIST calls can span a rollout; mixed-time evidence cannot support a trust-first diagnosis. |
| Focus stability is not an atomic snapshot claim | Kubernetes does not provide a single transaction across the resource kinds Tuna reads; the related-resource boundary remains explicit and must be challenged by active-rollout corpus cases. |
| No blanket second collection pass | Partial revalidation creates false assurance; a full pass doubles broad reads and volatile evidence calls. Corpus evidence must justify dependency-aware revalidation first. |
| Keep one namespace-wide Service LIST for now | Kubernetes has no reliable reverse selector query. A weak local baseline showed linear payload growth but no urgent latency problem, so correctness beats speculative optimization until field evidence says otherwise. |
| Soft-public source is not a release | Visibility enables evaluator feedback without implying a supported version, compatibility promise, Krew readiness, or broad launch. |
| External feedback starts before the corpus count is complete | A corpus authored entirely around the implementation can validate its own assumptions. Operators and field incidents must shape the dataset iteratively. |

## Reassessment triggers

After the first 3–5 external operators, pause new rules, adapters, and extension
work and revisit the product thesis if any of these dominate applicable cases:

- operators do not use Tuna a second time or cannot name a diagnosis it made
  materially faster;
- results are mostly `unknown`, partial, or standalone observations rather than
  useful causal chains;
- the required RBAC is routinely rejected or unavailable;
- misses cluster around resource families outside the Service/Deployment/Pod
  slice, making the current scope too narrow to be useful;
- high/medium confidence does not correspond to materially better precision.

The correct response to those signals may be a narrower positioning, a change
to evidence collection, one carefully selected new resource slice, or stopping
the external-rule plan. More framework is not the default answer.

## Success signals

In order of importance:

1. No high-confidence false root in the representative regression corpus, with
   the denominator and confidence buckets visible.
2. A non-trivial share of applicable inspections produce an actionable chain;
   `unknown`, partial, standalone, and missed outcomes are reported rather than
   removed from the denominator.
3. Healthy and recovered controls remain healthy across supported Kubernetes
   versions, while partial RBAC produces explicit unknowns instead of false
   absence findings.
4. At least two external operators can identify a case where the evidence chain
   shortened diagnosis, and at least one chooses to use Tuna again.
5. Wrong and missed diagnoses become sanitized regression cases quickly.
6. Only then: repeat users, external contributions, stars, a versioned preview,
   and distribution.
