# D-003: Project dotenv secret delivery contract

Date: 7 October 2026
Status: initial contract; phase 0 qualification and schema remain open
Tracking: #145; phase 0 #148; phase 1 #147

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

## Phase 1 Storage Proposal

The existing project identity is a canonical checkout root, not the optional
`Project` manifest label. The storage foundation uses the full SHA-256 digest
of that root as a host-local namespace, independently of the short VM-name
suffix. Symlink aliases resolve together; separate checkouts, worktrees, moved
roots, and machines do not implicitly share entries. Explicit migration or
sharing is outside this initial implementation.

Within that namespace, an explicit profile, normalized relative component file,
and variable name identify a secret. The index is local to the host and carries
only opaque credential references. There is no global-name or cross-profile
fallback. The manifest is unchanged by this foundation.

Each authorized component replacement prepares new store entries under opaque
references, recording a metadata-only journal first. Atomic index publication
selects the live generation; recovery removes either the uncommitted entries or
the retired generation, never entries referenced by the live index. Cleanup
failure remains visible and recoverable. Operations serialize through the
existing host-local project lock primitive.

Values are encoded inside the credential store to preserve literal empty,
multiline, and whitespace-containing values across native backend conventions.
This is encoding, not encryption: the native store provides the protection;
the explicitly consented file fallback is still plaintext-equivalent. Store
diagnostic text is suppressed because even an encoded value is sensitive.

Phase 1 adds a preview-first `secrets import` command with bounded project
discovery, explicit file selection, a huh multi-select on terminals, and a
non-interactive path requiring `--profile` and `--file`. Apply is separately
confirmed (and requires `--yes` without a terminal). Parsing uses the
maintained `godotenv` parser with dollar references masked so interpolation is
not evaluated; parser errors are redacted. Repeated keys are last-wins and
identified in the preview. Native-store portability limits each value to
951 UTF-8 bytes (the base64 envelope becomes a UTF-16 Windows credential blob
capped at 2,560 bytes); a dotenv file is capped at 1 MiB. These constraints are surfaced
before storage, never by truncation.

The command only stores values under the current canonical-root scope. It
does not create a project manifest entry, approve a binding, or deliver
anything to a guest. The user documentation is in `docs/credentials.md`.

M1 comprises phases 0–3; M2 comprises phases 4–5, not this storage foundation.
