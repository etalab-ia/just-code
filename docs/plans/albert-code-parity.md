# just-code: plan d'implémentation PR par PR (parité Albert Code)

Brouillon de revue | 22 septembre 2026

## Résumé

Atteindre la parité Albert Code via **21 PR revoyables**, organisées en preuves de compatibilité, fondations, un jalon de lancement par défaut utilisable, puis la configuration projet complète. Conserver Go et les adaptateurs existants. Ne pas construire un nouveau cadre d'orchestration.

Ce document transforme l'[analyse architecturale acceptée](https://gist.github.com/kaaloo/daad9511fdd645712d617e8632e9aebc) en backlog de livraison proposé. Les identifiants **P01-P21 sont des identifiants de planification, pas des numéros de PR GitHub existants**. Aucune PR d'implémentation ni nouvelle issue n'a été ouverte pour ce plan.

Référentiel source revérifié le 22 septembre : just-code main `6d231654633aa409ca373f89b1073f68e92ca619` (v0.4.2), Go 1.22, SDK Microsandbox 0.7.2. Main est inchangé depuis l'analyse. L'[issue #29](https://github.com/etalab-ia/just-code/issues/29) est la seule issue ouverte ; il n'y a pas de PR ouverte. La comparaison Albert Code reste fondée sur `46569c7`.

**Approbation demandée :** le périmètre, la séquence, la politique de compatibilité et les critères d'acceptation ci-dessous. L'approbation de ce document n'est pas une permission d'affaiblir la protection des identifiants si une expérience technique échoue.

## 1. Décisions de travail

Luis a accepté l'analyse et ses recommandations. Utiliser ce qui suit comme hypothèses de planification plutôt que de les rouvrir dans chaque PR :

| Topic | Implementation baseline |
|---|---|
| Normal launch | `just-code` uses Microsandbox and full isolation without flags or environment variables. Explicit supported overrides remain. |
| Workspace boundary | Keep the writable project checkout mount. Full means guest execution, not absence of shared host files. No home, host Docker socket, SSH agent or browser-profile mount. Guest-owned clones are out of scope. |
| Project configuration | Secret-free manifest and lockfile intended for version control; explicit local-only storage option. Setup writes files but never stages or commits them. |
| Credentials | Native credential store when usable; an explicitly accepted protected-file fallback for headless systems. No silent storage downgrade. |
| GitHub | Provision `gh` in the guest when GitHub is enabled. Credential access is separately approved per project. No host GitHub CLI requirement for normal use. |
| Instances | Distinct persistent project/worktree environments; one active managed environment initially. No automatic destruction when switching projects. |
| Browser guest | Select Alpine or a Debian-based guest from measured compatibility, not assumption. |
| Updates | Explicit preview/apply; no unpinned package or skill refresh on ordinary launch. |
| Runtime coverage | Microsandbox gets the protected default path. Tart and agent-vm remain explicit alternatives with honest capability and credential-exposure reporting. |

Three clarifications sharpen the previous discussion:

- **Tool installation is not authorization.** Absence of `gh` does not block HTTP calls, public GitHub access, or installation of another client. The managed grant controls whether just-code supplies credentials. Network policy and PAT permissions determine what authorized requests can do.
- **A native keyring is not a universal same-user process barrier.** Encryption, unlock behavior and application access controls vary. Probe availability and lock state; do not assume every desktop has a usable Secret Service or that only just-code can retrieve an item. Audit the runtime's own persistence too.
- **A lockfile improves reproducibility but does not make environments identical.** Host architecture, remote APIs/models and mutable OS package repositories remain variables. Record exact artifacts and verify integrity where supported; distinguish pinned from externally controlled components.

## 2. Règles de livraison et de revue

1. Extend issue #29 with the approved configuration contract, then create the grouped tracking issues in section 6 before implementation. Do not file 21 empty issues merely to mirror the PR count.
2. Each PR includes its own tests, documentation and upgrade notes. P21 is final integration qualification, not the first time features get tested.
3. Keep intermediate main usable. Foundations remain internal until their command path is complete; no placeholder commands or half-working wizard options. Incomplete credential transports are unavailable, never silently substituted with raw guest tokens.
4. Merge independent PRs to main where practical. Dependent PRs name their prerequisites and rebase after those merge. Avoid maintaining a 21-branch stack.
5. Do not merge a release-please release PR while a public behavior change is incomplete. Tag only at qualified milestones. P12 is the first possible new-default release; P21 qualifies complete parity.
6. Every PR must pass the repository's existing checks: formatting, `go vet ./...`, `go test -race ./...` on the current check runner, plus native test/build jobs. These commands are verified in `.github/workflows/ci.yml`. SDK CGO/FFI support means cross-compilation is not a substitute for native builds.
7. Preserve gitleaks and dependency review. The Node-only `cvs-lite-cli` hook is not an applicable Go validation gate. New Go/TUI/keyring dependencies need a license, maintenance, platform and Go-version check before adoption.
8. Real integration results state OS, architecture, CLI/SDK/OpenCode versions and what actually ran. A missing virtualization runner is a blocked test, not a pass. No production credentials or sensitive raw traces in test artifacts.

## 3. Séquence d'ensemble

Toutes les dépendances ci-dessous sont **directes** ; leurs dépendances transitives s'appliquent automatiquement. La numérotation donne un ordre de lecture commode, pas une obligation d'exécuter séquentiellement des travaux indépendants.

| PR | Proposed French PR title | Depends on | Result |
|---|---|---|---|
| P01 | `test: caractériser la configuration effective d’OpenCode` | None | Proven merge/discovery contract |
| P02 | `test: qualifier le transport et le stockage des secrets Microsandbox` | None | Credential transport decision |
| P03 | `test: qualifier les capacités invitées et le terminal interactif` | None | Guest/browser/PTY evidence |
| P04 | `feat(config): séparer les réglages globaux et projet` | None | Typed resolution, schemas, provenance |
| P05 | `refactor(msb): identifier les environnements par projet` | P04 | Named Microsandbox instances |
| P06 | `refactor(runtime): cibler les opérations sur une instance` | P05 | Coherent lifecycle across adapters |
| P07 | `feat(runtime): planifier et réconcilier les changements` | P06 | Safe, resumable application of changes |
| P08 | `feat(auth): stocker les identifiants hors des projets` | P02, P04 | Native stores + explicit fallback |
| P09 | `feat(msb): gérer les liaisons de secrets et leur révocation` | P07, P08 | Destination-bound credential lifecycle |
| P10 | `feat(config): composer et approuver la configuration OpenCode` | P01, P07, P09 | Effective config, trust, provider/model handling |
| P11 | `feat(setup): accompagner la configuration globale` | P03, P08, P10 | Usable global wizard |
| P12 | `feat: lancer Microsandbox en isolation complète par défaut` | P06, P10, P11 | Minimal zero-flag vertical slice |
| P13 | `feat(github): provisionner gh et activer les accès approuvés` | P09, P12 | Working guest GitHub workflow |
| P14 | `feat(projet): gérer les skills et les instructions versionnées` | P10, P12 | Pinned skills and managed rules |
| P15 | `feat(mcp): configurer data.gouv et Context7` | P09, P10, P12 | Remote MCP setup |
| P16 | `feat(invité): provisionner les MCP navigateur` | P03, P07, P10, P12 | Working Playwright and DevTools |
| P17 | `feat(init): unifier l’assistant de configuration projet` | P13, P14, P15, P16 | Complete project wizard |
| P18 | `feat(update): mettre à jour les dépendances sur validation` | P17 | Reviewable project updates |
| P19 | `feat(migration): importer les projets Albert Code` | P17 | Explicit, non-destructive importer |
| P20 | `feat(install): accompagner l’installation sur chaque plateforme` | P12 | Verified installation wrappers |
| P21 | `test: qualifier la parité et les parcours de migration` | P18, P19, P20 | Release evidence and support matrix |

**Parallel opportunities:** P01-P04; P05-P07 alongside P08; P13-P16 after P12; P18/P19/P20 once their prerequisites are available. Do not parallelize edits to `main.go` without coordinating ownership.

### Jalons

- **M0, evidence:** P01-P03. Close the consequential unknowns before promising GitHub protection or browser support. P04 can progress independently.
- **M1, usable default:** P04-P12 and their prerequisites. Fresh install -> global setup -> minimal project init -> guest TUI -> retained state. This is not yet advertised as Albert Code parity.
- **M2, project parity:** P13-P17. GitHub, selected skills, managed rules and all four curated MCPs work through a coherent wizard.
- **M3, qualified release:** P18-P21. Explicit updates, import, installation and real-host qualification are complete.

## 4. Contrats détaillés des PR

### P01. Characterize OpenCode configuration and discovery

**Purpose:** settle how generated provider/MCP configuration coexists with repository JSON/JSONC, global settings, skills and AGENTS instructions before writing the composer.

**Scope:** introduce a pinned OpenCode integration fixture and a concise decision record. Test project config, explicit config location and inline configuration precedence; determine how a guest-local overlay can coexist with project-relative paths. Verify the actual supported locations for skills and instruction discovery. Choose a maintained parser only if an import path needs JSONC parsing.

**Likely files:** proposed `tests/integration/opencode/`, a focused decision document under `docs/`; `assets/opencode-config.json` remains unchanged in this PR.

**Acceptance:** the real pinned OpenCode process exposes or exercises the effective model, provider, MCP and skill selections; unrelated user settings survive; conflicting managed fields are detectable; JSONC input is not rewritten. Determine whether plugin/config loading can execute code and include that in the trust contract. No invented OpenCode inspection command: verify its actual CLI/API during the spike.

**Boundary/gate:** test assets and conclusions only. If overlays cannot compose safely, revise P10's design before implementation. A fixture that checks only a generated JSON object is insufficient.

### P02. Qualify credential storage and transport

**Purpose:** establish what Microsandbox 0.7.2 actually protects, persists and permits updating.

**Scope:** opt-in tests with canary credentials for Albert Bearer auth, GitHub `gh` requests, Git HTTPS Basic auth and Context7's actual authentication mechanism. Inspect host state, guest environment/files, command arguments and logs without publishing values. Test allowed destinations, redirects, rotation, addition and removal of bindings on running/stopped guests. Check keyring library availability and locked/unavailable/error distinctions on the supported OS families.

**Likely files:** proposed `tests/integration/credentials/`, SDK contract fixtures near `microsandbox_sdk.go`, focused decision record. Retain current single-key production behavior until P09.

**Acceptance:** positive real calls plus negative destination/redirect cases; demonstrate whether raw credentials persist on the host; record restart versus recreation requirements and any network substitution limits. Validate Git write rights with a dedicated disposable repository, never the production repository. A failed test setup is reported separately from a transport failure.

**Boundary/gate:** if Basic auth or Context7 cannot be protected, stop that dependent feature and return a design choice. A broker is a separate conditional PR with its own threat model and estimate. Guest-readable token fallback is not pre-approved. Keyring storage must not be marketed as eliminating runtime plaintext persistence if the runtime still writes it.

### P03. Qualify guest capabilities and PTY behavior

**Purpose:** choose a practical guest foundation and verify the full-mode user experience.

**Scope:** compare the current Alpine guest with a pinned Debian-based candidate for OpenCode, Node/npm, `gh`, Chromium, Playwright MCP and Chrome DevTools MCP. Record image size, cold preparation time, disk/RAM needs and both guest architectures. Test terminal resize, Ctrl+C, clean exit, reconnect and SDK handle ownership. Check Git worktree/submodule metadata references outside the workspace mount.

**Likely files:** proposed integration fixtures and guest candidate assets under test-only directories; no production base-image switch yet.

**Acceptance:** actual browser navigation, screenshot and DOM/console operation; guest TUI runs without host Node/OpenCode; stopping an attach handle has the intended VM lifecycle. Identify unsupported host/guest combinations explicitly. Record a safe policy for linked worktrees: narrow metadata support or clear refusal, never a parent-directory mount.

**Boundary/gate:** browser measurements decide P16, not the M1 default switch. PTY and workspace-boundary evidence does gate M1. A new maintained image pipeline, if needed, is explicit scope for P16 and may be split into its own prerequisite PR.

### P04. Typed configuration, schemas and provenance

**Purpose:** resolve #29's underlying design without immediately changing all existing launch behavior.

**Scope:** global user config, project manifest, initial lock schema, platform directories and pure field-by-field resolution. Retain a compatibility adapter around existing `Config` callers. Define explicit flags > namespaced environment > project > user > built-ins for non-secret settings; track whether a value was unset or explicitly empty. Credential lookup remains separate. Provide redacted configuration explanation and a bounded parser for explicit legacy `.env` import.

**Likely files:** `config.go`, `config_test.go`, `paths.go`, `cmd/just-code/main.go`; proposed schema/resolver files and versioned JSON schemas. Logical credential requirements map to local credentials through host-local bindings; no machine-specific keyring IDs in shared files.

**Acceptance:** table-driven precedence tests, explicit-empty behavior, malformed/unknown schema versions, platform directory tests and secret redaction. Reading configuration has no process-environment mutation. Secret literals and arbitrary env maps are not supported by the managed schema; reject unsupported fields. No config migration executes shell files.

**Boundary/compatibility:** new resolver is initially internal; legacy launch defaults stay until P12. Define and document the future deprecation window, but do not silently read an unrelated project `.env` in the new path. This PR contributes to #29; it does not close it.

### P05. Project discovery and Microsandbox instance identity

**Purpose:** stop reusing one sandbox for unrelated checkouts.

**Scope:** `ProjectContext` and `InstanceRef`, canonical root discovery, deterministic readable name plus path-derived suffix, host-local registry and per-project operation lock. Explicit directory override wins; otherwise use Git worktree root, or the current directory for non-Git projects. Do not scan/mount the home or filesystem root implicitly. Pass instance names into create, lookup, exec, logs, shell and PTY methods instead of the global constant.

**Likely files:** `microsandbox.go`, `microsandbox_sdk.go`, `name.go`, `paths.go`, tests; proposed `project.go` and registry helpers.

**Acceptance:** two same-named repositories and two worktrees never collide; symlink aliases resolve consistently; Windows casing/path rules are tested on Windows. Concurrent setup calls cannot create duplicate instances. Stale or unknown mounts fail closed before attach. Unsupported external Git metadata gets a precise preflight error.

**Boundary/compatibility:** existing singleton instances are discovered as legacy, not renamed/deleted automatically. Metadata absence is not permission to adopt an arbitrary VM. Record ownership only after validating the real mounted path and runtime state.

### P06. Project-aware lifecycle across runtimes

**Purpose:** replace runtime-type-as-instance assumptions without abandoning explicit runtime overrides.

**Scope:** project-bound backend construction and registry-based discovery; parameterize Tart/agent-vm names, staged files and log paths too. Separate runtime-global template/install operations from project instances. Make `stop`, `check`, `logs`, `shell` and destructive cleanup target a project; add an explicit all-instance stop. Enforce the initial one-active-instance policy by prompting to stop, not destroy, a conflicting environment. Correct platform capability checks.

**Likely files:** `runtime.go`, `dispatcher.go`, `tart.go`, `agentvm.go`, `paths.go`, CLI parser and matching tests.

**Acceptance:** stopping project A leaves project B intact; missing project context never silently becomes stop-all; unsupported runtimes fail before installation. Legacy instances remain discoverable for explicit lifecycle operations. Switching runtimes preserves the other instance's disk. Resource/template settings cannot accidentally rename or remove unrelated VMs.

**Boundary/compatibility:** document the changed `stop` scope and expose the explicit replacement in the same PR. Keep legacy destructive `restart` blocked behind a warning/confirmation until P07 replaces its semantics. No default-runtime switch yet.

### P07. Reconciliation and non-destructive restart

**Purpose:** apply setup changes to existing instances, including running ones, rather than returning early or recreating everything.

**Scope:** a small typed plan with operations for no-op, config write, guest process restart, VM restart and recreation. Persist desired/applied configuration revisions and capability completion markers without credential values. Hash only non-secret config; use opaque credential generations, not hashes of credentials. Implement resumable, atomic writes and a per-project mutation lock. Make restart non-destructive; expose explicit recreation with a named loss summary.

**Likely files:** `microsandbox.go`, adapter lifecycle methods, `assets/guest-prep.sh`, `cmd/just-code/main.go`; proposed plan/reconcile helpers.

**Acceptance:** identical desired state performs no writes; interruption resumes at the unfinished operation; config failure leaves the prior configuration usable; resource/mount/isolation changes are classified correctly. Unknown persisted state does not trigger deletion. Existing readiness/recovery and SDK detach/close regressions remain covered.

**Boundary/compatibility:** old `restart` scripts receive prominent release/migration documentation; tests prove no path invokes `Clean` as ordinary restart. Do not promise transactional rollback of OS package installation: journal completed effects and clearly mark partial repair requirements.

### P08. Host credential storage

**Purpose:** provide global credentials without putting them in project files or shell profiles.

**Scope:** minimal credential-store interface; macOS Keychain, Windows Credential Manager and Linux Secret Service adapters; explicit owner-only file fallback. Add complete `auth add`, `auth status` and `auth remove` operations with masked interactive input or a documented noninteractive stdin path. Display storage type and verification state, not secret values. Handle unavailable, locked, denied and corrupt stores separately.

**Likely files:** proposed `credentials*.go`, OS-specific adapters/tests, `paths.go`, CLI auth parsing; `go.mod`/`go.sum` only for reviewed dependencies.

**Acceptance:** real store put/get/remove on each supported OS family; headless Linux fallback only after explicit consent; restrictive directory/file permissions or ACLs; atomic replacement and symlink defenses. Never silently create a new plaintext store after keyring unlock fails. No secret in argv, logs, preview or exported project config.

**Boundary/compatibility:** host credential storage does not itself grant guest access. For deletion of a currently bound credential, coordinate with P09: until revocation can be enforced, refuse with an actionable message rather than claim access was revoked. Existing environment credentials remain supported as explicit session inputs.

### P09. Multiple secret bindings and revocation

**Purpose:** generalize the Albert-specific SDK path while preserving its protection boundary.

**Scope:** replace `APIKey`-only SDK arguments with resolved named bindings, approved hosts, transport metadata and placeholders. Resolve values immediately before the runtime operation. Reconcile addition/removal/rotation using P02's proven lifecycle. Host-local project approval authorizes each optional binding; global storage alone does not. Removal updates running instances before reporting success.

**Likely files:** `microsandbox.go`, `microsandbox_sdk.go`, credentials integration, capability reporting for other adapters and tests built from real SDK JSON.

**Acceptance:** Albert regression passes; multiple bindings cannot cross destinations; no raw values in guest environment/config; running and stopped rotations/removals have real negative tests. A removed key is not left working through stale persisted proxy state. Errors do not print SDK objects containing secrets.

**Boundary/compatibility:** optional GitHub/Context7 bindings are exposed only when their P02 transport gate passes. Tart/agent-vm report guest-readable credentials and require explicit acknowledgement of that weaker behavior; they do not inherit a Microsandbox protection claim. No broker or raw-token workaround hidden in this PR.

### P10. OpenCode composition, local trust and models

**Purpose:** make one effective configuration path govern provider, model and later MCP additions.

**Scope:** implement P01's tested guest-local composition strategy; preserve repository JSON/JSONC and unrelated user settings. Remove stale creation-time inline configuration as the authoritative source. Add host-local approval of execution-relevant project inputs before OpenCode loads them: managed MCP commands, endpoint destinations, plugins and relevant project config. Validate Albert credentials/catalogue through approved endpoints; cache last-known-good models and request explicit selection for stale IDs.

**Likely files:** `assets/opencode-config.json`, `microsandbox.go`, `guest.go`, `agentvm.go`, `tart.go`; proposed OpenCode builder, trust, provider and catalogue helpers.

**Acceptance:** effective real OpenCode config matches approved intent after create and restart; field conflicts show a clear diff rather than an invisible override. A pulled MCP/plugin or endpoint change cannot use credentials before approval. Trust decisions are tied to the canonical project and approved content; recheck before execution to avoid applying changed files. Network errors do not erase the working model; catalogue IDs do not fabricate token limits.

**Boundary/compatibility:** local trust records never travel in the repository. They govern just-code-managed activation, not every action a fully privileged guest process could take. Existing project configuration needs an initial review/import path; do not treat a cloned repository as trusted merely because it contains a lockfile.

### P11. Global setup wizard

**Purpose:** make machine and credential preparation understandable and resumable.

**Scope:** implement `setup`: platform/virtualization/disk preflight -> Albert endpoint and masked credential -> optional GitHub credential and editable identity -> review/apply. Use the real domain operations from preceding PRs. Separate read-only diagnosis from installation. Prototype then select a maintained terminal renderer; retain a plain terminal and noninteractive path. Prepare the minimal guest capability, not browser tools by default.

**Likely files:** `cmd/just-code/main.go`, new command/rendering files; setup operations in `internal/justcode`; runtime installer hooks and docs.

**Acceptance:** clean host with no Node/OpenCode reaches a configured global state; unavailable Secret Service produces a clear choice; no credential prompt precedes a fatal virtualization blocker. Back/cancel/retry, narrow terminals, paste, no-color and non-TTY paths work. Interrupted installation resumes; failed credential validation distinguishes rejection from unavailable network. Skip GitHub without penalty.

**Boundary/compatibility:** `setup` may store a GitHub credential but does not activate it for all projects. It does not say the project GitHub workflow is ready before P13. No inference call just to display a green status badge.

### P12. Zero-flag defaults and minimal project init

**Purpose:** deliver the first end-to-end useful milestone, not merely change two constants.

**Scope:** set built-in runtime to Microsandbox and isolation to full. Bare interactive launch offers missing global setup and a minimal `init`: project root, sharing mode, model, resources, Albert grant and review. Write the minimal manifest/lock data and launch the guest TUI. Existing environment/CLI overrides remain explicit; remove ambient `.env` discovery from the new path and offer bounded legacy import. No host OpenCode check in full mode.

**Likely files:** CLI dispatch, `runtime.go`, `isolation.go`, resolver defaults, workspace gate, port mapping and launch policy; README and migration docs.

**Acceptance:** clean-machine zero-flag walkthrough; second launch needs no setup questions and retains sessions; non-TTY launch fails with actionable missing-input details instead of hanging. Full mode opens no OpenCode server port. Explicit backend mode preflights host OpenCode and uses per-instance auth; preview ports require explicit configuration and verified loopback behavior. Existing singleton/backend installations offer continued explicit legacy use or approved migration, never automatic recreation.

**Boundary/compatibility:** project workspace scan and secret-file guards stay in effect. Existing backend credentials explicitly set to empty retain their documented meaning, with a warning where appropriate; never silently expose a listener off loopback. P12 closes #29 only after its full acceptance criteria and migration documentation are satisfied. M1 is a default-launch release, not full parity.

### P13. Guest GitHub workflow

**Purpose:** make `gh`, git push and PR operations actually available when selected.

**Scope:** provision `gh` as a versioned guest capability; set user-selected Git identity; configure the tested `gh` and HTTPS git authentication path. Add project selection and approval through the existing minimal init flow. Distinguish stored token, valid account, repository access, write/PR permissions and organization approval. Use repository-scoped fine-grained PAT guidance.

**Likely files:** guest provisioning, GitHub helper/configuration, credential bindings, project schema and init options.

**Acceptance:** disposable private repository clone/fetch/push and draft PR from inside the guest; missing/expired/under-scoped token errors are distinct. Revoke the grant and prove subsequent authenticated requests fail. Never install host `gh` for this path. Guest key bytes remain hidden under the selected protected transport.

**Boundary/compatibility:** no host SSH-agent or signing-key forwarding. Disabled means no managed GitHub credential, not inability to access GitHub anonymously. If the transport spike requires an unapproved broker, this PR stays blocked rather than shipping a misleading partial claim.

### P14. Skills, managed instructions and lock data

**Purpose:** provide reproducible, reviewable project skills and preserve handwritten instructions.

**Scope:** catalogue selected skills from `etalab-ia/skills`; record source/revision/content integrity in lock data; fetch selected artifacts into a host cache and install guest-locally using P01's verified discovery path. Preserve user skills and manage only owned entries. Add/update a bounded AGENTS managed zone with a before/after diff. Expose selections through init's existing renderer, not an unrelated command family.

**Likely files:** proposed catalogue/skills/rules helpers, project schemas/lock writer, guest configuration, tests and a managed instruction template.

**Acceptance:** selection, deselection, cached offline launch, corrupt/missing archive, archive traversal/symlink escape and duplicate skill-name tests. Repeated init has no diff; malformed/ambiguous AGENTS markers stop safely; unmanaged text survives byte-for-byte. Real OpenCode discovers only the intended managed additions. No automatic latest revision on launch.

**Boundary/compatibility:** do not invoke an installer that uploads or executes arbitrary host-side code. Skills are reviewed instruction/code artifacts, not a sandbox security policy. Local-only mode stores selections outside the checkout and explicitly handles whether AGENTS content is written; it must not claim to be local-only while silently changing a tracked file.

### P15. Remote MCPs: data.gouv and Context7

**Purpose:** ship the simpler MCP integrations before browser provisioning complexity.

**Scope:** a small curated connector catalogue containing verified transport, endpoint, package/version if needed, configuration schema, credential requirement and health action. Add selected data.gouv and Context7 connectors to the approved overlay. Obtain a Context7 credential only when required by its verified integration contract. Display external data destinations and separate configured/verified/offline states.

**Likely files:** proposed MCP catalogue/configuration/health helpers, project schema, init selection and integration tests.

**Acceptance:** actual tool calls through OpenCode from the guest for both connectors; endpoint/schema drift, unavailable network and bad credentials reported distinctly. Deselection removes generated config and managed credential authorization. Selection does not install unrelated browser tooling. No `latest` package references in generated commands.

**Boundary/compatibility:** verify upstream URLs/auth mechanisms during implementation rather than infer them from names. Remote service availability/version remains external even when the local connector is pinned. Existing custom MCP config is preserved but requires local trust when execution-relevant inputs change.

### P16. Browser capability and browser MCPs

**Purpose:** ship working Playwright and Chrome DevTools, not just configuration entries.

**Scope:** apply P03's chosen guest approach; version browser, fonts/libraries and connector packages, with architecture-specific artifacts. Add browser capability planning and host-aware resource recommendations. If a maintained image is necessary, introduce digest-pinned image publication as a clearly separate prerequisite change within this workstream, with integrity/provenance and update ownership. Keep browser install optional.

**Likely files:** guest assets/provisioning, SDK image/resource options, browser MCP catalogue, integration tests; optional image build workflow and release metadata.

**Acceptance:** Playwright navigation and screenshot; Chrome DevTools DOM/console interaction; local project dev-server access from the guest browser; both supported guest architectures qualified. No host profile, host browser or exposed remote-debugging listener. Test constrained memory/disk, interrupted install and repeat setup. Record measured resource defaults rather than guessing a larger RAM number.

**Boundary/compatibility:** do not disable browser security flags merely to make a test pass without reviewing the isolation implications. Base-image/root-disk changes may require explicit recreation; they cannot silently destroy an M1 guest. Broader Docker/toolchain parity is not included.

### P17. Complete project wizard and consistent DX

**Purpose:** unify the accumulated capabilities into the intended installation/setup experience.

**Scope:** searchable skill selection, MCP options, GitHub grant, model/resources, versioned/local-only choice and a real combined preview. Back navigation retains non-secret choices; secret input has a separate lifetime. Explain downloads, modified files, credential destinations and process/VM restart consequences. Preserve independent user config. Bare launch invokes only missing setup steps; a configured user reaches the TUI directly.

**Likely files:** command/rendering layer, shared setup plan, UX fixtures and user docs. No second implementation of provisioning inside the TUI.

**Acceptance:** recorded walkthroughs on narrow/wide terminals and Windows terminal; cancel at every step, keyboard-only selection, plaintext fallback, redirected streams and scripted mode. Versioned mode writes but never commits; local-only mode keeps managed selections in host state. Noninteractive approvals name the exact plan/grants; blanket yes cannot authorize arbitrary endpoints or destruction. Warm launch performs no dependency update and does not repeat choices.

**Boundary/compatibility:** agree performance budgets from measured M1/early wizard baselines: local resolution, warm TUI entry and cold download are separate metrics. Avoid presenting a percentage for unmeasured package work.

### P18. Controlled updates

**Purpose:** keep project environments current without changing them unexpectedly during launch.

**Scope:** `update` compares pinned skill/MCP/image/rules revisions with available revisions, builds a reviewable plan, refreshes lock data and applies approved operations. Reuse the reconciler and trust checks. Catalogue refresh failure retains the known-good environment. Distinguish CLI/runtime updates from project dependency updates.

**Likely files:** update command, catalogue resolvers, lock writer, managed rules and reconcile integration.

**Acceptance:** no-op update, selective update, network loss, integrity mismatch and crash between manifest/lock writes. Stage and recover paired file updates through a journal; no mismatched manifest/lock accepted as ready. Changed commands/destinations require approval. Config/lock rollback is tested; VM/package rollback limitations and guest-state loss are reported honestly.

**Boundary/compatibility:** no automatic guest-image recreation, no floating revisions on boot, and no claim that rolling back a lockfile restores guest disk contents. Retain previous artifacts only within a documented cache/space policy.

### P19. Albert Code importer

**Purpose:** bring existing users across without deleting or executing their setup.

**Scope:** explicitly detect `.albert-code/skills.txt`, OpenCode config and known managed AGENTS zones. Parse recognized intent into a proposed manifest/lockfile and host credential references. Preserve unknown custom settings for review. Offer explicit credential import only from supported safe inputs; never source a shell profile/runtime script. Keep the legacy `.env`/singleton migration introduced for M1 covered here too.

**Likely files:** importer helpers/fixtures, init import flow, migration docs; no change to the albert-code repository.

**Acceptance:** realistic fixtures with JSONC, custom MCPs, partial setup, malformed markers and tracked/untracked config. Import twice is idempotent. Preview contains no secret bytes; originals remain untouched until the user approves any separate cleanup. Git exclusion edits, if needed, preserve unrelated lines and use Git-resolved paths. A credential file in the mounted workspace remains a blocking security concern, not a reason to disable scanning.

**Boundary/compatibility:** guest sessions, untracked guest files and installed tools are not declared migrated without a proven export/import path. Never execute `.agent-vm.runtime.sh` to discover configuration.

### P20. Verified installers

**Purpose:** reduce installation friction without bypassing existing release integrity checks.

**Scope:** small POSIX-shell and PowerShell wrappers around published artifacts; detect supported target, resolve one release, obtain binary/checksums from that release, verify before installation, install to a user-owned directory and invoke setup. Retain prior binary for rollback. Offer PATH guidance; prompt before profile changes. Reject unsupported targets such as macOS Intel while the SDK lacks its FFI build.

**Likely files:** proposed installer scripts, README, release workflow only where artifact/attestation needs require it; installer tests using representative release metadata.

**Acceptance:** real installation and upgrade on advertised targets, checksum mismatch, partial download, insufficient permissions, spaces in paths and interrupted replacement. No sudo for the user CLI installation. Version skew cannot mix one release's binary with another release's checksum. Preserve the runtime's existing pinned artifact verification.

**Boundary/compatibility:** distinguish checksum integrity from authenticated publisher provenance. Use existing release attestations where verified; otherwise document the trust boundary. Homebrew packaging/notarization are separate follow-ups, not prerequisites hidden in this PR.

### P21. Final diagnostics and release qualification

**Purpose:** prove the assembled experience and publish the actual support matrix.

**Scope:** integrate existing feature diagnostics into read-only `doctor` and project `check`; report host capability, VM state, toolchain, provider verification, managed skills and MCP readiness separately. Add whole-flow integration fixtures, redacted support output and migration/release documentation. Validate empty-machine, returning-user, cloned-team-project and imported-project journeys.

**Likely files:** diagnostic orchestration, end-to-end test harness, docs and a controlled integration workflow if real runners exist. Do not grant repository secrets to untrusted fork jobs; manual integration gates are acceptable when automation cannot safely run them.

**Acceptance:** M1/M2 flows plus update/import on macOS arm64, Linux amd64/arm64 and Windows amd64/arm64 as advertised. Hosted native compile/tests alone do not count as microVM qualification. Record missing evidence explicitly and block unsupported claims. Retry/offline/cancel/revoke tests pass; secrets are absent from diagnostics; doctor does not install or mutate. Confirm explicit Tart/agent-vm paths still work with documented limitations.

**Boundary/release:** only after acceptance, merge the appropriate release-please release PR through the existing process. Publish changes to stop/restart scope, runtime/isolation/workspace defaults, configuration lookup and credential storage prominently. Do not label the release complete parity if a credential transport or curated MCP remains unqualified.

## 5. Contrat transversal de tests et de migration

### Checks on every relevant PR

| Layer | Evidence required |
|---|---|
| Resolver/config | Source precedence, explicit empty values, schema compatibility, no global env mutation, redaction |
| Filesystem | Atomic writes, process interruption, symlink/path traversal, ownership and permissions, no unrelated edits |
| SDK | Fixtures captured from real serialized state; positive and negative persisted-state cases; real transport smoke test when affected |
| Lifecycle | No-op, apply failure, retry, current-versus-desired state, project isolation, no implicit deletion |
| Credentials | Store unavailable/locked, explicit fallback, destination mismatch, redirect, rotation and removal |
| User config | Existing JSONC/MCPs/skills/instructions preserved; effective OpenCode state checked |
| CLI | No prompt on non-TTY, help/version usable without credentials, targeted repair messages, no raw secret output |
| Platform | Native builds and tests; real keyring/PTY/microVM evidence wherever those behaviors change |

**Stop conditions:** a security boundary cannot be reproduced; a destructive migration lacks consent; an existing project loses unmanaged content; or a claimed supported platform has no required integration evidence. Do not relabel these as documentation-only limitations to ship the feature.

### Legacy transition

- Keep legacy exported environment settings for a documented deprecation interval; use provenance/warnings when they override new defaults. Choose the exact retirement release during P04, not in an unreviewed cleanup commit.
- New configuration never implicitly sources generic project/executable-adjacent `.env`; offer explicit import and leave originals intact.
- Existing singleton guests are legacy instances. Adoption requires matching the real mount/isolation and compatible provisioning; otherwise retain the old instance and create a new one only with explicit user approval.
- `stop` changes to project scope, with explicit all-instance operation. `restart` changes to non-destructive stop/start; recreation/cleanup names the instance and loss. Include these changes in M1 release notes.
- New defaults apply to new/unconfigured intent, not as permission to overwrite explicit runtime or isolation choices. An existing backend guest is not silently transformed into full mode.
- Schema readers fail clearly on newer unsupported schemas. Old binaries are not promised forward compatibility with newly configured guests; binary rollback and guest/config rollback are different procedures.

Le document de revue d'architecture détaillé associé est disponible dans [albert-code-parity-architecture.md](./albert-code-parity-architecture.md).

## 6. Organisation des issues et de la revue

After plan approval, establish this issue structure. These are proposed titles, not claims that issues exist.

| Tracker | Proposed scope | PRs |
|---|---|---|
| Existing #29 | Configuration contract, global/project setup baseline, default launch and legacy config migration | P04, P11, P12; references P08/P10 |
| `Qualifier les contrats OpenCode, secrets et environnement invité` | Compatibility evidence with three checklists | P01-P03 |
| `Gérer les instances et leur cycle de vie par projet` | Identity, adapter scope, reconciliation | P05-P07 |
| `Protéger et révoquer les identifiants des projets` | Credential storage, bindings, trust and GitHub transport | P08-P10, P13 |
| `Configurer les skills, instructions et MCP par projet` | Project capabilities and unified wizard | P14-P17 |
| `Mettre à jour et importer les environnements existants` | Dependency updates and Albert Code import | P18-P19 |
| `Qualifier l’installation et la parité multiplateforme` | Installers, diagnostics and end-to-end release gates | P20-P21 |

Use French issue/PR prose in this repository. Each PR names its planning identifier and tracker, states prerequisites, documents tests actually run and distinguishes source-derived expectations from real observations. Close a tracker only when all of its acceptance criteria are met; use GitHub's English closing keywords (`Closes #N`) in otherwise French bodies.

For the plan's review, the natural repository destination is `docs/plans/albert-code-parity.md` in a **documentation-only draft PR** linked to #29 and the gist. This downloadable draft does not itself create that PR, commit to the repository, or modify the gist. Implementation begins only after review.

## 7. Maîtrise du périmètre et estimation

The earlier 5-8 engineer-week range is an initial estimate, not a commitment. Re-estimate after P01-P03 using actual credential, browser and platform results. A protected GitHub broker, maintained guest-image pipeline or worktree metadata support can materially change it. Do not absorb those as incidental wizard changes.

The 21 PRs are review boundaries, not equal-sized units. Split P16 if it needs a new image supply chain; split native-store adapters from their common interface if P08 becomes difficult to review. Conversely, combine test-only spike artifacts only if their independent conclusions remain clear.

**Deferred deliberately:** guest-owned clones, simultaneous active projects, host signing-key forwarding, arbitrary secret-manager plugins, broad Docker/other-agent installations, automatic background updates, a runtime/plugin framework, and package-manager distribution beyond the verified installer path.

**Premier lot d'implémentation après approbation :** mettre à jour #29 et créer le suivi de compatibilité ; exécuter P01-P03 tout en implémentant la fondation de configuration pure P04. Examiner leurs preuves avant de câbler des identifiants supplémentaires ou de choisir l'image navigateur de production.
