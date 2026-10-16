# Native credential store qualification evidence

This directory holds the retained transcripts that qualify the native
credential store on each platform. It is the evidence half of the store
matrix in `docs/plateformes.md`; a platform is qualified there only when a
file here backs it.

## Why this directory is empty

The macOS Keychain store was exercised on macOS arm64 (`put` / `get` /
`remove`, rotation, and removal of an absent element), and that is what the
first version of `docs/plateformes.md` recorded as qualified. The transcript
was not kept, so the claim could not be checked by anyone reading the
repository, and the matrix now records the store as not qualified until a
run writes a file here. The Linux Secret Service and Windows Credential
Manager stores have never been exercised against a live service.

An unretained run is indistinguishable from a run that never happened, which
is the same failure the phase-3 native kit guards against by binding every
transcript to a source revision through its `META` line.

## Producing a transcript

On a machine where the platform's store service actually answers:

```sh
go test ./internal/justcode/ -run TestRealStore -v -count=1 2>&1 | tee tests/integration/credentials/evidence/$(go env GOOS)-$(go env GOARCH)-run-1.txt
```

The tests self-skip when the store is unusable (no session keychain, no
D-Bus), so a transcript full of `--- SKIP` is not evidence of anything: it
records that the store was not exercised. A qualifying transcript contains
`--- PASS` for `TestRealStorePutGetRemoveRoundTrip` and
`TestRealStoreRemoveAbsentIsNotFound`, with real counts (`ok` with a
non-zero `N` of tests run), not skips.

## Binding a transcript to what ran

Each retained transcript must let a reader answer "what produced this?".
Record alongside it:

- the project revision (`git rev-parse HEAD` of the code that was tested),
- the platform and store (e.g. macOS arm64, Keychain, `security` from macOS
  27.0.1),
- the Go toolchain (`go version`),
- the date, and
- for a second run, the fact that it was a separate run rather than a copy of
  the first.

Two runs that are byte-identical must say so and explain why (the store
transcript contains fixed case names and a synthetic marker value, never a
real credential, so identical bytes are expected rather than suspicious).
