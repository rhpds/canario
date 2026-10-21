# CanarioScore

Optional **grading policy**. Runs that set `spec.scoringRef` to this object’s name are scored once when the suite finishes. Changing a Score later does **not** re-grade past Runs (v1).

API: `canario.rhpds.com/v1alpha1` · Kind: `CanarioScore` · Short: `cscore`

Scores live on **Run status only**. They are intentionally **not** Prometheus metrics (timings are; grades are not).

## Model

- **Lower total points = better** (ux-bench style).
- Modules contribute independently; omitted modules are skipped.
- Per-worker totals → Run rollup = **worst worker** (`max` of worker totals).
- Graded zeros are written explicitly in JSON (`fileops: 0`, etc.).

### Band types

| Kind | Match rule | Typical use |
|------|------------|-------------|
| Upper-bound (`max`) | value &lt; max (omit max = +Inf catch-all); ascending max, first win | Latencies (ms) |
| Lower-bound (`min`) | value ≥ min (omit min = −Inf catch-all); descending min, first win | Throughput / IOPS / Mbps |

## Modules

| Module | Source fields | Notes |
|--------|---------------|-------|
| `responsive` | e.g. `cmdLsEtcMs` | Interactive feel stand-ins |
| `packages` | e.g. `dnfInstallMs` | Package install duration |
| `fileops` | e.g. `create1000Ms` | FS metadata ops |
| `network` | download Mbps + failure penalties | Soft external checks |
| `storage` | fio bands per **tier** | Tiers: `local` \| `remote` \| `object` |

### Storage tiers

Map StorageClass → tier via `modules.storage.storageClassTiers`, or use `defaultTier` (default `remote`). Bands are defined under `tiers.local` / `tiers.remote` / `tiers.object`. Worst disk on a worker feeds that worker’s `storage` points.

## Verdicts

Ordered bands on the run **total**:

```yaml
verdicts:
  - max: 1
    name: excellent
  - max: 3
    name: good
  - max: 5
    name: fair
  - max: 7
    name: below-average
  - name: poor          # catch-all
```

## Example

See `config/samples/canario_v1alpha1_canarioscore.yaml` for a full policy. Minimal wiring:

```yaml
apiVersion: canario.rhpds.com/v1alpha1
kind: CanarioScore
metadata:
  name: default
  namespace: canario-operator
spec:
  modules:
    packages:
      dnfInstallMs:
        - max: 3500
          points: 0
        - max: 6500
          points: 2
        - points: 3
    # … other modules …
  verdicts:
    - max: 3
      name: good
    - name: poor
```

Reference from a Run or Schedule template:

```yaml
spec:
  scoringRef: default
```

## Related

- Consumers: [CanarioRun](canariorun.md)
- Context: [overview](overview.md)
