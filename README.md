# canario

![canario logo](assets/logo.png)

In-cluster **VM canary** operator for OpenShift Virtualization: continuous and on-demand guest probes via CRs.

Spanish *canario* = canary. Manual = create a **Run**. Periodic = create a **Schedule**. Spoke install per cluster; fleet trends via central Prometheus.

Ad-hoc multi-cluster campaigns use [`ocp-virt-ux-bench`](https://github.com/yordangit/ocp-virt-ux-bench).

## Product surface

| Kind | Role |
|------|------|
| `CanarioRun` | One-shot probe campaign |
| `CanarioSchedule` | Emits Runs on an interval |

API group: `canario.rhpds.com`

## License

Apache License 2.0
