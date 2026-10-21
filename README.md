# canario

![canario logo](assets/logo.png)

In-cluster **VM canary** operator for OpenShift Virtualization: on-demand and periodic guest probes via CRs.

Spanish *canario* = canary. Manual = create a **Run**. Periodic = create a **Schedule**. Optional **Score** grades results on the Run status.

Install the operator on each OpenShift cluster you want to probe. Suite timings can go to Prometheus if you enable the optional monitoring scrape (off by default).

Ad-hoc multi-cluster campaigns use [`ocp-virt-ux-bench`](https://github.com/yordangit/ocp-virt-ux-bench).

## Product surface

| Kind | Role |
|------|------|
| `CanarioRun` | One-shot probe (creating it is the trigger) |
| `CanarioSchedule` | UTC cron; emits Runs (`Forbid` if previous still active) |
| `CanarioScore` | Optional grading bands; referenced by `scoringRef` |

API group: `canario.rhpds.com`

Docs: [overview](docs/overview.md) · [Run](docs/canariorun.md) · [Schedule](docs/canarioschedule.md) · [Score](docs/canarioscore.md) · [install](docs/install.md)

## Quick install (Quay + kustomize)

Requires OpenShift Virtualization (KubeVirt + CDI).

```bash
export VERSION=0.0.1
export IMG=quay.io/yvarbev/canario:v${VERSION}

make docker-build docker-push IMG=${IMG}
make deploy IMG=${IMG} KUSTOMIZE_DIR=config/overlays/openshift
```

Then set guest defaults on `canario-operator-config` (DataSource or registry), restart the manager, and apply samples under `config/samples/`.

Full steps, metrics overlay, and **OLM / OperatorHub roadmap** (target for OpenShift console install): [docs/install.md](docs/install.md).

> `hack/ocp-redeploy.sh` is lab-only (in-cluster binary build). Prefer Quay + `make deploy` for real installs; OLM when the catalog is ready.

## Minimal workflow

```bash
# Optional grading policy
oc apply -f config/samples/canario_v1alpha1_canarioscore.yaml

# One-shot
oc apply -f config/samples/canario_v1alpha1_canariorun.yaml

# Or periodic
oc apply -f config/samples/canario_v1alpha1_canarioschedule.yaml

oc get canariorun,canarioschedule -n canario-operator
```

Scores appear on `status.scores` when `scoringRef` is set. Prometheus timings need `oc apply -k config/overlays/monitoring` (UWM).

## Development

```bash
make generate manifests test
# lab redeploy against a logged-in cluster:
./hack/ocp-redeploy.sh
```

## License

Apache License 2.0
