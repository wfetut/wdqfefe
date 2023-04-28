# control-plane

## Quickstart

- Ensure aws cli is authenticated.

- Set up eks cluster and load credentials into tctl.

- Set up `vars.env`.  Note that the name of the eks cluster must match
the `CLUSTER_NAME` variable.

- Invoke `./apply.sh` to spin up the teleport control plane and monitoring stack.

- Use `./monitoring/port-forward.sh` to forward the grafana ui.

- When finished, first invoke `./clean-non-kube.sh`, then destroy the eks cluster (this ordering
is important, as some of the resources created by these scripts interfere with eks cluster teardown).
