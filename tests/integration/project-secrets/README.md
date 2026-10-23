# Project Secret Loader Qualification

Phase 0 of the secrets plan (#145 / #148). These tests exercise real dotenv
loaders against proposed guest-visible placeholders. They contain no secrets
and neither start a microVM nor mock a successful proxy exchange.

## Run

From the repository root:

```sh
npm ci --prefix tests/integration/project-secrets --ignore-scripts
npm test --prefix tests/integration/project-secrets
uv run --no-project --with python-dotenv==1.2.2 python tests/integration/project-secrets/test_python_loaders.py -v
go test ./internal/justcode -run 'TestOpaqueProjectSecretMarkers|TestDollarProjectSecretMarker' -count=1
```

Node dependencies are isolated here, pinned in `package-lock.json`; Python is
isolated by uv. The Go test uses the repository's pinned dependency. No loader
imports host credentials: Node interpolation gets an empty `processEnv`, Python
tests clear the environment temporarily, and Go uses `Unmarshal` only.

## Observed Matrix (2026-10-14)

| Loader | Version | Opaque marker | Dollar marker |
|---|---|---|---|
| Node dotenv | 16.4.7 | Preserved, unquoted/single/double quoted | Preserved by parse alone |
| Node dotenv-expand | 12.0.2 | Preserved in all tested quote modes | Undefined `$MSB_...` erased, including after quoted parsing |
| Python python-dotenv | 1.2.2 | Preserved in all tested quote/interpolation modes | Bare `$MSB_...` preserved; `${MSB_...}` erased with interpolation |
| Go godotenv | 1.5.1 | Preserved in all tested quote modes | Unquoted undefined `$MSB_...` erased |

Node: 14 tests; Python: 2 tests with subcases; Go: 2 tests. All passed locally.
Two component fixtures use the same application key with distinct markers.

## Design Consequence and Remaining Gate

Project bindings should use a stable opaque marker with no dollar references,
for example `__JUST_CODE_SECRET_<opaque-id>__`. Quote selection alone is not a
portable solution. This is a proposal, not a newly enabled runtime binding.

The current Microsandbox runtime/SDK v0.7.6 passed the native canary matrix
twice on macOS arm64. See [the native runner and evidence](native/README.md)
for custom opaque markers (raw and Basic), shared destinations, independent
per-binding authorization, forbidden hosts/redirects, rotation, removal,
re-add/restart and reference persistence. Applications that sign or transform
tokens locally remain unsupported without separate evidence.

The P02 credential-transport laboratory is now pinned to msb 0.7.6 and its
network matrix was requalified 16/16 on macOS (see #205). That result covers
P02 transport only; it does not qualify this project-secrets loader matrix or
these new cases. Do not call Phase 0 complete from loader tests alone. This
loader matrix ran in Cloud; the native matrix ran separately on an explicitly
connected Mac. Neither matrix alone proves the later Phase 2/3 just-code
application integration.
