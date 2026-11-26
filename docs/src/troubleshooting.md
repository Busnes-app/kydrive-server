# Troubleshooting

- **SSO succeeds but files are inaccessible:** verify SCIM provisioning, exact subject/externalId binding and workspace grants.
- **Owner cannot administer:** directory provisioning does not grant application administration; verify the explicit directory-role grant.
- **Editor fails to save:** check the editor's authenticated HTTPS connection, configured origins and the user's current grants.
- **Quota is full:** retained versions and trash count too. Review retention deliberately; do not remove blobs or Restic snapshots behind the application.
- **Recovery restores metadata but files are missing:** restore the exact bound bulk snapshot using the runbook.
- **Assessing readiness:** verify live identity, HTTPS, editor, and restore flows on your target deployment. A local build or browser fixture does not establish production readiness.

When reporting a problem, include the product version, platform, failed task and a redacted error. Exclude pairing links, device secrets, recovery codes, custodian shares and private content.
