# KyDrive

**Start here:** [Standalone installation](INSTALL.md). Browse, upload, download and organize files in permissioned workspaces, share files, retain versions and trash, and link desktop sync clients. Staff sign-in and provisioning require KyIdentity; the local bootstrap account is for administration. Euro-Office is an optional separate editor service. Recovery needs both sealed metadata and the independent bulk repository.

Open-source personal and shared drive for the Ky suite. KyIdentity owns people and groups; KyDrive owns workspace permissions. Euro-Office is the first editor integration. The drive stores arbitrary files and exposes a workspace-scoped service API for other suite applications.

Initial deployment: one `linux/amd64` Docker container on the NAS, Euro-Office in Kubernetes. The same drive image and SQLite/filesystem model also run on a single Kubernetes persistent volume. Verified pilot deployment evidence and remaining gates are in [PILOT.md](docs/PILOT.md).

## Scope

Single-instance SQLite and filesystem storage. No SMB endpoint, released desktop app (in development, see Linked computers), guest editing, full-text search, automatic version deletion unless a workspace retention policy is enabled, or HA. Interrupted unpublished uploads leave unreferenced blobs until the hourly sweep removes them; abandoned resumable uploads keep their sealed staging under `<data>/staging` for 24 hours. Filename search decrypts names per request (about 0.2 s per 50 000 names on the reference host). A folder zip or the completion of a very large upload must finish within `KY_WRITE_TIMEOUT` (at most 15m). An acknowledged save refers to durably flushed bytes plus a committed database revision.

KyDrive inherits the MIT-licensed server base. Euro-Office is a separate AGPLv3 service; Restic is BSD-2-Clause. Keep each dependency's license and source obligations with its distribution.

When a directory user is also named `admin`, reserve a distinct name for the local recovery administrator before provisioning: `/app/kydrive-server rename-local-admin -from admin -to recovery-admin`. Run this operator command with the deployment environment and persistent data mounted. It atomically renames only a local administrator, preserves its credentials and privileges, and records an audit row. Directory accounts are never adopted as local administrators.

## What it can do

- Upload, organize, share and version files in permissioned workspaces.
- Use WebDAV or link compatible desktop sync clients.
- Configure optional Euro-Office editing and document previews.
- Recover both sealed metadata and the bound bulk-file snapshot.

## Documentation

Euro-Office can also run on Docker with the optional [production editor Compose overlay](docs/EURO-DOCKER.md); Kubernetes remains supported.

- [Development](docs/DEVELOPMENT.md) — Local development, Verification. <a id="local-development"></a><a id="verification"></a>
- [Deployment](docs/DEPLOYMENT.md) — NAS package, Kubernetes rendering. <a id="nas-package"></a><a id="upgrading"></a><a id="kubernetes-rendering"></a>
- [Permissions](docs/PERMISSIONS.md) — Identity and permissions, Sharing, Leavers, Retention and permanent delete. <a id="identity-and-permissions"></a><a id="sharing"></a><a id="leavers"></a><a id="retention-and-permanent-delete"></a>
- [Clients and editor](docs/CLIENTS-AND-EDITOR.md) — Previews, WebDAV, Linked computers, Editor. <a id="previews"></a><a id="webdav"></a><a id="linked-computers"></a><a id="editor"></a>
- [Recovery and encryption](docs/RECOVERY-AND-ENCRYPTION.md) — Encryption at rest, Recovery. <a id="encryption-at-rest"></a><a id="recovery"></a>
