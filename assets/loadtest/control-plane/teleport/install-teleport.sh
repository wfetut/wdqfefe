#!/bin/bash

set -euo pipefail

source vars.env

values_yaml="$STATE_DIR/aws-values.yaml"

helm install teleport teleport/teleport-cluster \
  --create-namespace \
  --namespace teleport \
  -f "$values_yaml"
