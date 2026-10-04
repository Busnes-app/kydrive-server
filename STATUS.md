**Repo:** KyDrive-server
**Worktree:** /home/yoshi/git/busnes.app/KyDrive-server (branch master)

# Live pilot — 2026-10-04

Local seven-phase acceptance is complete. The live pilot has NAS storage, Kubernetes Euro-Office, HTTPS, KyIdentity SSO/SCIM user/group delivery, shared manager access, all three new Office file types and sealed KyRecovery deposits with passing metadata/bulk restore drills. Current image/source and receipts are in `docs/PILOT.md`.

The latest user requests are deployed: My files is private to its active owner; group-owned shared workspaces appear directly in the main sidebar. New file creates DOCX/XLSX/PPTX in the selected workspace and opens Euro-Office. Redundant Files navigation, nested workspace selection and Pair Device are removed. Local real-domain tests verify personal isolation, rejected grants and offboarding. Actual native UI created and edited all three formats; authenticated saved-content checks verified revision 2 and K/S/P. Three test files are in trash; original Validation.xlsx remains. Desktop/mobile checks have no document overflow.

Current runtime source `109e6f6`; CI 37182399482 passed and provenance was independently verified. Pinned image digest `493de5b520a521e3c152a205591c8cec1a3dc1b805c69200af45b7b39dcd6fa8`; audited KyYard recreate succeeded. Personal ownership schema survives restarts. Rollback must use a personal-aware version.

KyRecovery pairing remains pinned to the existing 2-of-5 suite key. Fresh sealed backup and seven-check restore drill include the new personal schema and all file versions. Independent bulk storage remains deferred by the user; NAS-local Restic is not independent recovery. Local operator is recovery-admin; directory Yoshi remains role user with group-derived shared permissions. Credentials stay private under ~/.local/state/kydrive-pilot/.

Remaining pilot gates: live collaboration/offboarding, full editor pod restart/rollback, custodian-led recovery. All three temporary NAS storage helpers were removed. Current UI request is complete; these remaining pilot gates are not a sign-in blocker. DOX contracts/indexes updated; docs ownership is unchanged because this pass adds evidence. Mirror this record, PILOT and the parent plan to myslop before stopping.
