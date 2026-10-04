**Repo:** KyDrive-server
**Worktree:** /home/yoshi/git/busnes.app/KyDrive-server (branch work/drive)

# Deployment pilot — 2026-10-04

The user authorized the next deployment pilot. Prior local acceptance is in `ACCEPTANCE.md`. Starting with current-state/access checks, source-to-editor artifact provenance, review/commit/image publication, NAS storage, Kubernetes editor, HTTPS, identity/recovery and live acceptance.

K80 SSH and its kubernetes-admin context are available; both amd64 nodes are Ready. The cluster has no StorageClass or IngressClass, so deployment must follow existing Retain local storage and source-restricted Nginx routing, not assume the generic renderer can be applied unchanged. Direct NAS SSH at 192.168.1.90:22 is refused; investigate the existing KyYard endpoint rather than mistaking the proxy hostname for the NAS. GitHub is authenticated. No pilot deployment resources have been created yet.

Do not claim production readiness from local tests. Preserve existing services, keys and user data. Keep generated credentials and private manifests outside the workspace. Production editor provenance, actual independent bulk storage and live integration gates are still open.
