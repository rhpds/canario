# CanarioSchedule

Timer that creates [`CanarioRun`](canariorun.md) objects from an embedded template. It does **not** run the suite itself.

API: `canario.rhpds.com/v1alpha1` · Kind: `CanarioSchedule` · Short: `csched`

## Spec

| Field | Default | Notes |
|-------|---------|-------|
| `schedule` | (required) | Standard 5-field cron, evaluated in **UTC** (`minute hour dom month dow`) |
| `suspend` | `false` | When `true`, no new Runs |
| `concurrencyPolicy` | `Forbid` | v1 only supports `Forbid` |
| `historyLimit` | `5` | How many **terminal** owned Runs to keep (newest retained) |
| `template.spec` | (required) | Full `CanarioRunSpec` copied onto each created Run |

## Behaviour

1. Parse cron in UTC. First eligible fire is the next tick **after** Schedule creation (missed fires before create are ignored).
2. If `suspend`: requeue until next tick; do not create.
3. If due and a labeled Run is still non-terminal (`Pending` / `Running` / empty phase):
   - **Forbid** — do not create; set `status.lastSkipTime` / `lastSkipReason`; emit Warning Event `Skipped`; advance schedule time so the same fire is not retried forever.
4. Else create Run:
   - Name: `{scheduleName}-{UTC timestamp}` e.g. `nightly-20261021t120000z` (truncated to 63 chars if needed)
   - Label: `canario.rhpds.com/schedule=<scheduleName>`
   - OwnerReference → Schedule (GC when Schedule is deleted)
5. Prune terminal Runs beyond `historyLimit`. Active Runs are never pruned by history.

## Status

| Field | Meaning |
|-------|---------|
| `lastScheduleTime` | Last successful create (UTC) |
| `lastRunName` | Name of that Run |
| `activeRunName` | Non-terminal Run, if any |
| `lastSkipTime` / `lastSkipReason` | Last Forbid skip |

## Example

```yaml
apiVersion: canario.rhpds.com/v1alpha1
kind: CanarioSchedule
metadata:
  name: every-6h
  namespace: canario-operator
spec:
  schedule: "0 */6 * * *"
  concurrencyPolicy: Forbid
  historyLimit: 5
  template:
    spec:
      type: guest
      profile: small
      workers: busiest
      storage: list
      storageClasses:
        - ocs-external-storagecluster-ceph-rbd
      scoringRef: default
```

Sample: `config/samples/canario_v1alpha1_canarioschedule.yaml`.

## Related

- Created objects: [CanarioRun](canariorun.md)
- Context: [overview](overview.md)
