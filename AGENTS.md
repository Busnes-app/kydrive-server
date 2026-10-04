
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
KyDrive is the suite's general drive backend. KyIdentity owns people/groups; KyDrive owns workspace permissions. Initial delivery builds and tests locally. NAS target: `unraid.urlxl.us` / KyYard `hluswcdata01`; recovery target: `https://kyrecovery.urlxl.us/`. The user authorized the live pilot on 2026-10-04; preserve existing suite data/services and verify production gates.

## Ownership
Root owns product identity, CLI lifecycle, Compose/Docker packaging, shared verification and operator documentation. Domain packages own contracts in the Child DOX Index.

## Local Contracts
- One SQLite writer and immutable filesystem blobs; target linux/amd64. PostgreSQL code inherited from the base is not a supported drive deployment.
- Read `../DRIVE_IMPLEMENTATION_PLAN.md` and `docs/ACCEPTANCE.md` for scope and gates. A local pass is not production readiness or a suite-wide installer.
- Shared files are workspace-owned and survive user/group deletion. Administration does not bypass content grants.
- Production integrations use HTTPS, stable OIDC subject/SCIM externalId mapping, durable secrets and authenticated service/editor APIs.
- Recovery uses `ky-primitives/recoveryclient`; no copied library crypto. Sealed metadata binds the exact independent Restic snapshot and includes every integration secret needed for restore.
- Retain all referenced versions/blobs and Restic snapshots. No automatic destructive garbage collection or bulk pruning.
- `runServer` stops HTTP, cancels the scheduler/editor worker and waits for detached backup handlers before closing the store. Shutdown timeout closes remaining HTTP connections. Compose's 20m grace period exceeds HTTP drain plus the library backup wait budget.
- Keep generated credentials, database files, rendered Secrets, opened capsules and bulk repositories out of Git and Docker build contexts.

## User Preferences
- Bootstrap and operator-reset passwords must be replaced before privileged use; resets revoke sessions/MFA/device grants.
- Euro-Office is the editor; no Nextcloud or ONLYOFFICE product deployment. Inherited Euro code/branding does not prove an independent artifact build chain.
- People/groups live in KyIdentity. Group permission administration lives in KyDrive.
- General drive API covers uploads/downloads, folders, versions, trash, quotas and workspace-scoped service credentials. Desktop sync, WebDAV, SMB, advanced search and HA are deferred.
- Preserve shared Busnes light/dark themes, named themes and saved browser choices.

## Verification
- `PATH=/path/to/restic:$PATH go test -race ./...`, `go vet ./...`, `go mod verify`; Restic-backed tests require the executable.
- `cd web && npm ci && npm test && npm run build`; embedded `web/dist` must match source.
- `scripts/smoke-test.sh` against the built binary; `python -m unittest discover -s deploy -p '*_test.py'`.
- `docker build --platform linux/amd64 -t kydrive-server:local .`; run health/restart/editor/recovery checks locally. Master CI publishes only the image built after passing checks, under its commit tag, and verifies GitHub build provenance; no mutable latest promotion.
- Native T3 browser evidence and remaining gates are recorded in `docs/ACCEPTANCE.md` and `UI-VERIFICATION.md`.

## Child DOX Index
- `docs/` — operator restore runbook, local acceptance and UI evidence assets.
- `internal/config/` — environment parsing, durable secrets and configuration validation.
- `internal/store/` — base identity repositories, atomic group replacement and SQLite initialization.
- `internal/drive/` — workspace/file domain, immutable blob publication, grants and editor/service sessions.
- `internal/editor/` — Euro-Office JWT, scoped output fetching and revocation commands.
- `internal/crypto/` — inherited application encryption helpers over shared primitives.
- `internal/auth/` — local/bootstrap authentication, MFA, sessions, CAPTCHA and CSRF.
- `internal/sso/` — OIDC verification and inherited federation helpers.
- `internal/scim/` — KyIdentity-compatible Users/Groups provisioning.
- `internal/backup/` — recoveryclient adapter, consistent metadata, Restic bulk binding and restore drill.
- `internal/devices/` — inherited ephemeral device pairing.
- `internal/testdb/` — disposable test databases.
- `internal/api/` — HTTP transport, authorization middleware and editor page.
- `web/` — shared-drive UI, recovery administration, theme behavior and browser checks.
- `deploy/` — reviewable NAS/Kubernetes rendering contracts and tests.
