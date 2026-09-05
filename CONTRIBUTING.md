# Contributing to Tuna

Tuna is experimental, pre-release software. Contributions are welcome as
focused improvements, but opening a pull request does not imply acceptance,
review turnaround, or support for any release. There is no supported version
and no published release artifact yet.

## Go toolchain

The required Go toolchain is the exact `go` directive in [`go.mod`](go.mod).
CI reads that same directive. Treat a change to it as a deliberate
compatibility decision, not a casual bump; `go.mod` remains the single
authoritative version source.

## Local checks

Before opening a pull request:

1. Keep Go sources formatted (`gofmt`).
2. Run `make verify`; it covers Go unit tests/vet plus cluster-free shell and
   baseline tooling checks.
3. When diagnostics, rules, or seed-corpus fixtures change, also run
   `make corpus`.

Run targeted Kind/E2E only when the change affects relevant live Kubernetes
behavior, and only against a disposable cluster:

```sh
make e2e-list
make e2e E2E_CASE=healthy-service
```

`make e2e-full` is an explicit integration milestone, not a normal local
pre-commit check. The full scenario × Kubernetes-version matrix runs in CI.

## Rule contributions

New diagnostics currently require an in-tree Go rule with the same review bar
as built-ins. Arbitrary external YAML, shared libraries, or executables are not
loaded today. Summarized expectations:

- one narrow Kubernetes mechanism and a stable rule ID;
- structured evidence first; Event text only as bounded support;
- positive, negative, boundary, and adversarial tests, including at least one
  plausible non-diagnosis;
- causal edges only when a specific directed Kubernetes relationship proves
  them;
- no Secret payload reads, Pod exec, log fetches, or cluster writes for
  convenience.

The full contract is in
[`docs/extending-rules.md`](docs/extending-rules.md). Prefer linking and
following that document over copying its checklist into a pull request
description.

## Pull request expectations

- Keep the change focused on one problem.
- Include tests that would fail without the new or corrected behavior.
- Update documentation and the RBAC contract when collection needs, operator
  guidance, or permissions change. See [`docs/rbac.md`](docs/rbac.md).
- Do not commit generated release artifacts, binaries, checksums, or Krew
  packaging. Distribution remains gated.

## Where to report issues

- Suspected vulnerabilities or sensitive cluster evidence: follow
  [`SECURITY.md`](SECURITY.md). Do not file them as public issues.
- Evaluator outcomes (useful chains, wrong roots, misses, confusing output,
  source-build or snapshot friction): use the structured diagnostic feedback
  form and the guidance in [`docs/evaluator-guide.md`](docs/evaluator-guide.md).
- Reproducible CLI, collection, rendering, build, documentation, or contributor
  tooling defects: use the bug report form.
- New diagnostic mechanisms: use the rule proposal form after reading
  [`docs/extending-rules.md`](docs/extending-rules.md). General product or
  workflow improvements belong in the feature request form.
