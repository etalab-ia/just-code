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

That guarantee covers the **delivery**. It does not cover every later use of
the value, and one such use is a real leak: the runtime persists the stdout of
guest executions in its own log (`logs/exec.log` under `MSB_HOME`), so a
command that *prints* a guest-readable value — a plain `cat .env` — copies it
into a persisted host log. The property is therefore "delivery writes no log",
not "the value can never reach a log", and just-code itself must not read a
managed file back through guest stdout. Comparing content without exposing it
means comparing a digest the guest computes, not reading the file.

That last sentence was written here before it was true in the code: the
inspection path still used `cat` to read a managed file back, so a reconciled
start or a rotation — anything that compares an already-delivered file — copied
the real value into `logs/exec.log`, on the host, in plaintext, and that log
survives the sandbox. The comparison now happens inside the guest: the digest
of the content this run wants is compared against `sha256sum` of the file by
the guest's own shell, and the guest prints only `managed=yes|no` and a token
when the digests match. Nothing prints content, and the value never becomes an
argument either, so it cannot reach the guest process table.

The expected digest travels on the command's standard input, not in argv, and
that is not a detail: the same log records exec argv, so a digest of the
secret written as an argument would land in the host log this section is
about. A digest is not the secret, but for a value with little entropy it is
enough to test guesses offline, and keeping it off argv costs nothing. The
first line of the file is compared as-is rather than with carriage returns
stripped: normalising would let a file whose first line reads
`# just-code mana\rged: do not edit` pass as just-code's own, which is a way
to have a user's file overwritten.

One limit is deliberate and stated rather than papered over: this proves the
file's content is the one just-code wanted, not who wrote it. If a sandbox is
rebuilt and a file at the same path happens to hold the same bytes, the plan
reads it as unchanged. The forgeable case is narrow — rebuilding a sandbox and
writing the exact expected bytes there is strictly more work than writing the
original file would have been, so a caller able to do it is already inside the
guest — but it is a limit, not a proof. Telling the two apart needs a secret
this sandbox carries, which the guest environment cannot hold without putting
it where any guest process can read it; that is a channel decision for a later
phase, not a line of code here.
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
- **Single quotes** carry newlines, whitespace, `#`, double quotes, backslashes
  and the empty value exactly in all four configurations.
- Two classes cannot be represented without choosing one loader and breaking
  another. A **single quote** inside the value: `godotenv` refuses the whole
  file (Node and Python read it, so the refusal is driven by the strictest
  loader). A **`$`**: expanded by `dotenv-expand` and `godotenv`, literal for
  plain `dotenv`, and the usual escape `\$` is then left as a literal backslash
  by plain `dotenv` and rejected by `python-dotenv`.

Both classes are therefore **refused**, at approval time so the user is
present, naming the variable and the character. A refused secret is a nuisance;
a silently altered one fails far from here. The round-trip is checked against
real loaders rather than asserted: `godotenv` in
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
