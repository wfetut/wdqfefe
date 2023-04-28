#!/bin/bash

set -euo pipefail

source vars.env

dynamo_policy="$STATE_DIR/dynamo-iam-policy"
dynamo_policy_arn="arn:aws:iam::${ACCOUNT_ID}:policy/${CLUSTER_NAME}-dynamo"

s3_policy="$STATE_DIR/s3-iam-policy"
s3_policy_arn="arn:aws:iam::${ACCOUNT_ID}:policy/${CLUSTER_NAME}-s3"


route53_policy="$STATE_DIR/route53-policy"
route53_policy_arn="arn:aws:iam::${ACCOUNT_ID}:policy/${CLUSTER_NAME}-route53"

aws iam create-policy \
    --policy-name "${CLUSTER_NAME}-dynamo" \
    --policy-document "$(cat "$dynamo_policy")"

aws iam create-policy \
    --policy-name "${CLUSTER_NAME}-s3" \
    --policy-document "$(cat "$s3_policy")"

aws iam create-policy \
    --policy-name "${CLUSTER_NAME}-route53" \
    --policy-document "$(cat "$route53_policy")"


# arn:aws:eks:${AWS_REGION}:032205338087:nodegroup/fspm-loadtest/ng-1f0b78c4/88c3da7e-6575-cf4b-ac55-f66bc28f4704
