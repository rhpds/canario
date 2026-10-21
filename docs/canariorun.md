# CanarioRun

One-shot canary experiment. Creating the object **is** the manual trigger.

API: `canario.rhpds.com/v1alpha1` · Kind: `CanarioRun`

## Spec

| Field | Default | Notes |
|-------|---------|-------|
| `type` | `guest` | Only `guest` in v1 (temporary KubeVirt VM) |
| `profile` | `small` | `small` \| `default` (VM sizing) |
| `workers` | `all` | `all` \| `busiest` \| a worker node name |
| `guestImage` | — | Optional if operator ConfigMap has `CANARIO_DEFAULT_GUEST_*` |
| `guestImage.dataSource` | — | CDI DataSource `{name, namespace}` |
| `guestImage.registry` | — | CDI registry URL (exactly one of dataSource / registry) |
| `storage` | `default` | `default` (cluster default SC) \| `list` |
| `storageClasses` | — | Required for meaningful `storage: list` (one data disk per SC) |
| `suite.sections` | operator / `all` | Comma list or `all` |
| `network.*` | operator defaults | `mode` `auto`\|`on`\|`off`; ping/DNS/download/git |
| `scoringRef` | — | Name of a same-namespace `CanarioScore`. Omit = no grading |

Suite sections include: storage, fileops, responsive, packages, compile, cpu, memory, network, git.

## Lifecycle

1. Place on selected worker(s) under node/storage ceilings.
2. Create CioVM(s) (KubeVirt `VirtualMachine` owned by the Run) + data disks.
3. Run in-guest suite; mirror results into `status.workers[]`.
4. On terminal suite: apply `scoringRef` if set → `status.scores`.
5. Publish Prometheus gauges; delete CioVMs / cloud-init Secrets.
6. On Run delete: remove metric series (finalizer `canario.rhpds.com/metrics`).

Phases: `Pending` → `Running` → `Succeeded` \| `Failed`.

## Status (high level)

| Field | Meaning |
|-------|---------|
| `phase` / `message` | Run outcome |
| `suiteStatus` | Aggregate suite |
| `workers[]` | Per-worker CioVM, node, disk + guest timings, optional `scores` |
| `scores` | Worst-worker rollup: `total`, `verdict`, `scoringRef` |
| `startTime` / `completionTime` | Wall clock |

Point fields that were graded show `0` when best band matched (not omitted).

## Metrics

Recorded when the Run finishes (not continuous mid-suite). Common labels: `run`, `uid` (8-char), `profile`; guest series add `node`; disk series add `storage_class`.

Examples: `canario_run_info`, `canario_guest_dnf_install_ms`, `canario_disk_fio_rand_read_iops`, …

**Scores are status-only** — not Prometheus metrics. Enable scrape with the [monitoring overlay](install.md#metrics-opt-in).

## Example

```yaml
apiVersion: canario.rhpds.com/v1alpha1
kind: CanarioRun
metadata:
  name: bench
  namespace: canario-operator
spec:
  type: guest
  profile: small
  workers: busiest
  storage: list
  storageClasses:
    - ocs-external-storagecluster-ceph-rbd
  scoringRef: default
```

Sample: `config/samples/canario_v1alpha1_canariorun.yaml`.

## Related

- Periodic creation: [CanarioSchedule](canarioschedule.md)
- Grading policy: [CanarioScore](canarioscore.md)
- Context: [overview](overview.md)
