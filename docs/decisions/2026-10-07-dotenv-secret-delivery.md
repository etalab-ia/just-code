# D-003: Project dotenv secret delivery contract

Date: 7 October 2026
Status: accepted for M1 design; runtime qualification remains required
Tracking: #145

## Context

Project `.env` files are intentionally excluded from the sealed workspace
transfer. Microsandbox secrets are a separate mechanism: the guest receives a
placeholder and the network proxy substitutes a host-held value only for
allowed destinations. This cannot satisfy application-local consumers such as
database drivers or signing libraries.

The pinned Go SDK is v0.7.6. Its `SecretEnvOptions` and
`SecretModifySpec` both expose a custom `Placeholder`; the latter also
supports host environment references. Therefore a guest dotenv file may
contain a unique placeholder while the host value stays out of the transferred
workspace and persisted SDK configuration.

## Decision

- Treat import, approval, and delivery as separate operations.
- Identify an imported value by project, profile, source file, and key; the
  key name alone is not a credential-store identity.
- For proxy-protected values, use a stable unique guest variable and
  placeholder per imported identity. A generated guest dotenv target contains
  the placeholder, never the stored value.
- Resolve the stored value only around the SDK call through a temporary host
  environment reference. Preserve the existing inert bootstrap value on the
  create-only SDK path.
- Keep network destinations explicit and host-local. Do not infer them from
  variable names or dotenv contents.
- Keep guest-readable values out of this first path. They need an independent
  delivery design and an explicit warning that the agent can read them.
- Continue to exclude source `.env` files from transfer and never mutate them.

## Qualification boundary

The pinned SDK source verifies custom placeholder fields on create and modify;
just-code now pins that adapter contract in unit tests. That is API/serializer
evidence, not proof of live proxy behavior for several placeholders, overlapping
allowed hosts, dotenv parser expansion, or application-specific clients.
Retain the existing disjoint-host validation until the pinned runtime is
exercised with canary secrets. Do not enable body/query substitution: the
existing qualified policy is headers only. A client that signs or otherwise
transforms the placeholder is unsupported unless separately qualified.

## Consequences

M1 can represent project-scoped bindings without colliding on common guest
variable names. Import metadata and generated guest targets remain to be
implemented. The first delivery slice must fail closed for missing approvals,
unavailable stores, unqualified clients, and unsafe target paths; it must not
fall back to plaintext guest delivery.
