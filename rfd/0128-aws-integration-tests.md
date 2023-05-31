---
authors: Tiago Silva (tiago.silva@goteleport.com)
state: draft
---

# RFD 128 - AWS End To End Tests

## Required Approvers

- Engineering: `@r0mant && @smallinsky`
- Security: `@reedloden || @jentfoo`

## What

As part of the increase of the reliability of Teleport, we want to add integration
tests for AWS. This will allow us to test the integration with AWS without having
to rely on manual testing.

## Why

We want to increase the reliability of Teleport by adding integration tests for
AWS. This will allow us to test the integration with AWS without having to rely
on manual testing. This will also allow us to test the integration with AWS in
a more reliable process to ensure we don't introduce regressions in the future
or if we do, we can catch them early.

Teleport integrates deeply with AWS to provide a seamless experience for users
when using auto-discovery of nodes, databases and Kubernetes clusters. This
integration is critical for the success of Teleport and we want to ensure that
we can test it reliably.

Each integration test will use the minimum required
AWS API permissions to test the integration. This will ensure that we don't
introduce regressions by changing the permissions required by Teleport but those
changes are not detected because another test requires the same permissions.

The goal of this RFD is to define the scope of the integration tests for AWS
and how we will run them.

## When

AWS integration tests will be added to the Teleport CI pipeline as part of the
cronjob that triggers integrations each day. During the release process, the
integration tests will be run automatically as part of the release process.

## How

This section describes how the end-to-end tests will authenticate with AWS API
and how they will be authorized to perform the required actions.

Teleport AWS account will be configured to allow GitHub OIDC provider to assume
a set of roles. These roles will be assumed by the GitHub actions pipeline to
interact with AWS API.

GitHub action [configure-aws-credentials](https://github.com/aws-actions/configure-aws-credentials)
will be used to handle the authentication with AWS API. This action configures
AWS credentials and region environment variables for use in subsequent steps.
The action implements the AWS SDK credential resolution chain and exports the
following environment variables:

- `AWS_ACCESS_KEY_ID`
- `AWS_SECRET_ACCESS_KEY`
- `AWS_SESSION_TOKEN`

Once the environment variables are set, the AWS SDK that Teleport uses will
automatically use them to authenticate with the AWS API and does not require
any additional configuration.

One of the requirements for the integration tests is that they should use the
minimum required permissions to perform the required actions. This will ensure
that we don't introduce regressions by changing the permissions required by
Teleport. To achieve this, GitHub action will be allowed to assume a set of
roles that will simulate the minimum required permissions that each Teleport
service requires to perform the required actions. Teleport services will be
configured to assume these roles when running the integration tests - requires
changes to the Teleport configuration - so we can test them in isolation.

The AWS account will be configured with a set of roles that will be assumed by
the GitHub actions pipeline. These roles will be configured with the minimum
required permissions to run the integration tests. This will ensure that we
don't introduce regressions by changing the permissions required by Teleport
but those changes are not detected because another test requires the same
permissions.

AWS account configuration will be handled by IAC and will be stored in the
Teleport Terraform repository. This will allow us to track changes to the configuration
and ensure that we can revert them if needed while also allowing us to review
the changes before they are applied. Each End-to-End test will run in a separate
Job so we can configure the assumed role for each test. End-to-End action
requires `id-token: write` permission to the GitHub OIDC provider to generate
the OIDC token that will be used to authenticate with AWS API.

## Tests

This section describes the integration tests that will be added to the Teleport
and what they will test.

### Kubernetes Access

Teleport supports the automatic discovery of Kubernetes clusters running on AWS.
This is done by using the AWS API to discover the Kubernetes clusters running on
the account that matches the configured labels and then using the Kubernetes API
to forward the requests to the cluster.

The first step of this process happens on the Teleport Discovery service and
uses the AWS API credentials to poll the available clusters.
If the discovery service finds a cluster that matches the configured labels, it
will create a Kubernetes cluster - `kube_cluster` - object in the Teleport database.
This object contains the information required to
connect to the Kubernetes cluster. The second step of this process happens on
the Kubernetes Service and uses the information from the `kube_cluster` object
to generate a short-lived token that is used to authenticate with the Kubernetes
API.

The integration tests for Kubernetes access will test the following:

- The discovery service can discover Kubernetes clusters running on AWS and
  create the `kube_cluster` object in the Teleport database. If the object
  already exists, it will be updated with the new labels.
- The Kubernetes service receives the `kube_cluster` object and can generate
  a short-lived token to authenticate with the Kubernetes API.
- The Kubernetes service can forward the requests to the Kubernetes API.
- The Kubernetes service automatically refreshes the token when it's about to
  expire.

#### Requirements

This section describes the requirements for the integration tests for Kubernetes
access.

- One or more EKS control plane clusters running on AWS. We don't need that the
  clusters are running any workloads, we only need that their control plane is
  running and accessible.
- Discovery service configured to discover the EKS clusters and with List and Describe
  permissions to the EKS API. [Permissions](https://goteleport.com/docs/kubernetes-access/discovery/aws/#step-13-set-up-aws-iam-credentials)
- Kubernetes service IAM role configured with the minimum required permissions
  to generate the short-lived token and forward the requests to the Kubernetes
  API. [IAM Mapping](https://goteleport.com/docs/kubernetes-access/discovery/aws/#iam-mapping)

Spin up the EKS clusters takes a long time and it's not feasible to do it for
each test. To speed up the process, we should use an existing EKS cluster that
is already running, accessible and configured to allow Teleport Kubernetes service
to access it. This will allow us to run the integration tests without having to
wait for the EKS cluster to be created. Since we can interact with the EKS API
and we do not need to run any workloads on the cluster, the existing EKS cluster
does not need to have dedicated nodes to run workloads. A single control plane
deployed on a single availability zone is enough.

## Security

Several security considerations must be to be taken into account
when implementing the integration tests for AWS.

### GitHub OIDC Provider

GitHub OIDC provider will be used to authenticate with AWS API. This will allow
us to use GitHub actions to run the integration tests without having to manage
AWS credentials. GitHub actions will be allowed to assume a set of roles that
will simulate the minimum required permissions that each Teleport service
requires to perform the required actions. An important consideration is that
all other OIDC tokens from GitHub that don't belong to the Teleport Enterprise
Account actions or originated from other repositories besides `gravitational/teleport`
must be rejected. This includes tokens from other GitHub
organizations and tokens from the Teleport Enterprise Account that don't belong
to the GitHub actions pipeline.

This is achieved by configuring the AWS federation to only allow JWT tokens with:

1. The issuer - `iss` - set to `token.actions.githubusercontent.com`
2. The audience - `aud` - set to `sts.amazonaws.com`
3. The subject - `sub` - set to `repo:gravitational/teleport:*`. The last `*` matches
    any branch in the `gravitational/teleport` repository and we can further
    restrict it to only allow the `master` branch.

### Credential Lifetime

The default session duration is 1 hour when using the OIDC provider to directly
assume an IAM Role.

We can reduce the session duration with the `role-duration-seconds` parameter
to reduce the risk of credentials being leaked. This parameter can be set to
the timeout of the GitHub action to ensure that the credentials are not valid
after the action finishes.

### Least Privilege

The GitHub actions pipeline will be allowed to assume a set of roles that will
simulate the minimum required permissions that each Teleport service requires
to perform the required actions. We need to protect these roles from being
too permissive or being used to escalate privileges to other resources in the
AWS account.
