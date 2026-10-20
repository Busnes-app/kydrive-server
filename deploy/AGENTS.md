# Deployment packages

## Purpose
Local rendering and packaging of the NAS drive and Kubernetes Euro-Office deployment, with an optional single-instance Kubernetes drive profile.

## Ownership
Owns `render.py` and the generated installation contract. Root Compose owns NAS mounts and runtime environment.

## Local Contracts
- Rendering performs no cluster writes, remote connections or readiness claims.
- Generated `INSTALL.txt` names only origins the operator supplied; no deployment's hosts are built in.
- Editor and drive images are digest-pinned for production manifests; workloads target amd64.
- Required credentials are supplied through mode-0600 files and emitted into an output directory at mode 0700. Generated Secret manifests are never committed.
- TLS, ingress class, storage class and independent bulk storage are operator inputs. A PVC alone does not establish an independent backup destination.
- A NAS connection is an external endpoint; kubeconfig grants no NAS authority.

- The non-root drive container sets `allowPrivilegeEscalation: false`, drops all capabilities, uses the `RuntimeDefault` seccomp profile and a read-only root filesystem with an emptyDir at `/tmp`. Euro-Office runs as root by design and keeps its defaults.
- Initialize the bundled service data from the same pinned editor image on the first start, refusing non-empty unmarked destinations. Each directory gets `/seed/.seeded-<name>` after its move and is skipped once marked; stale `/seed/.seed-<name>.*` stages are removed before staging, so a start interrupted between moves resumes. Preserve the source directory owner and mode on staging roots as well as copied contents. Keep each service and editor runtime in a separate subPath; never mount the PVC root over the editor cache.

## Work Guidance

## Verification
- `python -m unittest discover -s deploy -p '*_test.py'`

## Child DOX Index
None.
