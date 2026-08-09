# Evaluator guide

This guide is for selected evaluation of Tuna **from source** while the project
remains experimental and unreleased. It is not installation or support
documentation for a release. Snapshot binaries, Krew packages, and supported
versions do not exist yet. Sharing this repository does not invite broad
outreach or imply a compatibility guarantee.

## Build and run from source

Use the exact Go toolchain named by the `go` directive in
[`../go.mod`](../go.mod). That directive is authoritative and is what CI
reads.

```bash
go test ./...
go build -o ./bin/kubectl-tuna ./cmd/kubectl-tuna
```

Inspect with the built binary directly:

```bash
./bin/kubectl-tuna inspect service NAME -n NAMESPACE
./bin/kubectl-tuna inspect deployment NAME -n NAMESPACE
./bin/kubectl-tuna inspect pod NAME -n NAMESPACE
```

To exercise the kubectl plugin name without making `make install-plugin` the
only path, place `kubectl-tuna` on your `PATH` (for example by copying or
symlinking `./bin/kubectl-tuna`), then run:

```bash
kubectl tuna inspect service NAME -n NAMESPACE
```

`make install-plugin` remains a convenience for local PATH installs only. It
does not publish anything.

Machine-readable output (`-o json`) is useful for capture, but the JSON schema
is **not** stable while the project is pre-release. Do not treat the shape as a
contract.

## Permissions

Grant only the read access needed for the entry point you will exercise.
Per-entry-point operations are documented in [`rbac.md`](rbac.md). Prefer that
table over applying the aggregated Role blindly. Node `get` is optional and
cluster-scoped; omit it unless you intentionally want node-pressure
enrichment.

Tuna is read-only by intent: it does not create, update, delete, exec, fetch
logs, or watch. It also does not fetch Secret objects. Missing permissions
should surface as partial evidence or `unknown` health rather than silent
absence.

## What to record

Capture enough context to judge the diagnosis without granting broader cluster
access than the inspection needs:

| Field | Notes |
|---|---|
| Tuna commit | Exact git commit of the binary you built; do not report a release version |
| Invocation | Focus kind plus anonymized namespace/name identities |
| Environment | Kubernetes API server version/distribution; kubelet/runtime when relevant |
| Expected diagnosis | Established independently of Tuna (your own investigation) |
| Actual result | Roots, health, partial/`unknown` notes, confusing wording |
| Outcome category | See below |
| Usefulness | Whether the chain shortened diagnosis time, and how |

### Outcome categories

Use one primary category per report:

- **useful chain** — evidence-backed causal chain that helped diagnosis
- **wrong root** — confident or presented root that was factually incorrect
- **missed diagnosis** — a real current problem Tuna should have found
- **healthy false positive** — healthy/recovered control reported as a current problem
- **abstention/unknown** — Tuna correctly or incorrectly returned `unknown` / abstained
- **partial evidence** — incomplete collection shaped the result in a notable way
- **installation failure** — could not build or invoke from source as documented
- **confusing output** — result was hard to interpret even if not clearly wrong

### Evidence classes (do not conflate them)

- **Author-led observations** — the maintainer inspecting clusters or failures
  not encoded as fixtures. Useful sequencing signal; not independent validation.
- **Project-authored live reproductions** — Kind/e2e and similar scenarios the
  project constructed. They exercise real APIs/controllers but still encode
  author assumptions.
- **Independent external feedback** — evaluator reports on clusters and
  incidents the project did not construct. This is the validation class Gate B
  still needs.

## Anonymization before sharing

Never include Secret objects or payloads, tokens, certificates, kubeconfig
contents, raw UIDs, internal addresses or hostnames, private image registries,
organization or customer names, or unrelated objects.

When you must share structure, preserve diagnostic meaning with consistent
aliases:

- selectors and labels (rewrite values consistently so relationships still match)
- owner relationships (Deployment → ReplicaSet → Pod, Service selection)
- UID equality or inequality as consistent aliases when object identity matters
- container identity
- relevant status and conditions
- resource quantities
- timestamps and event order
- event reason and semantics
- API failures and denials
- Kubernetes distribution and version
- kubelet and container runtime when they matter to the case

Raw `kubectl get`, `describe`, and `events` output often contains sensitive
metadata and must be reviewed line by line before sharing. Tuna JSON is **not**
automatically safe to publish merely because Tuna does not fetch Secrets;
resource names, labels, messages, and environment details can still identify a
cluster or tenant.

Prefer the smallest sanitized excerpt that still supports the outcome you are
reporting. A minimal synthetic reproduction is better than a wide namespace dump.

## Where to send reports

- Diagnostic usefulness and failure outcomes: open the structured
  [diagnostic feedback](https://github.com/leventozen/kubectl-tuna/issues/new?template=diagnostic-feedback.yml)
  issue form. Reports are evidence, not a support ticket; there is no promised
  response time.
- Suspected vulnerabilities or sensitive unreproducible evidence: follow
  [`../SECURITY.md`](../SECURITY.md) and use private vulnerability reporting.
  Do not file those as public issues.
