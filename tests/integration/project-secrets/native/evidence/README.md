# Native Qualification Evidence

Date: 2026-10-27, two runs completed at approximately 15:26 and 15:28 UTC.
Platform: macOS 27.0.1 arm64; native Go 1.27.3; SDK/runtime 0.7.11.
Project fixture source: Microsandbox pin commit `35f5d312aed0a3ea2a972e7862a17987650b9c69`,
with the SDK pin fix from `bb79304fe142a3d15447517e765d5b1c82560ab3`.

The Cloud-authored v3 test artifact was downloaded and checksum-verified
before execution on the Mac:

```text
kit SHA256: 2afb0636e8a404ac8baea78fef12aa0e83bcb5964aa1f456dd958a725d946bf2
runtime archive SHA256: 4669f08a31d115cc728ff216a0e5a5f8b8928a599a446b667120ab9ac52be46d
run-1 results SHA256: 4245c72e2dbf47f6172d4f0559c284dc5336443157592385781cbc7523619691
run-2 results SHA256: 4245c72e2dbf47f6172d4f0559c284dc5336443157592385781cbc7523619691
```

Each run used a fresh random VM name, fresh isolated state, synthetic canaries,
temporary CA and independent lab journal. The sanitized transcripts are
[run 1](darwin-arm64-run-1.txt) and [run 2](darwin-arm64-run-2.txt). They are
byte-identical because they contain stable metadata and case names, not random
values, paths, VM names or timestamps. Two separate native invocations passed
all 29 assertions; independent cleanup also passed for each, and isolated
`msb list` confirmed no sandboxes remained. Run 2 used a second verified-free
port pair (19443/16354) because 18443 was held in TIME_WAIT with no listener.
No process was killed or evicted.

Two earlier attempts are not counted as passes. The first invocation failed
`create-inert-sentinels` with "runtime error: no tested sandbox launch
contract for runtime 0.7.11" because the kit's own `go.mod` still pinned SDK
0.7.8 while the runtime was 0.7.11. The kit is a separate module, so the root
build never compiled it. The pin was corrected and
`TestProjectSecretNativeKitMatchesRuntimeSDK` now fails when the kit's pin or
`go.sum` coverage drifts from the embedded runtime version. The subsequent
kit diff was exactly those two pin lines; no assertion was changed or weakened.

## Qualified Contract

- Dollar-free opaque markers survive raw header and Basic base64 substitution.
- Two bindings can share an allowed destination and substitute distinct values.
- A third binding's permission for another host never authorizes the first two
  there, and vice versa; positive reachability controls distinguish secret
  authorization from network/DNS/TLS failure.
- Forbidden destinations and cross-host redirects fail closed.
- Live value rotation preserves the other bindings; removal stops transmission
  of old/new canary values without claiming guest-environment erasure.
- Re-add with NoRestart is refused; NextStart persists the registration but
  leaves it inactive until boot. After stop/start, all three markers, restored
  A, unchanged B/C, and per-binding host restrictions work.
- Persisted config contains host environment references rather than raw canary
  values; no configured host mounts were present.

## Boundaries

This qualifies the SDK transport primitive, not an end-to-end just-code custom
binding UI or generated dotenv delivery. Host networking was permitted for the
private TLS/DNS lab, and guest Python skipped client certificate verification;
the interception proxy still performed upstream verification using the temporary
CA and required TLS identity for secret substitution. It is not evidence for
guest CA installation, production egress profiles, public services, or other
native architectures. The disposable `python:3.12-alpine` image tag was not
digest-pinned. Header/Basic transport is covered; local token signing and query/
body substitution are not enabled or qualified by these results.

No existing runtime, project checkout, host DNS or host trust store was
modified. Raw private diagnostic logs and CA keys are not checked in.
