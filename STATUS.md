**Repo:** KyDrive-server
**Worktree:** /home/yoshi/git/busnes.app/KyDrive-server (branch master)

# Live pilot — 2026-10-04

Local seven-phase acceptance is complete. The live pilot has NAS storage, Kubernetes Euro-Office, HTTPS, KyIdentity SSO/SCIM user/group delivery, shared manager access, all three new Office file types and sealed KyRecovery deposits with passing metadata/bulk restore drills. Current image/source and receipts are in `docs/PILOT.md`.

The latest user requests are deployed: My files is private to its active owner; group-owned shared workspaces appear directly in the main sidebar. New file creates DOCX/XLSX/PPTX in the selected workspace and opens Euro-Office. Redundant Files navigation, nested workspace selection and Pair Device are removed. Local real-domain tests verify personal isolation, rejected grants and offboarding. Actual native UI created and edited all three formats; authenticated saved-content checks verified revision 2 and K/S/P. Three test files are in trash; original Validation.xlsx remains. Desktop/mobile checks have no document overflow.

Current runtime source `c5f4004`; CI 37183103503 passed and provenance was independently verified. Pinned image digest `d98555bfb3067e40e85e90ef89f0698585f3c3f7e75225b0c41470e3cc5ee90e`; audited KyYard recreate succeeded. Personal ownership schema survives restarts. Rollback must use a personal-aware version.

KyRecovery pairing remains pinned to the existing 2-of-5 suite key. Fresh sealed backup and seven-check restore drill include the new personal schema and all file versions. Independent bulk storage remains deferred by the user; NAS-local Restic is not independent recovery. Local operator is recovery-admin; directory Yoshi now has the explicit application administrator grant, while shared content remains governed by group permissions. Native KyIdentity re-login shows admin, Create shared workspace, Recovery and Settings & DB. Role change is audited and old sessions were revoked; SCIM cannot replace the grant, but offboarding still disables access. Credentials stay private under ~/.local/state/kydrive-pilot/.

Remaining pilot gates: live collaboration/offboarding, full editor pod restart/rollback, custodian-led recovery. All three temporary NAS storage helpers were removed. Current UI request is complete; these remaining pilot gates are not a sign-in blocker. DOX contracts/indexes updated; docs ownership is unchanged because this pass adds evidence. Mirror this record, PILOT and the parent plan to myslop before stopping.
