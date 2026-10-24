
# DOX framework

- DOX is highly performant AGENTS.md hierarchy installed here
- Agent must follow DOX instructions across any edits

## Core Contract

- AGENTS.md files are binding work contracts for their subtrees
- Work products, source materials, instructions, records, assets, and durable docs must stay understandable from the nearest applicable AGENTS.md plus every parent AGENTS.md above it

## Read Before Editing

1. Read the root AGENTS.md
2. Identify every file or folder you expect to touch
3. Walk from the repository root to each target path
4. Read every AGENTS.md found along each route
5. If a parent AGENTS.md lists a child AGENTS.md whose scope contains the path, read that child and continue from there
6. Use the nearest AGENTS.md as the local contract and parent docs for repo-wide rules
7. If docs conflict, the closer doc controls local work details, but no child doc may weaken DOX

Do not rely on memory. Re-read the applicable DOX chain in the current session before editing.

## Update After Editing

Every meaningful change requires a DOX pass before the task is done.

Update the closest owning AGENTS.md when a change affects:

- purpose, scope, ownership, or responsibilities
- durable structure, contracts, workflows, or operating rules
- required inputs, outputs, permissions, constraints, side effects, or artifacts
- user preferences about behavior, communication, process, organization, or quality
- AGENTS.md creation, deletion, move, rename, or index contents

Update parent docs when parent-level structure, ownership, workflow, or child index changes. Update child docs when parent changes alter local rules. Remove stale or contradictory text immediately. Small edits that do not change behavior or contracts may leave docs unchanged, but the DOX pass still must happen.

## Hierarchy

- Root AGENTS.md is the DOX rail: project-wide instructions, global preferences, durable workflow rules, and the top-level Child DOX Index
- Child AGENTS.md files own domain-specific instructions and their own Child DOX Index
- Each parent explains what its direct children cover and what stays owned by the parent
- The closer a doc is to the work, the more specific and practical it must be

## Child Doc Shape

- Create a child AGENTS.md when a folder becomes a durable boundary with its own purpose, rules, responsibilities, workflow, materials, or quality standards
- Work Guidance must reflect the current standards of the project or user instructions; if there are no specific standards or instructions yet, leave it empty
- Verification must reflect an existing check; if no verification framework exists yet, leave it empty and update it when one exists

Default section order:
- Purpose
- Ownership
- Local Contracts
- Work Guidance
- Verification
- Child DOX Index

## Style

- Keep docs concise, current, and operational
- Document stable contracts, not diary entries
- Put broad rules in parent docs and concrete details in child docs
- Prefer direct bullets with explicit names
- Do not duplicate rules across many files unless each scope needs a local version
- Delete stale notes instead of explaining history
- Trim obvious statements, repeated rules, misplaced detail, and warnings for risks that no longer exist

## Closeout

1. Re-check changed paths against the DOX chain
2. Update nearest owning docs and any affected parents or children
3. Refresh every affected Child DOX Index
4. Remove stale or contradictory text
5. Run existing verification when relevant
6. Report any docs intentionally left unchanged and why

## Purpose
KyDrive is the suite's general drive backend. KyIdentity owns people/groups; KyDrive owns workspace permissions. Initial delivery builds and tests locally. Pilot NAS (via KyYard) and KyRecovery hosts are private and stay out of public docs; `docs/PILOT.md` uses placeholders. The user authorized the live pilot on 2026-10-04; preserve existing suite data/services and verify production gates.

## Ownership
Root owns product identity, CLI lifecycle, Compose/Docker packaging, shared verification and operator documentation. Domain packages own contracts in the Child DOX Index.

## Local Contracts
- `apply-setup` is the installer boundary: bounded private bundle, capabilities, read-only check and explicit graceful restart. A private data-directory Unix socket reaches the running server. New directory identities and settings are audited and owned by stable setup ID; existing accounts are never adopted. See `docs/INSTALLER-SETUP.md` for supported fields and remaining setup gates.
- `apply-setup --stage` prepares immutable secret-free metadata at
  `<KY_DATA_DIR>/installer/<setup UUID>/setup.json` through shared `applysetup` authority.
  It requires protected data-directory ancestry; check creates nothing. Preparation
  requires native SCIM references for SSO and creates no credentials, identities or settings.
- One SQLite writer and immutable filesystem blobs; target linux/amd64. PostgreSQL code inherited from the base is not a supported drive deployment.
- Blobs are encrypted at rest under the deployment key (`internal/drive`). This protects a stolen disk or copied data directory only when the key lives outside it (`KY_ENCRYPTION_KEY`); startup and the Recovery screen warn when it is `data/encryption.key`. File and folder names are encrypted the same way; workspace names, group names and service-token names stay plaintext (admin configuration). The content search index (`internal/search`, `<data>/search/index.db`) stores keyed term hashes only: it reveals word-frequency and shared-word patterns, never words, and is in no backup. Backups and capsules taken before an upgrade still hold plaintext names. It is not end-to-end: the server, its operator and Euro-Office read plaintext.
- Read `../DRIVE_IMPLEMENTATION_PLAN.md` and `docs/ACCEPTANCE.md` for scope and gates. A local pass is not production readiness or a suite-wide installer.
- Personal files belong to one active account’s private workspace until an admin handover moves a leaver's tree to a shared workspace or a colleague's My files (UI or audited `handover` CLI); shared files use group grants. Any folder or file, personal ones included, can also be shared with people or groups as reader, editor or manager (shares only add; a folder share covers its subtree; a deactivated owner's personal shares stop until handover, which drops them). The sidebar lists My files, Shared with me and shared workspaces directly. New file creates blank DOCX/XLSX/PPTX and opens Euro-Office.
- Shared files are workspace-owned and survive user/group deletion. Administration does not bypass content grants.
- Production integrations use HTTPS, stable OIDC subject/SCIM externalId mapping, durable secrets and authenticated service/editor APIs.
- Recovery uses `ky-primitives/recoveryclient`; no copied library crypto. Sealed metadata binds the exact Restic snapshot and includes every integration secret needed for restore. `KYDRIVE_BULK_BACKUP_REPOSITORY` is a local path outside the data directory or an offsite `s3:https://` or `rest:https://` repository; credentials come only from `KYDRIVE_BULK_BACKUP_CREDENTIALS_FILE` (allowlisted S3 and rest-server keys), reach only the restic child, and are sealed in the capsule so a restore needs nothing else. A weekly `restic check --read-data-subset=5%` follows a scheduled deposit. NAS-local snapshots are not independent recovery copies; the pilot has not yet configured an offsite one.
- Versions and blobs are removed only by an explicit manager purge of trashed items or old versions, by retention, or as orphans; purges and retention are audited. Retention: a drive-wide trash age (admin Settings, default 90 days, 0 = off) applies to every workspace including My files, a shared workspace's own `trash_days` overrides it, and `keep_versions` is per shared workspace, off by default. Metadata commits before blob removal, and a blob is removed only when the purging transaction found it unreferenced, or by the hourly orphan sweep once no version names it and it is over an hour old. Restic bulk snapshot pruning (admin `bulk_retention` policy, run by the scheduler at most daily, audited as `system`) is opt-in, off by default, never forgets the newest KY_BACKUP_KEEP deposit snapshots. KyRecovery-held capsules older than the policy may name forgotten snapshots: set the policy to outlive KyRecovery's retention. An append-only rest-server or Object Lock repository refuses prune by design.
- Compose and the Kubernetes drive pod drop all capabilities, forbid privilege escalation and mount the root filesystem read-only with a `/tmp` tmpfs/emptyDir; the server writes only to its data, backups and bulk volumes and `/tmp` (Restic packs, SQLite temp files). CLI subcommands run in the container need output paths on those volumes (e.g. `export-capsule -out /app/backups/...`). Subcommands that operate on a deployment refuse a missing key or database (`config.LoadExisting`) rather than creating one; only the server and `init-admin` create them.
- `runServer` stops HTTP, cancels the scheduler, editor, retention, blob-sealing and search index workers and any backup still collecting its payload, and waits for them and detached backup handlers before closing the store (the search index is closed after its worker returns). Shutdown timeout closes remaining HTTP connections. Compose's 20m grace period exceeds HTTP drain plus the library backup wait budget.
- `apply-setup --capabilities` advertises `restore-reset` without opening data, so filesystem-restore orchestrators can refuse older images before changing their target and invoke offline `restore-reset -to <root>` before startup. Capsule `restore` invalidates restored bearer grants before returning success. A failed reset keeps the server offline until `restore-reset -to <opened>` succeeds; it uses only the extracted database and key and preserves content, accounts and integration config. `docs/RESTORE.md` owns reconciliation of directory and password changes after the snapshot.
- Keep generated credentials, database files, rendered Secrets, opened capsules and bulk repositories out of Git and Docker build contexts.
- `docker-compose.yml` and `.env.example` default `KY_IMAGE` to `ghcr.io/busnes-app/kydrive-server:stable`, which `release-image.yml` moves to each newest release, `vX.Y.Z.N` included (`TestComposeDefaultsToStable`, `internal/api`; quick start; the README tells production to pin the digest); `docker-compose.build.yml` overrides it with the local `kydrive-server:local` build. The agent skill's `version` is the line in `internal/appversion/VERSION` (`TestAgentSkill`); bump it with `VERSION`.

## User Preferences
- Bootstrap and operator-reset passwords must be replaced before privileged use; resets revoke sessions/MFA.
- Euro-Office may run in production on Docker (`docker-compose.office.yml`, `docs/EURO-DOCKER.md`) or Kubernetes; both require the existing provenance, HTTPS and acceptance gates.
- Euro-Office is the editor; no Nextcloud or ONLYOFFICE product deployment. Inherited Euro code/branding does not prove an independent artifact build chain.
- People/groups live in KyIdentity. Group permission administration lives in KyDrive. The installation owner’s KyIdentity account must receive an explicit KyDrive administrator grant; do not leave owner administration dependent on an undisclosed local recovery login. Use the audited set-directory-role CLI with the exact directory subject.
- Reserve a distinct local recovery administrator username when the directory also uses `admin`; use the audited `rename-local-admin -from admin -to recovery-admin` operator command on existing installations. Directory provisioning never adopts the local account.
- General drive API covers uploads (single-request and resumable), downloads, folders, versions, trash, quotas, search across readable workspaces by file name and, unless `KYDRIVE_CONTENT_SEARCH=off`, by the words in Office, OpenDocument, text and (with Euro-Office) PDF files, folder zip downloads and workspace-scoped service credentials. WebDAV at `/dav/` (app passwords, `internal/dav`). AI agents over MCP at `/mcp` (agent tokens, `internal/api` `mcp.go`; skill in `internal/agentskill`): read or read-and-write, optionally limited to one folder, trash only; reads text files and the extracted text of Office/OpenDocument files (in process, `internal/extract`) and PDFs (through Euro-Office when configured). Desktop sync (M7): client mtimes, a per-workspace change feed and snapshot, and per-device credentials linked by browser approval (`internal/api` `drive_changes.go`, `devices.go`) serve the `Busnes-app/kydrive-desktop` client, which is not yet released. SMB and HA are deferred.
- Preserve shared Busnes light/dark themes, named themes and saved browser choices.
- OneDrive/SharePoint parity decisions (2026-10-07), planned in `docs/superpowers/plans/2026-10-07-onedrive-parity-roadmap.md`: per-workspace opt-in retention (admin-set, off by default) plus manager purge; an admin hands a leaver's My files to a shared workspace or named colleague (shipped); sharing covers people, per-folder grants, organisation links and external guest links; independent bulk backup goes offsite (S3-compatible or rest-server over HTTPS, shipped). Current contracts above stay binding until each milestone ships and rewrites them.

## Verification
- `python3 scripts/accept-office-compose.py` runs the optional editor image in a disposable bounded Compose project: unsafe JWT refusal, readiness and volume persistence after recreation; no Drive editing or production provenance claim.
- `PATH=/path/to/restic:$PATH go test -race ./...`, `go vet ./...`, `go mod verify`; Restic-backed tests require the executable.
- `go test -race ./cmd/server -run '^TestInstallerPreparation$' -count=1` checks native
  metadata staging/check/resume and input refusal without UI rendering or a running server.
- `cd web && npm ci && npm test && npm run build` before Go builds/tests that need the UI. `web/dist` is build output embedded by `go:embed`, not committed (only `.gitkeep`); CI and the Docker image build it from source. Then `go build -o .browser/server ./cmd/server && cd web && npm run test:browser`.
- `scripts/smoke-test.sh .browser/server` (binary path argument; defaults to `./kydrive-server`) against the built binary; `python -m unittest discover -s deploy -p '*_test.py'`.
- The `test` job's `release binary is repeatable` step rebuilds `web/dist` and `./cmd/server` the way the Dockerfile does (`-trimpath`, from `git archive` copies) in two directories and requires identical bytes. Keep the Dockerfile's `go build` and that step in step; `docs/VERIFYING-RELEASES.md` gives users the same commands.
- `docker build --platform linux/amd64 -t kydrive-server:local .`; run health/restart/editor/recovery checks locally. Master CI publishes only the image built after passing checks, under its commit tag, and verifies GitHub build provenance; no mutable latest promotion.
- The version has one source: `internal/appversion/VERSION` (`X.Y.Z`, the release line; `web/package.json` has no version). Go code calls `appversion.String()`; a release image build sets the tag's full version through `-ldflags -X …/internal/appversion.release` (Dockerfile `ARG VERSION`), so images report `X.Y.Z` or `X.Y.Z.N` and source builds report the line.
- Publishing a GitHub release `vX.Y.Z` or `vX.Y.Z.N` runs `release-image.yml` (a dispatch on a published release's tag runs it too). It refuses a malformed tag, a tag whose `X.Y.Z` disagrees with `internal/appversion/VERSION`, and any commit on which a ci.yml job in its `REQUIRED` list (`test`) did not pass. It then builds `:X.Y.Z[.N]` with provenance and SBOM, then attests and verifies it (identity `release-image.yml@refs/tags/<tag>`). Before pushing it refuses a version tag that already exists in GHCR, and fails on any lookup error other than not found; GHCR itself does not lock tags. It then moves `:stable` to that digest only for the highest non-prerelease, and asserts and verifies `:stable`. `:stable` is the only mutable tag; master CI publishes commit tags only. A job added to ci.yml must be added to `REQUIRED` too.
- `dependency-release.yml` runs daily at 23:00 UTC: if Dependabot commits landed on `master` since the newest release on the current line and the push CI run on `master`'s tip succeeded, it publishes the next `vX.Y.Z.N` (one per day however many PRs merged) and dispatches `release-image.yml` on it, since a `GITHUB_TOKEN` release raises no release event. Run by hand, it releases `master` without the Dependabot condition. It waits while the line in `VERSION` has no `vX.Y.Z` release: bump `VERSION` and publish `vX.Y.Z` by hand for a new line.
- Native T3 browser evidence and remaining gates are recorded in `docs/ACCEPTANCE.md` and `UI-VERIFICATION.md`.

## Child DOX Index
- `docs/` — operator restore runbook, local acceptance and UI evidence assets, release verification and the assurance case.
- `CONTRIBUTING.md` (root-owned) — contribution process, test policy and the coding standard with the CI step enforcing each.
- `internal/config/` — environment parsing, durable secrets and configuration validation.
- `internal/store/` — base identity repositories, atomic group replacement and SQLite initialization.
- `internal/drive/` — workspace/file domain, immutable blob publication, grants and editor/service sessions.
- `internal/dav/` — WebDAV file system over the drive store for app-password clients.
- `internal/editor/` — Euro-Office JWT, scoped output fetching and revocation commands.
- `internal/crypto/` — inherited application encryption helpers over shared primitives.
- `internal/auth/` — local/bootstrap authentication, MFA, sessions, CAPTCHA and CSRF.
- `internal/sso/` — KySignOn OIDC verification and signed directory webhook.
- `internal/scim/` — KyIdentity-compatible Users/Groups provisioning.
- `internal/backup/` — recoveryclient adapter, consistent metadata, Restic bulk binding and restore drill.
- `internal/testdb/` — disposable test databases.
- `internal/api/` — HTTP transport, authorization middleware, editor page and the MCP endpoint.
- `internal/agentskill/` — the embedded AI agent skill served at `/agent/skills/kydrive/SKILL.md`.
- `internal/extract/` — bounded, standard-library text extraction from Office Open XML and OpenDocument files.
- `internal/search/` — content search: tokenizer, blind index database (`<data>/search/index.db`) and snippets.
- `web/` — shared-drive UI, recovery administration, theme behavior and browser checks.
- `deploy/` — reviewable NAS/Kubernetes rendering contracts and tests.

## Operator documentation

`INSTALL.md` is the root-owned standalone entry point. When installation prerequisites, bootstrap, persistence or verification change, update it together with `README.md` and the linked operator guides.


## README and topic guides

Keep `README.md` as the concise product overview and navigation. Detailed procedures live in `docs/DEVELOPMENT.md`, `docs/DEPLOYMENT.md`, `docs/PERMISSIONS.md`, `docs/CLIENTS-AND-EDITOR.md`, `docs/RECOVERY-AND-ENCRYPTION.md`, `docs/VERIFYING-RELEASES.md`, `docs/ASSURANCE-CASE.md`. Update the owning topic guide and preserve README links when a procedure changes. Commands in these guides run from the repository root.
