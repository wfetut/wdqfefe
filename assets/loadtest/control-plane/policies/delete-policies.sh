#!/bin/bash

set -euo pipefail

source vars.env

dynamo_policy_arn="arn:aws:iam::${ACCOUNT_ID}:policy/${CLUSTER_NAME}-dynamo"

s3_policy_arn="arn:aws:iam::${ACCOUNT_ID}:policy/${CLUSTER_NAME}-s3"

route53_policy_arn="arn:aws:iam::${ACCOUNT_ID}:policy/${CLUSTER_NAME}-route53"

aws iam delete-policy \
    --policy-arn "$dynamo_policy_arn"

aws iam delete-policy \
    --policy-arn "$s3_policy_arn"

aws iam delete-policy \
    --policy-arn "$route53_policy_arn"
