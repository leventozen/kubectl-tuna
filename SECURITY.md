# Security policy

## Project status

kdiag is experimental, pre-release software. There are no supported versions
or published release artifacts yet. Security fixes currently land on `main`;
this does not make `main` a supported production release.

## Reporting a vulnerability

Please do not open a public issue for a suspected vulnerability.

Use GitHub's private vulnerability reporting flow:

<https://github.com/leventozen/kdiag/security/advisories/new>

Include the affected commit, impact, reproduction steps, and any proposed
mitigation. If the report involves Kubernetes evidence, remove credentials,
Secret values, tokens, certificates, internal hostnames, cluster identities,
and unrelated workload data before submitting it. A minimal synthetic
reproduction is preferred.

You should receive an acknowledgement within seven days. Timelines for
validation and remediation depend on severity and maintainer availability.
Please allow time for a fix before public disclosure.

## Security boundaries

kdiag is intended to be read-only, but read access to Kubernetes metadata can
still expose sensitive operational details. The current collector does not
fetch Secret objects, execute commands in containers, read logs, or send
telemetry to an external service. Review the documented RBAC contract before
using it against a cluster:

<https://github.com/leventozen/kdiag/blob/main/docs/rbac.md>

Reports about incorrect diagnosis are also important, especially a confident
false root cause or a failure to represent unavailable evidence as `unknown`.
If the report does not expose a security weakness or sensitive cluster data,
it may be filed as a normal issue after the repository becomes public.
