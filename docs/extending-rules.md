# Extending kdiag rules

kdiag should become extensible, but it is not a third-party rule platform yet.
The current contribution path is an in-tree Go rule reviewed and released with
kdiag. Installing an arbitrary YAML file, shared library, or executable does
not add a rule today.

That limitation is intentional while the graph, evidence, finding, and JSON
contracts are pre-release. Freezing the wrong extension API would make false
diagnoses harder to correct and would transfer kdiag's credibility to code it
cannot constrain.

## What exists now

Each built-in rule implements `diag.Rule` and is registered with immutable
metadata in [`internal/diag/rules_common.go`](../internal/diag/rules_common.go).
[`internal/diag/registry.go`](../internal/diag/registry.go) rejects:

- a missing implementation, ID, family, description, or Kubernetes range;
- a metadata ID that differs from the implementation ID;
- duplicate rule IDs;
- missing finding-type declarations or two rules claiming the same type;
- invalid or inverted Kubernetes minor-version ranges.

The engine records the rule ID on every finding and discards a finding whose
type was not declared by that rule, making the lost coverage visible as
health-blocking incomplete evidence. Before executing a rule it
compares the discovered API server minor version with the rule's inclusive
reviewed range. A rule outside that range is skipped and named in JSON under
`rules.skipped`; it is not optimistically executed against unknown semantics.

The current built-ins declare Kubernetes 1.34 through 1.36. This is a reviewed
semantic window, not a support guarantee. Real-cluster e2e coverage across the
entire window is still a release gate.

## Contributing a built-in rule now

A useful contribution includes all of the following:

1. One narrow Kubernetes mechanism and a stable rule ID. A rule should not be
   a bag of loosely related message patterns.
   It must also declare every finding type it owns; another rule cannot claim
   or impersonate the same type.
2. Structured trigger state. Conditions, status fields, specs, and exact graph
   relationships are primary evidence; Event text can only provide bounded
   support.
3. Findings with severity, confidence, impact, resource, optional container
   subject, verifiable evidence, and actionable recommendations.
4. An explicit Kubernetes min/max minor range backed by API semantics review.
   Do not copy the default range without checking the fields and behavior the
   rule relies on.
5. Positive, negative, boundary, and adversarial tests. At least one test must
   show a plausible condition that the rule must *not* diagnose.
6. A causal predicate only when a specific directed Kubernetes relationship
   proves the connection. New finding types remain standalone if that predicate
   does not exist.
7. Documentation and RBAC changes when collection needs new evidence. Rule code
   must not perform Kubernetes API calls itself; collection and reasoning stay
   separate.

Rules must not read Secret payloads, execute in Pods, fetch logs, or add cluster
writes merely because that data would be convenient. Any expansion of the data
contract is a separate security and product decision.

## Why native Go plugins are not the plan

Go's `plugin` package couples a plugin to operating system support, the exact
Go toolchain, dependency versions, build flags, and compatible package ABIs.
It also loads third-party code directly into the kdiag process with the user's
cluster credentials. That is a poor portability and trust boundary for a kubectl
plugin, so kdiag will not use native Go shared objects as its public extension
model.

## Planned extension sequence

External rule packs come after the reliability baseline and stable evidence
contract, in this order:

1. **Stable normalized snapshot.** Publish a versioned, data-minimized input
   model derived from the focused graph. Packs receive facts already collected
   by kdiag; they do not get a Kubernetes client or Secret data.
2. **Declarative pilot.** Evaluate a bounded expression format such as CEL for
   predicates and finding templates. Parsing is strict, evaluation is cost- and
   time-limited, and unknown fields fail validation.
3. **Standalone findings first.** A pack can initially emit evidence-backed
   standalone findings. It cannot override built-ins, raise built-in
   confidence, suppress findings, or add arbitrary causal edges.
4. **Constrained causality later.** If needed, packs may declare links only from
   a small vocabulary of core-owned predicates such as same-resource,
   same-container, `selects`, `owns`, or `scheduled-on`.
5. **Advanced out-of-process protocol only if justified.** Rules that cannot be
   expressed declaratively may eventually use a versioned stdin/stdout protocol
   with an explicit user-installed executable, handshake, timeout, output-size
   limit, and no automatic download. This is not part of the first pilot.

Every external ID will need a namespace (for example
`example.com/team/rule-name`), and every result will identify pack name,
version, checksum, rule ID, and compatibility decision. Built-in IDs cannot be
overridden.

## Kubernetes version is necessary but not sufficient

The API server minor version is only one compatibility input:

- managed distributions can enable different feature gates;
- API discovery can differ even at the same minor version;
- kubelets can be older than the API server;
- a mixed-version upgrade can expose Nodes with different kubelet behavior;
- events and implementation-specific messages can differ by component or
  runtime.

Future rule metadata therefore needs capability requirements in addition to a
minor range: required API group/versions, required fields or feature state, and
possibly component scope such as kubelet version. A rule must become
`not-applicable` or `unknown` when those requirements cannot be established; it
must not silently assume them.

The Kubernetes project maintains the three latest minor branches and documents
component skew separately. See the official
[release list](https://kubernetes.io/releases/) and
[version skew policy](https://kubernetes.io/releases/version-skew-policy/).
