#!/bin/bash

set -euo pipefail

# set up cluster resources (kube cluster must exist, aws and kubectl must be authenticated,
# and helm repos must be up to date).

log_info() {
    echo "[i] $* [ $(caller | awk '{print $1}') ]" >&2
}

log_info "generating iam policies..."

./policies/gen-policies.sh

log_info "creating iam policies..."

./policies/create-policies.sh

log_info "attaching iam policies..."

./policies/attach-policies.sh attach

log_info "installing monitoring stack..."

./monitoring/install-monitoring.sh

log_info "setting up cert-manager..."

./dns/init-cert-manager.sh

log_info "installing teleport cluster..."

./teleport/install-teleport.sh

log_info "waiting for auths to report ready..."

./teleport/wait.sh auth

log_info "setting up dns record..."

./dns/update-record.sh UPSERT # CREATE|UPSERT|DELETE

log_info "waiting for proxies to report ready..."

./teleport/wait.sh proxy

log_info "switching dynamo to on-demand mode..."

./storage/set-on-demand.sh

log_info "setting grafana admin password..."

./monitoring/set-password.sh
