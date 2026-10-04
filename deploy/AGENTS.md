# Deployment packages

## Purpose
Local rendering and packaging of the NAS drive and Kubernetes Euro-Office deployment, with an optional single-instance Kubernetes drive profile.

## Ownership
Owns `render.py` and the generated installation contract. Root Compose owns NAS mounts and runtime environment.

## Local Contracts
- Rendering performs no cluster writes, remote connections or readiness claims.
- Editor and drive images are digest-pinned for production manifests; workloads target amd64.
- Required credentials are supplied through mode-0600 files and emitted into an output directory at mode 0700. Generated Secret manifests are never committed.
- TLS, ingress class, storage class and independent bulk storage are operator inputs. A PVC alone does not establish an independent backup destination.
- A NAS connection is an external endpoint; kubeconfig grants no NAS authority.

## Work Guidance

## Verification
- `python -m unittest discover -s deploy -p '*_test.py'`

## Child DOX Index
None.
