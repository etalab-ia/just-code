# Native Qualification Evidence

Date: 2026-10-14, runs completed at approximately 12:20 and 12:21 UTC.
Platform: macOS arm64; native Go 1.27.1; SDK/runtime 0.7.6.
Project baseline: PR #159 merge `0af624b58b0d3eeb648bd0a398e74b741aaa5099`.

The exact Cloud-authored v3 test artifact was downloaded and checksum-verified
before execution on the Mac:

```text
kit SHA256: 562343a03356fb6753d144c055e883e498f99176fe5c0fa6b6c4764370c91d55
runtime archive SHA256: a4f1722fee6460d4e2813e62b60dae4c092021d8120b7aaa9c2f4b5679d97d89
run-1 results SHA256: 5807c788d549d623b355240e2225488914d83644f765da3c272baa25c34fc231
run-2 results SHA256: 5807c788d549d623b355240e2225488914d83644f765da3c272baa25c34fc231
```

Each run used a fresh random VM name, fresh isolated state, synthetic canaries,
temporary CA and independent lab journal. The sanitized transcripts are
[run 1](darwin-arm64-run-1.txt) and [run 2](darwin-arm64-run-2.txt). They are
byte-identical because they contain stable metadata and case names, not random
values, paths, VM names or timestamps. Two separate native invocations passed
all 29 assertions; independent cleanup also passed for each, and a subsequent
isolated `msb list` confirmed no sandboxes remained.

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

An initial v1 attempt failed before any transport test because macOS's long
default temporary path exceeded the Unix socket path limit. An independent
no-secret CLI baseline exposed that error. The short `/tmp/jcp0-*` path fixes
the fixture without changing the secret assertions. v1 (short TMPDIR) and v2
then passed their 13-case subset; the recorded final evidence uses v3's expanded
29-case matrix. No existing runtime, project checkout, host DNS or host trust
store was modified. Raw private diagnostic logs and CA keys are not checked in.
