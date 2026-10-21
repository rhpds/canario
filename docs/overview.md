# Canario overview

Canario is an in-cluster **VM canary** operator for OpenShift Virtualization. It runs temporary guest VMs, executes an in-guest UX/bench suite, writes results on the CR status, and (optionally) scrapes timings into Prometheus.

Spanish *canario* = canary. Ad-hoc multi-cluster campaigns stay in [`ocp-virt-ux-bench`](https://github.com/yordangit/ocp-virt-ux-bench). Install Canario on each OpenShift cluster you want to probe.

## Product API

| Kind | Short | Role |
|------|-------|------|
| [`CanarioRun`](canariorun.md) | — | One experiment (manual trigger = create this object) |
| [`CanarioSchedule`](canarioschedule.md) | `csched` | Cron timer that creates Runs from a template |
| [`CanarioScore`](canarioscore.md) | `cscore` | Optional grading policy; referenced by Runs |

API group: `canario.rhpds.com` (`v1alpha1`).

No other product CR names in v1 (no Action / Job / Probe CR).

## Workflow

```text
GitOps / oc apply
    │
    ├─ CanarioRun          → execute now
    └─ CanarioSchedule     → emit CanarioRuns on UTC cron
              │
              ▼
         CanarioRun reconciler
              │  place workers → CioVM(s) + disks → suite → status → cleanup
              │
              ├─ status (phase, workers, suite fields)
              ├─ if scoringRef set → grade via CanarioScore → status.scores
              └─ metrics gauges (timings) until Run is deleted
```

1. **Manual** — `oc apply` a `CanarioRun`. That *is* the trigger.
2. **Periodic** — `oc apply` a `CanarioSchedule`. It only creates Runs; execution is identical.
3. **Scoring** — optional. Omit `scoringRef` for metrics/status-only. Scores are **not** exported to Prometheus.
4. **Metrics** — opt-in scrape via `config/overlays/monitoring` (OpenShift User Workload Monitoring).

## Install-time defaults

The OpenShift overlay ships ConfigMap `canario-operator-config` (envInto the manager). Important keys:

| Key | Purpose |
|-----|---------|
| `CANARIO_DEFAULT_GUEST_DATASOURCE_*` / `CANARIO_DEFAULT_GUEST_REGISTRY` | Boot image when Run omits `guestImage` |
| `CANARIO_NODE_USAGE_CEILING` / `CANARIO_STORAGE_USAGE_CEILING` | Placement gates |
| `CANARIO_DEFAULT_SUITE_*` / `CANARIO_DEFAULT_NETWORK_*` | Suite / network fallbacks |

Set a Ready DataSource for *this* cluster (`oc get datasource -A`) or a registry URL before relying on Runs without `spec.guestImage`.

## Locked decisions (v1)

- One operator install per cluster (no multi-cluster hub CR).
- Lower score points = better (ux-bench style); run rollup = **worst worker**.
- Storage tiers are generic: `local` | `remote` | `object` (not vendor names).
- Schedule cron is **UTC**; concurrency policy **Forbid** only (skip + Warning Event if a previous Run is still active).
- Graded `0` must appear in status JSON (no `omitempty` on score ints).
- Metrics are **per Run** (labels include `run`, short `uid`, `profile`, and where relevant `node` / `storage_class`). Series are removed when the Run is deleted.

## Non-goals (v1)

- Re-scoring past Runs when a Score CR changes
- Score values in Prometheus
- Allow / Replace concurrency on Schedule
- Nested / nested-VM experiment types beyond `guest`
- Helper ClusterRoles for every CR (operator uses `manager-role`; optional human editor roles are cosmetic)

## Docs map

| Doc | Contents |
|-----|----------|
| [canariorun.md](canariorun.md) | Spec, status, examples |
| [canarioschedule.md](canarioschedule.md) | Cron, Forbid, history |
| [canarioscore.md](canarioscore.md) | Modules, bands, verdicts |
| [install.md](install.md) | Quay image deploy + OLM roadmap |
| [../README.md](../README.md) | Quick start |
