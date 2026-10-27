# Native Qualification Evidence

Date: 2026-10-27, two runs completed at approximately 14:30 and 14:34 UTC.
Platform: macOS 27.0.1 arm64; native Go 1.27.3; SDK/runtime 0.7.8.
Project fixture source: Microsandbox pin commit `e10db59879912af341e8794fcf9b7c2357fddaa6`.

The Cloud-authored v3 test artifact was downloaded and checksum-verified
before execution on the Mac:

```text
kit SHA256: ea1a46da0b1771573fa5838513c50366bd4f884ce160fb10abb5832202d7e12d
runtime archive SHA256: 5dbe0d921c6b6f807d2c59f87e0a3c3a648424871b6bee513b837a8c600a2493
run-1 results SHA256: bd135fecc29b40da987cc3521c72a2a7986e4b8019a56fb084dd98ae82b1153a
run-2 results SHA256: bd135fecc29b40da987cc3521c72a2a7986e4b8019a56fb084dd98ae82b1153a
```

Each run used a fresh random VM name, fresh isolated state, synthetic canaries,
temporary CA and independent lab journal. The sanitized transcripts are
[run 1](darwin-arm64-run-1.txt) and [run 2](darwin-arm64-run-2.txt). They are
byte-identical because they contain stable metadata and case names, not random
values, paths, VM names or timestamps. Two separate native invocations passed
all 29 assertions; independent cleanup also passed for each, and isolated
`msb list` confirmed no sandboxes remained. The first attempt at run 2 was
refused before VM creation because the configured lab port was occupied. No
process was killed; a fresh run 2 succeeded after the port became free. That
preflight attempt is not counted as a native pass.

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
