# Install

Two tracks:

| Track | When |
|-------|------|
| **Quay + kustomize** (current) | Dev / early v1 installs |
| **OLM** (target) | Proper OpenShift console / OperatorHub UX |

`hack/ocp-redeploy.sh` is a **lab-only** loop (binary BuildConfig → internal registry). Do not use it as the supported install path.

## Prerequisites

- OpenShift with OpenShift Virtualization (KubeVirt + CDI)
- `oc` / `kubectl` and cluster-admin (or equivalent) for first install
- A Ready guest DataSource **or** registry URL for boot disks (`oc get datasource -A`)

## Quay image + kustomize (supported now)

Default image name used in examples:

```text
quay.io/yvarbev/canario:<tag>
```

Replace `<tag>` with a release tag (e.g. `v0.0.1`) or digest.

### 1. Build and push

```bash
export VERSION=0.0.1
export IMG=quay.io/yvarbev/canario:v${VERSION}

make docker-build docker-push IMG=${IMG}
# or: podman build -t ${IMG} . && podman push ${IMG}
```

Ensure the cluster can pull from that Quay repo (public, or pull secret on the operator namespace / global pull secrets).

### 2. Deploy (OpenShift / CNV)

```bash
make deploy IMG=${IMG} KUSTOMIZE_DIR=config/overlays/openshift
```

This applies CRDs, RBAC (including SCC use for virt-launcher exec), the manager Deployment, and ConfigMap `canario-operator-config`.

Vanilla Kubernetes + KubeVirt (no SCC): `KUSTOMIZE_DIR=config/default`.

### 3. Guest defaults

Edit the ConfigMap (or re-apply) so Runs can omit `spec.guestImage`:

```bash
oc -n canario-operator edit cm canario-operator-config
# set CANARIO_DEFAULT_GUEST_DATASOURCE_NAME / _NAMESPACE
# or CANARIO_DEFAULT_GUEST_REGISTRY
oc -n canario-operator rollout restart deploy/canario-controller-manager
```

### 4. Optional: score policy + samples

```bash
oc apply -f config/samples/canario_v1alpha1_canarioscore.yaml
# then a Run / Schedule from config/samples/
```

### 5. Undeploy

```bash
make undeploy KUSTOMIZE_DIR=config/overlays/openshift
# CRDs: make uninstall   # destructive if Runs still exist
```

## Metrics (opt-in)

Does **not** change cluster Prometheus CRs. Creates a ServiceMonitor + metrics-reader token Secret in `canario-operator`.

Requires User Workload Monitoring (or any Prometheus Operator that watches that namespace).

```bash
oc apply -k config/overlays/monitoring
```

Query via Thanos / UWM (examples): `canario_run_info`, `canario_guest_dnf_install_ms`, …

## OLM / OpenShift console (roadmap)

Goal: install and upgrade from the OpenShift UI (OperatorHub / Operator → Installed Operators), with CSV-driven forms for CRs where useful.

Planned shape (operator-sdk / kubebuilder already scaffolded):

1. Set `IMAGE_TAG_BASE=quay.io/yvarbev/canario` and `VERSION`.
2. `make bundle` → operator bundle image (`*-bundle:v$VERSION`).
3. `make bundle-build bundle-push`.
4. Catalog index (`make catalog-build catalog-push`) **or** a CatalogSource pointing at the bundle/index on Quay.
5. Create `Subscription` (UI or YAML) in `canario-operator` (or chosen ns); OLM owns Deployment upgrades.

UI integration checklist (when implementing):

- CSV display name, description, icon (`assets/logo.png`), links to docs
- Owned CRDs: Run, Schedule, Score with useful descriptors / examples
- Install modes: OwnNamespace (and AllNamespaces if we support it)
- OpenShift SCC + operator ConfigMap included in bundle manifests (parity with `config/overlays/openshift`)
- Optional monitoring still via overlay or documented post-install step

Until the catalog is published, use the Quay + kustomize path above.

## Related

- [overview](overview.md)
- CRDs: [Run](canariorun.md) · [Schedule](canarioschedule.md) · [Score](canarioscore.md)
