# canarino

![canarino logo](assets/logo.png)

In-cluster **VM canary** operator for OpenShift Virtualization: continuous and on-demand guest probes via CRs.

Italian *canarino* = canary. Manual = create a **Run**. Periodic = create a **Schedule**. Spoke install per cluster; fleet trends via central Prometheus.

Ad-hoc multi-cluster campaigns stay in the sibling tool: [`ocp-virt-ux-bench`](https://github.com/yordangit/ocp-virt-ux-bench).

## Product surface

| Kind | Role |
|------|------|
| `CanarinoRun` | One-shot probe campaign |
| `CanarinoSchedule` | Emits Runs on an interval |

API group (provisional): `canarino.io` — confirm DNS ownership before publish.

## License

Apache License 2.0

## Status

Design phase — naming locked; no operator code yet.
