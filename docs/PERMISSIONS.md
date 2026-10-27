# Permissions

[Product overview](../README.md) · [Standalone installation](../INSTALL.md)

Commands and unlinked file paths below are relative to the repository root.

## Identity and permissions

Register an OIDC application in KyIdentity using callback `<drive origin>/api/sso/kysignon/callback` and back-channel logout URI `<drive origin>/api/sso/kysignon/backchannel-logout`. The routes retain the base's name; set `KY_KYSIGNON_ISSUER` to the actual KyIdentity issuer, with its client ID/secret. Keep `KY_SSO_AUTO_PROVISION=false`. A verified OIDC `sub` must equal the user's SCIM `externalId`; username and email do not link accounts.

Configure a KyIdentity outbound SCIM system at `<drive origin>/scim/v2` with `KY_SCIM_TOKEN`. Enable group delivery and assign the required users/groups. KyDrive supports exact `externalId` reconciliation, user PUT/deactivation, groups PUT/delete and atomic membership replacement. An `externalId` is immutable. Application administrator privilege is separate from directory roles; incoming `roles=admin` cannot elevate it. The break-glass local administrator cannot be modified through SCIM.

Create a workspace in KyDrive and grant a KyIdentity group reader, editor or manager access. Grants inherit throughout the workspace. An administrator can configure quotas/grants and view audits, but needs a group grant to read document content. Workspace files survive deletion of any employee or group. A manager can issue reader/editor service credentials scoped to their workspace, expiring after 90 days and invalidated when that user's membership or account ceases to authorize them.

The main sidebar lists My files and shared workspaces. Each active account gets one private workspace, initially 10GiB; its owner can edit files, and group grants cannot expose it to others. Shared workspaces use group grants. New file creates a blank DOCX, XLSX or PPTX in the selected workspace and opens Euro-Office. The UI also provides folders, upload/download, versions, trash restore, quotas, operational status and audit. The people/group action links to KyIdentity; it does not duplicate the directory or embed another administration application.

To make an installation owner an application administrator, run the audited operator command inside the drive container:

```sh
/app/kydrive-server set-directory-role -subject <exact-KyIdentity-subject> -role admin
```

This preserves the directory account and revokes its existing sessions; sign in with KyIdentity again. Use `-role user` to remove the grant. SCIM roles do not assign application administration, and offboarding still disables administrators. The distinct local `recovery-admin` account is for recovery; deployment credentials belong in an operator-controlled secret location.

## Sharing

Beyond workspace group grants, any folder or file can be shared with a person or a group as reader, editor or manager from its Actions menu. A folder share covers everything inside it. Shares only add access; they never take away what a workspace grant gives. Workspace managers share; editors can share up to editor access when a manager turns on "Editors may share" in Workspace settings. People who received a share, even as manager, cannot pass it on. The owner of My files can share from it too; those shares stop while the owner's account is deactivated and are dropped when an administrator hands the files over. Moving a file to another workspace drops its shares.

An **organisation link** gives anyone signed in to KyDrive who opens it reader or editor access to one file or folder, until it expires (1, 7 or 30 days, or never) or is revoked; create and revoke links in the same Share dialog. The link is shown once. Its secret sits after the `#`, which browsers never send to a server, so it does not appear in proxy logs; every opening is audited.

A **guest link** shares one file or folder with people who have no account. It is off until an administrator turns it on under Settings > Guest links, which also sets the longest a link may last (default 30 days). A guest link is view and download only, or upload-only into a folder, where guests send files without seeing what is there. It always expires, can need a password (send it separately), and stops working if its creator loses the right to share the item. Guest pages send no referrer, ask search engines not to index them and set no cookies; turning guest links off stops all of them at once.

Items shared with you outside your own workspaces appear under **Shared with me**, where you can browse shared folders and open or download files. They are not yet listed over WebDAV, and the search box, by name or by content, covers your workspaces only. Search never shows a file, a match or a snippet you cannot read at that moment; losing access ends it on the next search. Removing a share, or removing someone from a shared group, ends their open editor at once.

## Leavers

A deactivated or deleted account's My files stay until an administrator hands them over. The Leavers panel lists each leaver's size and item count, never file names. Handover moves the whole tree, including trash and versions, into a new folder `Handover from <name> <date>` in a shared workspace or a colleague's My files, revokes open editor sessions and the leaver's service credentials, and is audited. The administrator gains no read access, unless they already hold a grant on the destination. To delete a leaver's files, hand them to a shared workspace and purge them there. The same operation as an audited operator command:

```sh
/app/kydrive-server handover                       # list leavers
/app/kydrive-server handover -from <personal-workspace-id> -to-user <exact-KyIdentity-subject>
/app/kydrive-server handover -from <personal-workspace-id> -workspace <shared-workspace-id>
```

## Retention and permanent delete

Trashed items are permanently deleted after 90 days by default, in every workspace including My files. An administrator changes that age, or turns it off with 0, under Settings > Trash. A manager of a workspace (or the owner of a personal one) can permanently delete trashed files and folders, delete a non-current version, or empty the trash; each action is audited and confirmed in the UI. A file with an open or undelivered editor session cannot be purged. An administrator can set per-workspace retention on shared workspaces: `trash_days` (purge items trashed longer than N days, overriding the drive-wide age; 0 follows it) and `keep_versions` (keep only the newest N versions; 0 is off). An hourly worker applies the policies as actor `system` and logs how many items it skipped. Files trashed before trash timestamps existed count from that upgrade; anything trashed longer ago than the drive-wide age is purged on the first run after upgrading to it. The same worker removes upload staging files and blobs no version names once they are an hour old. Backups still hold purged bytes until their own snapshots are retired. Known limitation: if a purge removes a blob during the hard-link pass that follows a bulk backup's database snapshot, that run fails loudly and is retried at the next interval.
