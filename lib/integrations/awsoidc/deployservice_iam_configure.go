/*
Copyright 2023 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package awsoidc

import (
	"context"
	"log"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/gravitational/trace"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/api/utils/aws"
	awslib "github.com/gravitational/teleport/lib/cloud/aws"
)

// DeployServiceIAMConfigureRequest is a request to configure the DeployService action required Roles.
type DeployServiceIAMConfigureRequest struct {
	// Cluster is the Teleport Cluster.
	// Used for tagging the created Roles/Policies.
	Cluster string

	// IntegrationName is the Integration Name.
	// Used for tagging the created Roles/Policies.
	IntegrationName string

	// Region is the AWS Region.
	// Used to set up the AWS SDK Client.
	Region string

	// IntegrationRole is the Integration's AWS Role used to set up Teleport as an OIDC IdP.
	IntegrationRole string

	// IntegrationRoleDeployServicePolicy is the Policy Name that is created to allow the DeployService to call AWS APIs (ecs, logs).
	// Defaults to DeployService.
	IntegrationRoleDeployServicePolicy string

	// TaskRole is the AWS Role used by the deployed service.
	TaskRole string

	// TaskRoleBoundaryPolicyName is the name to be used to create a Policy to be used as boundary for the TaskRole.
	TaskRoleBoundaryPolicyName string

	// AccountID is the AWS Account ID.
	// Optional. sts.GetCallerIdentity is used if the property is not provided.
	AccountID string

	// ResourceCreationTags is used to add tags when creating resources in AWS.
	ResourceCreationTags awsTags

	// PartitionID is the AWS Partition ID.
	// Eg, aws, aws-cn, aws-us-gov
	// https://docs.aws.amazon.com/IAM/latest/UserGuide/reference-arns.html
	partitionID string
}

// CheckAndSetDefaults ensures the required fields are present.
func (r *DeployServiceIAMConfigureRequest) CheckAndSetDefaults() error {
	if r.Cluster == "" {
		return trace.BadParameter("cluster is required")
	}

	if r.IntegrationName == "" {
		return trace.BadParameter("integration name is required")
	}

	if r.Region == "" {
		return trace.BadParameter("region is required")
	}

	if r.IntegrationRole == "" {
		return trace.BadParameter("integration-role is required")
	}

	if r.IntegrationRoleDeployServicePolicy == "" {
		r.IntegrationRoleDeployServicePolicy = "DeployService"
	}

	if r.TaskRole == "" {
		return trace.BadParameter("task-role is required")
	}

	if r.TaskRoleBoundaryPolicyName == "" {
		r.TaskRoleBoundaryPolicyName = r.TaskRole + "Boundary"
	}

	if r.ResourceCreationTags == nil {
		r.ResourceCreationTags = DefaultResourceCreationTags(r.Cluster, r.IntegrationName)
	}

	r.partitionID = aws.GetPartitionFromRegion(r.Region)

	return nil
}

// DeployServiceIAMConfigureClient describes the required methods to create the IAM Roles/Policies required for the DeployService action.
type DeployServiceIAMConfigureClient interface {
	// GetCallerIdentity returns information about the caller identity.
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)

	// CreatePolicy creates a new IAM Policy.
	CreatePolicy(ctx context.Context, params *iam.CreatePolicyInput, optFns ...func(*iam.Options)) (*iam.CreatePolicyOutput, error)

	// CreateRole creates a new IAM Role.
	CreateRole(ctx context.Context, params *iam.CreateRoleInput, optFns ...func(*iam.Options)) (*iam.CreateRoleOutput, error)

	// PutRolePolicy creates or replaces a Policy by its name in a IAM Role.
	PutRolePolicy(ctx context.Context, params *iam.PutRolePolicyInput, optFns ...func(*iam.Options)) (*iam.PutRolePolicyOutput, error)
}

type defaultDeployServiceIAMConfigureClient struct {
	*iam.Client
	stsClient *sts.Client
}

// NewDeployServiceIAMConfigureClient creates a new DeployServiceIAMConfigureClient.
func NewDeployServiceIAMConfigureClient(ctx context.Context, region string) (DeployServiceIAMConfigureClient, error) {
	if region == "" {
		return nil, trace.BadParameter("region is required")
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return &defaultDeployServiceIAMConfigureClient{
		Client:    iam.NewFromConfig(cfg),
		stsClient: sts.NewFromConfig(cfg),
	}, nil
}

// GetCallerIdentity returns details about the IAM user or role whose credentials are used to call the operation.
func (d defaultDeployServiceIAMConfigureClient) GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return d.stsClient.GetCallerIdentity(ctx, params, optFns...)
}

// ConfigureDeployServiceIAM set ups the roles required for calling the DeployService action.
// It creates the following:
//
// A) Role to be used by the deployed service, also known as _TaskRole_.
// The Role is able to manage policies and create logs.
// To ensure there's no priv escalation, we also set up a boundary policy.
// The boundary policy only allows the above permissions and the `rds-db:connect`.
//
// B) Create a Policy in the Integration Role - the role used when setting up the integration.
// This policy allows for the required API Calls to set up the Amazon ECS TaskDefinition, Cluster and Service.
// It also allows to 'iam:PassRole' only for the _TaskRole_.
//
// The following actions must be allowed by the IAM Role assigned in the Client.
// - iam:CreatePolicy
// - iam:CreateRole
// - iam:PutRolePolicy
// - iam:TagPolicy
// - iam:TagRole
func ConfigureDeployServiceIAM(ctx context.Context, clt DeployServiceIAMConfigureClient, req DeployServiceIAMConfigureRequest) error {
	if err := req.CheckAndSetDefaults(); err != nil {
		return trace.Wrap(err)
	}

	if req.AccountID == "" {
		callerIdentity, err := clt.GetCallerIdentity(ctx, nil)
		if err != nil {
			return trace.Wrap(err)
		}
		req.AccountID = *callerIdentity.Account
	}

	if err := createBoundaryPolicyForTaskRole(ctx, clt, req); err != nil {
		return trace.Wrap(err)
	}

	if err := createTaskRole(ctx, clt, req); err != nil {
		return trace.Wrap(err)
	}

	if err := addPolicyToTaskRole(ctx, clt, req); err != nil {
		return trace.Wrap(err)
	}

	if err := addPolicyToIntegrationRole(ctx, clt, req); err != nil {
		return trace.Wrap(err)
	}

	return nil
}

// createBoundaryPolicyForTaskRole creates a Policy to be used as TaskRole's Role Boundary.
// It allows for the TaskRole to:
// - connect to any RDS DB
// - Get, Put and Delete Role Policies to manage the Policy Statements when adding other rds-db:connect entries
// - write application logs to CloudWatch
func createBoundaryPolicyForTaskRole(ctx context.Context, clt DeployServiceIAMConfigureClient, req DeployServiceIAMConfigureRequest) error {
	taskRoleBoundaryPolicyDocument, err := awslib.NewPolicyDocument(
		policyStatementAllowRDSDBConnect(),
		policyStatementGetPutDeleteRolePolicy(req.partitionID, req.AccountID, req.TaskRole),
		policyStatementAllowLogs(),
	).Marshal()
	if err != nil {
		return trace.Wrap(err)
	}

	_, err = clt.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     &req.TaskRoleBoundaryPolicyName,
		PolicyDocument: &taskRoleBoundaryPolicyDocument,
		Tags:           req.ResourceCreationTags.ForIAM(),
	})
	if err != nil {
		if trace.IsAlreadyExists(awslib.ConvertIAMv2Error(err)) {
			log.Printf("TaskRole: Boundary Policy %q already exists.\n", req.TaskRoleBoundaryPolicyName)
			return nil
		}
		return trace.Wrap(err)
	}

	log.Printf("TaskRole: Boundary Policy %q created.\n", req.TaskRoleBoundaryPolicyName)
	return nil
}

// createTaskRolecreates the TaskRole and sets up the Role Boundary and its Trust Relationship.
func createTaskRole(ctx context.Context, clt DeployServiceIAMConfigureClient, req DeployServiceIAMConfigureRequest) error {
	taskRoleAssumeRoleDocument, err := awslib.NewPolicyDocument(
		policyStatementAssumeRoleECSTasks(),
	).Marshal()
	if err != nil {
		return trace.Wrap(err)
	}

	boundaryRoleARN := awslib.PolicyARN(req.partitionID, req.AccountID, req.TaskRoleBoundaryPolicyName)

	_, err = clt.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 &req.TaskRole,
		AssumeRolePolicyDocument: &taskRoleAssumeRoleDocument,
		PermissionsBoundary:      &boundaryRoleARN,
		Tags:                     req.ResourceCreationTags.ForIAM(),
	})
	if err != nil {
		if trace.IsAlreadyExists(awslib.ConvertIAMv2Error(err)) {
			log.Printf("TaskRole: Role %q already exists.\n", req.TaskRole)
			return nil
		}
		return trace.Wrap(err)
	}

	log.Printf("TaskRole: Role %q created with Boundary %q.\n", req.TaskRole, boundaryRoleARN)
	return nil
}

// addPolicyToTaskRole updates the TaskRole to allow the service to:
// - manage Policies of the TaskRole
// - write logs to CloudWatch
func addPolicyToTaskRole(ctx context.Context, clt DeployServiceIAMConfigureClient, req DeployServiceIAMConfigureRequest) error {
	taskRolePolicyDocument, err := awslib.NewPolicyDocument(
		policyStatementGetPutDeleteRolePolicy(req.partitionID, req.AccountID, req.TaskRole),
		policyStatementAllowLogs(),
	).Marshal()
	if err != nil {
		return trace.Wrap(err)
	}

	_, err = clt.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		PolicyName:     &req.TaskRole,
		RoleName:       &req.TaskRole,
		PolicyDocument: &taskRolePolicyDocument,
	})
	if err != nil {
		return trace.Wrap(err)
	}

	log.Printf("TaskRole: IAM Policy %q added to Role %q.\n", req.TaskRole, req.TaskRole)
	return nil
}

// addPolicyToIntegrationRole creates or updates the DeployService Policy in IntegrationRole.
// It allows the Proxy to call ECS APIs and to pass the TaskRole when deploying a service.
func addPolicyToIntegrationRole(ctx context.Context, clt DeployServiceIAMConfigureClient, req DeployServiceIAMConfigureRequest) error {
	taskRoleARN := awslib.RoleARN(req.partitionID, req.AccountID, req.TaskRole)
	taskRolePolicyDocument, err := awslib.NewPolicyDocument(
		policyStatementAllowPassRole(taskRoleARN),
		policyStatementECSManagement(),
	).Marshal()
	if err != nil {
		return trace.Wrap(err)
	}

	_, err = clt.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		PolicyName:     &req.IntegrationRoleDeployServicePolicy,
		RoleName:       &req.IntegrationRole,
		PolicyDocument: &taskRolePolicyDocument,
	})
	if err != nil {
		return trace.Wrap(err)
	}

	log.Printf("IntegrationRole: IAM Policy %q added to Role %q\n", req.IntegrationRoleDeployServicePolicy, req.IntegrationRole)
	return nil
}

// policyStatementGetPutDeleteRolePolicy contains the required permissions for the Database Service
// to create additional permissions on the TaskRole.
// This is protected by the boundary policy so that other Actions or other Roles aren't possible for
// the deployed service.
func policyStatementGetPutDeleteRolePolicy(partitionID, accountID, taskRole string) *awslib.Statement {
	return &awslib.Statement{
		Effect:  awslib.EffectAllow,
		Actions: awslib.SliceOrString{"iam:GetRolePolicy", "iam:PutRolePolicy", "iam:DeleteRolePolicy"},
		Resources: awslib.SliceOrString{
			awslib.PolicyARN(partitionID, accountID, taskRole),
		},
	}
}

// policyStatementAllowRDSDBConnect allows for the service to connect to any RDS DB.
func policyStatementAllowRDSDBConnect() *awslib.Statement {
	return &awslib.Statement{
		Effect:  awslib.EffectAllow,
		Actions: awslib.SliceOrString{"rds-db:connect"},
		Resources: awslib.SliceOrString{
			types.Wildcard,
		},
	}
}

// policyStatementAssumeRoleECSTasks returns the Trust Relationship for the TaskRole.
// It allows the usage of this Role by the ECS Tasks service.
func policyStatementAssumeRoleECSTasks() *awslib.Statement {
	return &awslib.Statement{
		Effect:  awslib.EffectAllow,
		Actions: awslib.SliceOrString{"sts:AssumeRole"},
		Principals: map[string]awslib.SliceOrString{
			"Service": {"ecs-tasks.amazonaws.com"},
		},
	}
}

// policyStatementAllowLogs returns the statement required for the Amazon ECS Service to export logs
// to CloudWatch.
// https://docs.aws.amazon.com/AmazonECS/latest/developerguide/using_awslogs.html
func policyStatementAllowLogs() *awslib.Statement {
	return &awslib.Statement{
		Effect:  awslib.EffectAllow,
		Actions: awslib.SliceOrString{"logs:CreateLogStream", "logs:PutLogEvents", "logs:CreateLogGroup"},
		Resources: awslib.SliceOrString{
			types.Wildcard,
		},
	}
}

// policyStatementAllowPassRole creates a policy statement that allows the Integration Role
// to pass a Role (TaskRole) to the ECS Service.
// https://docs.aws.amazon.com/AmazonECS/latest/userguide/task-iam-roles.html#specify-task-iam-roles
func policyStatementAllowPassRole(taskRole string) *awslib.Statement {
	return &awslib.Statement{
		Effect:  awslib.EffectAllow,
		Actions: awslib.SliceOrString{"iam:PassRole"},
		Resources: awslib.SliceOrString{
			taskRole,
		},
	}
}

// policyStatementECSManagement is the required policy statement to allow the Integration Role
// to deploy the service using Amazon ECS.
func policyStatementECSManagement() *awslib.Statement {
	return &awslib.Statement{
		Effect: awslib.EffectAllow,
		Actions: awslib.SliceOrString{
			"ecs:DescribeClusters", "ecs:CreateCluster", "ecs:PutClusterCapacityProviders",
			"ecs:DescribeServices", "ecs:CreateService", "ecs:UpdateService",
			"ecs:RegisterTaskDefinition",
		},
		Resources: awslib.SliceOrString{
			types.Wildcard,
		},
	}
}
