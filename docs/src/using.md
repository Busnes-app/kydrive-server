# Everyday tasks

## Files and workspaces

Choose My files or a shared workspace in the main navigation. Create folders, upload files or download a copy. New file creates a blank document, spreadsheet or presentation in the selected workspace and opens Euro-Office when the editor integration is configured.

## Permissions

Shared workspaces belong to the workspace, so files survive employee offboarding. Reader, editor and manager group grants control access. An application administrator does not gain document content access merely by administering the service. Manage people and groups in KyIdentity and grants in KyDrive.

## Versions, trash and devices

Use versions and trash to recover earlier content; both count toward quota. Linked desktop and Android clients have their own enrolment and revocation workflows. Confirm server/client compatibility before depending on automatic desktop saves.

## Backups

Recovery needs the sealed metadata capsule and its exact bound Restic bulk snapshot. Retain snapshots named by retained capsules. A NAS-local copy is not independent of NAS failure.

## Export during hosted account deletion

The shared-service tenancy work includes a restricted KyIdentity export sign-in. When
KyIdentity admits you during scheduled deletion, sign in to KyDrive normally: it downloads
`My-files.zip` instead of opening the drive. The export contains your My files, including
trash and every retained version. `manifest.json` records the folder hierarchy and current
revision; `files/<file ID>/<revision>/<name>` contains each version. Shared workspaces and
files that other people shared with you are outside this personal export.

The session lasts at most one hour and permits only export, checking your own sign-in and
signing out. Editing, sharing and linking new devices remain unavailable. Deletion, account
erasure or expiry ends export access. An archive beyond the server's zip size cap is refused.
This is tenancy groundwork; the shared-service startup and acceptance gates remain pending.
Self-hosted single-tenant installs keep their existing download and sign-in behavior.
