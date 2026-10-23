# Validation corpus

Tuna's corpus is a labeled regression set for diagnostic outcomes. Its job is
to make wrong roots, missed roots, false degradation, and hidden uncertainty
reproducible. It is not a collection of examples for inflating an accuracy
number.

## Evidence classes

Corpus cases must state where their evidence came from:

- `synthetic-fixture`: authored Kubernetes objects evaluated with client-go's
  fake tracker. Useful for deterministic contracts; not cluster validation.
- `live-reproduction`: a deterministic fault and recovery observed through a
  real Kubernetes API server. Useful for API and controller behavior, but still
  authored by the project.
- `sanitized-field`: data captured from an operator's real incident and
  reviewed after anonymization. This is the strongest regression evidence for
  behavior the author did not anticipate.

The current machine-readable seed manifest contains nine
`synthetic-fixture` snapshots and ten inspections. One snapshot is inspected
from both Service and Pod entry points; it still counts as one evidence case.
Only one inspection is a healthy control. These numbers are deliberately not
presented as Gate B progress toward 25 realistic cases and 10 healthy/recovered
controls.

Run the seed contract with:

```sh
make corpus
```

Each inspection asserts the exact root-cause type set as well as health and
partial status. A new unexpected root therefore fails the corpus even if the
expected diagnosis is also present.

## E2E evidence artifacts

Every CI Kubernetes-minor job uploads one `tuna-e2e-kubernetes-*` artifact.
It contains the exact API server build data, distribution label, Node kubelet
versions, container runtimes, operating-system details, and the JSON result for
each completed scenario phase. It also records the exact Tuna source commit,
Go toolchain, expected Kubernetes patch, and digest-pinned Kind image. A
successful run emits `compatibility-baseline.json`: a deterministic summary of
health, partial state, root types, finding types, warnings, and rule execution
with resource and Node identities removed. Raw artifacts are retained for 30
days; selected reviewed summaries can be committed under
`testdata/compatibility/baselines/` for durable historical comparison.

The summary generator rejects dirty Tuna source trees, unpinned Kind images,
API-server version mismatches, inconsistent root markers, and malformed case
results. Run its focused tests with `make e2e-baseline-test`. A summary remains
project-authored live-reproduction evidence; compacting it does not turn it
into independent field evidence or a replayable corpus case.

For local real-cluster work, list scenarios with `make e2e-list` and run only
the affected one with `make e2e E2E_CASE=<case>`. `make e2e-full` is reserved
for an explicit integration milestone. Targeted runs still retain raw evidence
but cannot emit a durable compatibility baseline; only a successful `--all`
run with exact version and image provenance may do that.

These are `live-reproduction` observations because a real API server and
controllers produced the state. They are still project-authored scenarios, not
independent field incidents, and the result JSON alone is insufficient to
replay collection. Therefore they improve version/environment traceability but
do not count toward the sanitized-field corpus target.

## Case acceptance

A durable real-cluster case should contain:

1. a stable case ID, evidence class, collection time, and anonymization notes;
2. exact focus kind, namespace alias, and name alias;
3. API server version, distribution, Node kubelet versions, and container
   runtime when they were observable;
4. sanitized typed API objects used by Tuna, including Events and collection
   failures; raw `describe` output may be retained as operator context but is
   not a substitute for typed evidence;
5. expected health, partial state, exact root-cause types, and a short human
   rationale established independently of Tuna's output;
6. recovery or healthy-control evidence whenever the incident permits it.

Secrets must never enter the repository. Remove payloads, tokens, addresses,
UIDs, organization names, and unrelated metadata before review. Anonymization
must preserve selectors, owner relationships, container identity, status,
conditions, resource quantities, relevant timestamps, and event semantics or
the case no longer tests the original mechanism.

## What the corpus may support

Per-rule precision or false-positive counts can be reported once the dataset
contains enough independently labeled cases. A global accuracy percentage is
not defensible until sampling is representative of actual operator workloads.
Fixture pass rate, Kind e2e pass rate, and corpus precision are separate facts
and must remain separate in documentation.
