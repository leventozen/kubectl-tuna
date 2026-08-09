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
2. Run `go test ./...`.
3. Run `go vet ./...`.
4. When diagnostics, rules, or seed-corpus fixtures change, also run
   `make corpus`.

Run Kind/e2e (`make e2e` or `examples/e2e.sh`) only when the change affects
relevant live Kubernetes behavior, and only against a disposable cluster.

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
