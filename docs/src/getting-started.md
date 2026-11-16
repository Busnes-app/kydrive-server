# Getting started

## Install and configure

Follow the NAS package instructions in the product reference. Prepare persistent storage and private environment files with the configured container UID/GID. SQLite needs working local filesystem locks and fsync; do not place its database on an SMB mount.

Configure the HTTPS origin, a durable session secret and the documented KyIdentity OIDC/SCIM connection. An OIDC subject must match the provisioned SCIM `externalId`; matching email addresses do not link accounts.

## Establish owner access

Use the audited `set-directory-role` operator command documented in the product reference to grant the installation owner's exact directory subject application administrator access. This is separate from SCIM provisioning. Verify administrator access before retiring any recovery login.

Create or select a workspace, grant the appropriate directory group access, then test upload and download with an everyday member. Configure Euro-Office separately before testing document editing.
