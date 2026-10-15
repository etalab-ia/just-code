# D-004: Guest-readable secret delivery contract

Date: 15 October 2026
Status: transport qualified on macOS arm64; implementation pending
Tracking: #145; phase 4 #151

## Context

Phases 1–3 cover secrets the network proxy can substitute: the guest holds an
opaque placeholder and the real value never enters the VM. Some values cannot
work that way. A database password, a signing key or a message-broker
credential is consumed *locally* by the application — there is no outbound
request for the proxy to rewrite — so the plaintext value has to exist inside
the guest.

That changes the security posture, and the change is the point of the phase:
the agent runs in that same guest, so anything delivered this way is readable
by the agent and by every process it can start. The plan therefore requires a
separate, explicit approval with a persistent warning, and forbids a silent
fallback from a proxy-protected secret to a guest-readable one.

Before building anything, the delivery channel was qualified on the pinned
runtime (`tests/integration/project-secrets/native-phase4/`, macOS arm64,
SDK/runtime 0.7.6). The question was not "how do we write a file in the guest"
but "which transport leaks, and where".

## Decision

**Transport.** Values reach the guest through the SDK filesystem write
(`FS().Write`) or an stdin stream. Neither appears in process argv, in the
guest or host environment, in guest `/var/log`, in the persisted runtime log,
in the persisted runtime configuration, or in an intermediate host file. The
channels that do leak are excluded by construction, and the probe demonstrates
them as controls: a value in a shell command is visible in the process table,
and a value supplied inline in the runtime's secret configuration is persisted
in the runtime database.

**Destination.** A guest-readable value is delivered to the path the
application already reads — by default the component's own file in the guest
workspace (`/workspace/.env`, `/workspace/apps/api/.env`), matching where the
manager's generated dotenv files already go. Writing a file into the workspace
is the ordinary case this feature exists to serve, and it is explicitly not a
violation of anything: the guest's disk is a host-side image, so *any* file the
guest writes lives in a host file. What the contract forbids is an
*intermediate* host file used to stage the value on its way in.

**Permissions.** Files are created `0600`. The runtime writes `0644` by
default, so the mode is set explicitly and verified, and the value is delivered
only after the file exists with the restrictive mode.

**Encoding.** A value is written as a single-quoted dotenv literal. This is not
cosmetic: dotenv has no escaping that every loader agrees on, and the failure
is not confined to the variable. Measured against the loaders just-code pins
(Node `dotenv` 16.4.7 with and without `dotenv-expand` 12.0.2, `python-dotenv`
1.2.2, `godotenv` 1.5.1):

- A **raw** value containing a newline makes `godotenv` refuse the *whole file*,
  so one PEM-shaped secret would take down every other secret in that `.env`.
  Leading or trailing whitespace, `#`, quotes and backslashes are truncated or
  transformed. The importer deliberately preserves values that do this.
- **Single quotes** carry newlines, whitespace, `#`, double quotes, an interior
  backslash and the empty value exactly in all four configurations. A
  backslash is only safe on its own, between ordinary characters.
- Four classes cannot be represented without choosing one loader and breaking
  another, so they are **refused**, at approval time so the user is present,
  naming the variable and the character. A refused secret is a nuisance; a
  silently altered one fails far from here.
  - A **single quote** inside the value: `godotenv` refuses the whole file.
  - A **`$`**: expanded by `dotenv-expand` and `godotenv`, literal for plain
    `dotenv`, and the usual escape `\$` is then left as a literal backslash by
    plain `dotenv` and rejected by `python-dotenv`.
  - **Two consecutive backslashes**: `python-dotenv` silently collapses them to
    one, so the application would receive a different secret than the one
    approved.
  - A **trailing backslash**: it escapes the closing quote. `python-dotenv`
    then loses the value *and the following line*, and `godotenv` refuses the
    whole file. The shape matters and is the one a generated file always has:
    the value is followed by another quoted value.

The first two were found by reading the loaders; the last two came from a
native qualification kit and were confirmed by measurement, after an earlier
version of this record claimed single quotes preserved backslashes "exactly in
every loader" on the strength of an interior backslash alone. The round-trip is
now checked against real loaders rather than asserted: `godotenv` in
`internal/justcode/project_secret_encoding_loaders_test.go`, and Node and
Python in `tests/integration/project-secrets/`.

**Snapshot lifecycle.** A snapshot taken while a guest-readable value is
delivered captures it, and deleting the file afterwards does not remove it from
snapshots already taken. This is ordinary snapshot behaviour, not a defect, and
it is a documented property of the feature rather than a reason to avoid the
workspace: an operator who snapshots a guest holding live credentials has
snapshotted those credentials. Revocation therefore means "removed from the
live guest", never "gone everywhere". A value that must not be captured can be
delivered to a RAM-backed target instead, which snapshots do not contain and a
restart clears — at the cost of re-delivering it at every boot.

**Approval.** Guest-readable delivery is a distinct approval from the proxy
bindings, recorded host-locally beside them, per project/profile/component/
variable. Enabling it prints a persistent warning, repeated at each start, that
the value is readable inside the guest by the agent and anything it runs. There
is no automatic downgrade from a proxy-protected binding to a guest-readable
one, and no implicit delivery: a value is not readable in the guest until it is
explicitly approved for that.

**Lifecycle.** The value is (re-)delivered on every start, so a restarted guest
and a RAM-backed target are always repopulated. Removing the approval removes
the delivered file from the guest. The host-local inventory of what is
currently delivered lives beside the existing binding records, never in the
project, and never in the instance state that reconciliation rewrites.

## Qualification boundary

The probe measures the pinned runtime on macOS arm64 with synthetic canaries; it
does not prove absence in host process memory (the value necessarily transits
the host process over the FFI boundary), and the macOS unified-log check is
weaker than the others because `log show` may redact values as `<private>`.
Absence is measured at the points listed above, before, during and after a
snapshot and after teardown — not continuously.

Values that an application must consume locally are, by construction, readable
by the agent. That is accepted here with explicit consent, not mitigated.
