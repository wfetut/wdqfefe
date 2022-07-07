.PHONY: create-cluster
create-cluster:
	eksctl create cluster -f cluster.yaml

.PHONY: get-creds
get-creds:
	eksctl utils write-kubeconfig -f cluster.yaml

.PHONY: destroy-cluster
destroy-cluster:
	eksctl delete cluster -f cluster.yaml

.PHONY: expand-yaml
expand-yaml:
	@bash -c "set -a && source ./secrets/secrets.env && set +a && envsubst < $(FILENAME).yaml > $(FILENAME)-gen.yaml"