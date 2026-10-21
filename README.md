# KyDrive

**Start here:** [Standalone installation](INSTALL.md). Browse, upload, download and organize files in permissioned workspaces, share files, retain versions and trash, and link desktop sync clients. Staff sign-in and provisioning require KyIdentity; the local bootstrap account is for administration. Euro-Office is an optional separate editor service. Recovery needs both sealed metadata and the independent bulk repository.

Open-source personal and shared drive for the Ky suite. KyIdentity owns people and groups; KyDrive owns workspace permissions. Euro-Office is the first editor integration. The drive stores arbitrary files and exposes a workspace-scoped service API for other suite applications.

Initial deployment: one `linux/amd64` Docker container on the NAS, Euro-Office in Kubernetes. The same drive image and SQLite/filesystem model also run on a single Kubernetes persistent volume. Verified pilot deployment evidence and remaining gates are in [PILOT.md](docs/PILOT.md).

## Scope

Single-instance SQLite and filesystem storage. No SMB endpoint, released desktop app (in development, see Linked computers), guest editing, automatic version deletion unless a workspace retention policy is enabled, or HA. Interrupted unpublished uploads leave unreferenced blobs until the hourly sweep removes them; abandoned resumable uploads keep their sealed staging under `<data>/staging` for 24 hours. Filename search decrypts names per request (about 0.2 s per 50 000 names on the reference host). Search also matches whole words in the current version of Office, OpenDocument, text and (with Euro-Office) PDF files, a few seconds after they change; `KYDRIVE_CONTENT_SEARCH=off` turns that off. Its index holds keyed hashes of words, not words, and is in no backup: it is rebuilt after a restore, and it reveals to someone holding the disk which files share words and how often words occur ([Encryption at rest](docs/RECOVERY-AND-ENCRYPTION.md#encryption-at-rest)). A folder zip or the completion of a very large upload must finish within `KY_WRITE_TIMEOUT` (at most 15m). An acknowledged save refers to durably flushed bytes plus a committed database revision.

KyDrive inherits the MIT-licensed server base. Euro-Office is a separate AGPLv3 service; Restic is BSD-2-Clause. Keep each dependency's license and source obligations with its distribution.

When a directory user is also named `admin`, reserve a distinct name for the local recovery administrator before provisioning: `/app/kydrive-server rename-local-admin -from admin -to recovery-admin`. Run this operator command with the deployment environment and persistent data mounted. It atomically renames only a local administrator, preserves its credentials and privileges, and records an audit row. Directory accounts are never adopted as local administrators.

## What it can do

- Upload, organize, share and version files in permissioned workspaces, and find them by name or by the words in them.
- Use WebDAV or link compatible desktop sync clients.
- Let AI agents read and organize files over MCP, with an access level and an optional folder limit per agent.
- Configure optional Euro-Office editing and document previews.
- Recover both sealed metadata and the bound bulk-file snapshot.

## Connect an AI agent

AI agents such as OpenClaw and Hermes Agent reach your files over MCP at
`https://<your host>/mcp`. On the **Account** page, under **App passwords**, choose **AI agent**
and an access level:

- **Read only** (the default): the agent lists, searches (by name and by content) and reads your files, including the
  text of Word, Excel, PowerPoint and OpenDocument files, and of PDFs when Euro-Office is set up.
- **Read and write**: it can also create, replace, rename, move and trash files.

Optionally limit the agent to one folder: it then sees that folder as its whole drive and
nothing outside it. An agent never has more access than you do, and none can delete anything
for good: trashed items stay in KyDrive's trash, and a replaced file keeps its old version. The
page shows the token once, with config for both agents that reads it from `KYDRIVE_TOKEN`, and
the command to install the KyDrive skill (served at `/agent/skills/kydrive/SKILL.md`), which
teaches the agent the tools and when to ask you first. You can change an agent's access there
later. An agent token unused for 90 days is revoked; an agent token never works over WebDAV,
nor a WebDAV password over MCP. Details are in [Clients and editor](docs/CLIENTS-AND-EDITOR.md#ai-agents).

A web page can never obtain or use a token: `/mcp` refuses every browser request, sends no CORS
headers, and tokens are created only from KyDrive's own pages, which always name the agent and
the access it gets.

File names and contents can come from other people, and an agent may treat them as
instructions. Give an agent write access only if you trust everyone who can message it.

## Documentation

Euro-Office can also run on Docker with the optional [production editor Compose overlay](docs/EURO-DOCKER.md); Kubernetes remains supported.

- [Development](docs/DEVELOPMENT.md) — Local development, Verification. <a id="local-development"></a><a id="verification"></a>
- [Deployment](docs/DEPLOYMENT.md) — NAS package, Kubernetes rendering. <a id="nas-package"></a><a id="upgrading"></a><a id="kubernetes-rendering"></a>
- [Permissions](docs/PERMISSIONS.md) — Identity and permissions, Sharing, Leavers, Retention and permanent delete. <a id="identity-and-permissions"></a><a id="sharing"></a><a id="leavers"></a><a id="retention-and-permanent-delete"></a>
- [Clients and editor](docs/CLIENTS-AND-EDITOR.md) — Previews, WebDAV, Linked computers, AI agents, Editor. <a id="previews"></a><a id="webdav"></a><a id="linked-computers"></a><a id="editor"></a>
- [Recovery and encryption](docs/RECOVERY-AND-ENCRYPTION.md) — Encryption at rest, Recovery. <a id="encryption-at-rest"></a><a id="recovery"></a>
