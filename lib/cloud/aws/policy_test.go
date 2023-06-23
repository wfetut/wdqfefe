/*
Copyright 2021 Gravitational, Inc.

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

package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/arn"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
)

func TestSliceOrString(t *testing.T) {
	t.Run("marshal", func(t *testing.T) {
		t.Run("nil slice", func(t *testing.T) {
			var empty SliceOrString
			bytes, err := json.Marshal(empty)
			require.NoError(t, err)
			require.Equal(t, "[]", string(bytes))
		})

		t.Run("single string", func(t *testing.T) {
			single := SliceOrString{"single"}
			bytes, err := json.Marshal(single)
			require.NoError(t, err)
			require.Equal(t, "\"single\"", string(bytes))
		})

		t.Run("slice", func(t *testing.T) {
			slice := SliceOrString{"e1", "e2"}
			bytes, err := json.Marshal(slice)
			require.NoError(t, err)
			require.Equal(t, "[\"e1\",\"e2\"]", string(bytes))
		})
	})

	t.Run("unmarshal", func(t *testing.T) {
		t.Run("single string", func(t *testing.T) {
			var single SliceOrString
			err := json.Unmarshal([]byte(`"single"`), &single)
			require.NoError(t, err)
			require.Equal(t, SliceOrString{"single"}, single)
		})

		t.Run("slice", func(t *testing.T) {
			var slice SliceOrString
			err := json.Unmarshal([]byte(`["e1", "e2"]`), &slice)
			require.NoError(t, err)
			require.Equal(t, SliceOrString{"e1", "e2"}, slice)
		})

		t.Run("error int", func(t *testing.T) {
			var slice SliceOrString
			err := json.Unmarshal([]byte(`5`), &slice)
			require.Error(t, err)
		})

		t.Run("error invalid json", func(t *testing.T) {
			var slice SliceOrString
			err := json.Unmarshal([]byte(`"e1,`), &slice)
			require.Error(t, err)
		})
	})
}

func TestParsePolicyDocument(t *testing.T) {
	t.Run("parse without principals", func(t *testing.T) {
		policyDoc, err := ParsePolicyDocument(`{
			"Version": "2012-10-17",
			"Statement": [
			  {
				"Effect": "Allow",
				"Action": "rds-db:connect",
				"Resource": ["arn:aws:rds-db:us-west-1:12345:dbuser:id/*"]
			  }
			]
		  }`)
		require.NoError(t, err)
		require.Equal(t, PolicyDocument{
			Version: PolicyVersion,
			Statements: []*Statement{{
				Effect:    EffectAllow,
				Actions:   SliceOrString{"rds-db:connect"},
				Resources: SliceOrString{"arn:aws:rds-db:us-west-1:12345:dbuser:id/*"},
			}},
		}, *policyDoc)
	})
	t.Run("parse without resource", func(t *testing.T) {
		policyDoc, err := ParsePolicyDocument(`{
			"Version": "2012-10-17",
			"Statement": [
			  {
				"Effect": "Allow",
				"Action": "rds-db:connect",
				"Principal": {
					"Service": "ecs-tasks.amazonaws.com"
				}
			  }
			]
		  }`)
		require.NoError(t, err)
		require.Equal(t, PolicyDocument{
			Version: PolicyVersion,
			Statements: []*Statement{{
				Effect:  EffectAllow,
				Actions: SliceOrString{"rds-db:connect"},
				Principals: map[string]SliceOrString{
					"Service": {"ecs-tasks.amazonaws.com"},
				},
			}},
		}, *policyDoc)
	})
}

func TestMarshalPolicyDocument(t *testing.T) {
	t.Run("marshal without principal", func(t *testing.T) {
		doc := PolicyDocument{
			Version: PolicyVersion,
			Statements: []*Statement{{
				Effect:    EffectAllow,
				Actions:   SliceOrString{"rds-db:connect"},
				Resources: SliceOrString{"arn:aws:rds-db:us-west-1:12345:dbuser:id/*"},
			}},
		}

		docString, err := doc.Marshal()
		require.NoError(t, err)

		require.Equal(t, `{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Action": "rds-db:connect",
            "Resource": "arn:aws:rds-db:us-west-1:12345:dbuser:id/*"
        }
    ]
}`, docString)
	})

	t.Run("marshal without resources", func(t *testing.T) {
		doc := PolicyDocument{
			Version: PolicyVersion,
			Statements: []*Statement{{
				Effect:  EffectAllow,
				Actions: SliceOrString{"rds-db:connect"},
				Principals: map[string]SliceOrString{
					"Service": {"ecs-tasks.amazonaws.com"},
				},
			}},
		}

		docString, err := doc.Marshal()
		require.NoError(t, err)

		require.Equal(t, `{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Action": "rds-db:connect",
            "Principal": {
                "Service": "ecs-tasks.amazonaws.com"
            }
        }
    ]
}`, docString)
	})
}

// TestIAMPolicy verifies AWS IAM policy manipulations.
func TestIAMPolicy(t *testing.T) {
	policy := NewPolicyDocument()

	// Add a new action/resource.
	alreadyExisted := policy.Ensure(EffectAllow, "action-1", "resource-1")
	require.False(t, alreadyExisted)
	require.Equal(t, &PolicyDocument{
		Version: PolicyVersion,
		Statements: []*Statement{
			{
				Effect:    EffectAllow,
				Actions:   []string{"action-1"},
				Resources: []string{"resource-1"},
			},
		},
	}, policy)

	// Add the same action/resource.
	alreadyExisted = policy.Ensure(EffectAllow, "action-1", "resource-1")
	require.True(t, alreadyExisted)
	require.Equal(t, &PolicyDocument{
		Version: PolicyVersion,
		Statements: []*Statement{
			{
				Effect:    EffectAllow,
				Actions:   []string{"action-1"},
				Resources: []string{"resource-1"},
			},
		},
	}, policy)

	// Add a new resource to existing action.
	alreadyExisted = policy.Ensure(EffectAllow, "action-1", "resource-2")
	require.False(t, alreadyExisted)
	require.Equal(t, &PolicyDocument{
		Version: PolicyVersion,
		Statements: []*Statement{
			{
				Effect:    EffectAllow,
				Actions:   []string{"action-1"},
				Resources: []string{"resource-1", "resource-2"},
			},
		},
	}, policy)

	// Add another action/resource.
	alreadyExisted = policy.Ensure(EffectAllow, "action-2", "resource-3")
	require.False(t, alreadyExisted)
	require.Equal(t, &PolicyDocument{
		Version: PolicyVersion,
		Statements: []*Statement{
			{
				Effect:    EffectAllow,
				Actions:   []string{"action-1"},
				Resources: []string{"resource-1", "resource-2"},
			},
			{
				Effect:    EffectAllow,
				Actions:   []string{"action-2"},
				Resources: []string{"resource-3"},
			},
		},
	}, policy)

	// Delete existing resource action.
	policy.Delete(EffectAllow, "action-1", "resource-1")
	require.Equal(t, &PolicyDocument{
		Version: PolicyVersion,
		Statements: []*Statement{
			{
				Effect:    EffectAllow,
				Actions:   []string{"action-1"},
				Resources: []string{"resource-2"},
			},
			{
				Effect:    EffectAllow,
				Actions:   []string{"action-2"},
				Resources: []string{"resource-3"},
			},
		},
	}, policy)

	// Delete last resource from first action, statement should get removed as well.
	policy.Delete(EffectAllow, "action-1", "resource-2")
	require.Equal(t, &PolicyDocument{
		Version: PolicyVersion,
		Statements: []*Statement{
			{
				Effect:    EffectAllow,
				Actions:   []string{"action-2"},
				Resources: []string{"resource-3"},
			},
		},
	}, policy)

	// Delete last resource action, policy should be empty.
	policy.Delete(EffectAllow, "action-2", "resource-3")
	require.Equal(t, &PolicyDocument{
		Version: PolicyVersion,
	}, policy)

	// Policy with duplicate statement.
	policy = &PolicyDocument{
		Version: PolicyVersion,
		Statements: []*Statement{
			{
				Effect:    EffectAllow,
				Actions:   []string{"action-1"},
				Resources: []string{"resource-1"},
			},
			{
				Effect:    EffectAllow,
				Actions:   []string{"action-1"},
				Resources: []string{"resource-1"},
			},
		},
	}
	policy.Delete(EffectAllow, "action-1", "resource-1")
	require.Equal(t, &PolicyDocument{
		Version: PolicyVersion,
	}, policy)

	// Policy with deny statement.
	policy = &PolicyDocument{
		Version: PolicyVersion,
		Statements: []*Statement{
			{
				Effect:    EffectAllow,
				Actions:   []string{"action-1"},
				Resources: []string{"resource-1", "resource-2"},
			},
			{
				Effect:    EffectDeny,
				Actions:   []string{"action-1"},
				Resources: []string{"resource-2"},
			},
		},
	}
	policy.Delete(EffectAllow, "action-1", "resource-2")
	require.Equal(t, &PolicyDocument{
		Version: PolicyVersion,
		Statements: []*Statement{
			{
				Effect:    EffectAllow,
				Actions:   []string{"action-1"},
				Resources: []string{"resource-1"},
			},
			{
				Effect:    EffectDeny,
				Actions:   []string{"action-1"},
				Resources: []string{"resource-2"},
			},
		},
	}, policy)
}

func TestPolicyEnsureStatements(t *testing.T) {
	policy := NewPolicyDocument(
		&Statement{
			Effect:    EffectAllow,
			Actions:   []string{"action-1"},
			Resources: []string{"resource-1"},
		},
		&Statement{
			Effect:    EffectDeny,
			Actions:   []string{"action-1"},
			Resources: []string{"resource-2"},
		},
	)

	policy.EnsureStatements(
		// Existing/new action and existing resource.
		&Statement{
			Effect:    EffectAllow,
			Actions:   []string{"action-1", "action-2"},
			Resources: []string{"resource-1"},
		},
		// Existing action and new resource.
		&Statement{
			Effect:    EffectAllow,
			Actions:   []string{"action-1"},
			Resources: []string{"resource-3"},
		},
		// New actions and new resources.
		&Statement{
			Effect:    EffectAllow,
			Actions:   []string{"action-2", "action-3", "action-4"},
			Resources: []string{"resource-4"},
		},
		// Test nil.
		nil,
		// Existing action and resource.
		&Statement{
			Effect:    EffectDeny,
			Actions:   []string{"action-1"},
			Resources: []string{"resource-2"},
		},
	)
	require.Equal(t, &PolicyDocument{
		Version: PolicyVersion,
		Statements: []*Statement{
			{
				Effect:    EffectAllow,
				Actions:   []string{"action-1"},
				Resources: []string{"resource-1", "resource-3"},
			},
			{
				Effect:    EffectDeny,
				Actions:   []string{"action-1"},
				Resources: []string{"resource-2"},
			},
			{
				Effect:    EffectAllow,
				Actions:   []string{"action-2"},
				Resources: []string{"resource-1", "resource-4"},
			},
			{
				Effect:    EffectAllow,
				Actions:   []string{"action-3", "action-4"},
				Resources: []string{"resource-4"},
			},
		},
	}, policy)
}

func TestRetrievePolicy(t *testing.T) {
	ctx := context.Background()

	tests := map[string]struct {
		tags        map[string]string
		iamMock     *iamMock
		returnError bool
	}{
		"PolicyFound": {
			iamMock: &iamMock{
				policy:         &iam.Policy{},
				policyVersions: []*iam.PolicyVersion{{VersionId: aws.String("v1")}},
			},
		},
		"PolicyMatchLabels": {
			tags: map[string]string{"env": "prod"},
			iamMock: &iamMock{
				policy:         &iam.Policy{Tags: []*iam.Tag{{Key: aws.String("env"), Value: aws.String("prod")}}},
				policyVersions: []*iam.PolicyVersion{{VersionId: aws.String("v1")}},
			},
		},
		"PolicyNotMatchingLabels": {
			tags:        map[string]string{"env": "prod"},
			returnError: true,
			iamMock: &iamMock{
				policy:         &iam.Policy{},
				policyVersions: []*iam.PolicyVersion{{VersionId: aws.String("v1")}},
			},
		},
		"PolicyNotFound": {
			iamMock:     &iamMock{},
			returnError: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// Retrieve doesn't use `identity` so we can pass a nil value.
			policies := NewPolicies("", "", test.iamMock)

			policy, versions, err := policies.Retrieve(ctx, "", test.tags)
			if test.returnError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, policy)
			require.Empty(t, cmp.Diff(test.iamMock.policyVersions, versions))
		})
	}
}

func TestUpsertPolicy(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	accountID := "123456789012"
	partitionID := "aws"

	tests := map[string]struct {
		expectedPolicyArn string
		returnError       bool
		iamMock           *iamMock
	}{
		"CreateNewPolicy": {
			expectedPolicyArn: "expected-arn",
			iamMock: &iamMock{
				policyCreated: &iam.Policy{Arn: aws.String("expected-arn")},
			},
		},
		"AddPolicyVersion": {
			expectedPolicyArn: fmt.Sprintf("arn:aws:iam::%s:policy/", accountID),
			iamMock: &iamMock{
				policy: &iam.Policy{Arn: aws.String("expected-arn")},
				policyVersions: []*iam.PolicyVersion{
					{VersionId: aws.String("v1"), IsDefaultVersion: aws.Bool(false), CreateDate: aws.Time(now.Add(time.Second))},
				},
				policyVersionCreated: &iam.PolicyVersion{},
			},
		},
		"DeleteAndAddPolicyVersion": {
			expectedPolicyArn: fmt.Sprintf("arn:aws:iam::%s:policy/", accountID),
			iamMock: &iamMock{
				policy: &iam.Policy{Arn: aws.String("expected-arn")},
				policyVersions: []*iam.PolicyVersion{
					{VersionId: aws.String("v1"), IsDefaultVersion: aws.Bool(false), CreateDate: aws.Time(now.Add(time.Second))},
					{VersionId: aws.String("v2"), IsDefaultVersion: aws.Bool(false), CreateDate: aws.Time(now.Add(2 * time.Second))},
					{VersionId: aws.String("v3"), IsDefaultVersion: aws.Bool(false), CreateDate: aws.Time(now.Add(3 * time.Second))},
					{VersionId: aws.String("v4"), IsDefaultVersion: aws.Bool(false), CreateDate: aws.Time(now.Add(4 * time.Second))},
					{VersionId: aws.String("v5"), IsDefaultVersion: aws.Bool(true), CreateDate: aws.Time(now.Add(5 * time.Second))},
				},
				policyVersionDeleted: true,
				policyVersionCreated: &iam.PolicyVersion{},
			},
		},
		"PolicyCreateError": {
			returnError: true,
			iamMock:     &iamMock{},
		},
		"PolicyVersionCreateError": {
			returnError: true,
			iamMock: &iamMock{
				policy: &iam.Policy{Arn: aws.String("expected-arn")},
				policyVersions: []*iam.PolicyVersion{
					{VersionId: aws.String("v1"), IsDefaultVersion: aws.Bool(false), CreateDate: aws.Time(now.Add(time.Second))},
				},
			},
		},
		"PolicyVersionDeleteError": {
			returnError: true,
			iamMock: &iamMock{
				policy: &iam.Policy{Arn: aws.String("expected-arn")},
				policyVersions: []*iam.PolicyVersion{
					{VersionId: aws.String("v1"), IsDefaultVersion: aws.Bool(false), CreateDate: aws.Time(now.Add(time.Second))},
					{VersionId: aws.String("v2"), IsDefaultVersion: aws.Bool(false), CreateDate: aws.Time(now.Add(2 * time.Second))},
					{VersionId: aws.String("v3"), IsDefaultVersion: aws.Bool(false), CreateDate: aws.Time(now.Add(3 * time.Second))},
					{VersionId: aws.String("v4"), IsDefaultVersion: aws.Bool(false), CreateDate: aws.Time(now.Add(4 * time.Second))},
					{VersionId: aws.String("v5"), IsDefaultVersion: aws.Bool(true), CreateDate: aws.Time(now.Add(5 * time.Second))},
				},
				policyVersionCreated: &iam.PolicyVersion{},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			policies := NewPolicies(partitionID, accountID, test.iamMock)

			arn, err := policies.Upsert(ctx, &Policy{})
			if test.returnError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, test.expectedPolicyArn, arn)
		})
	}
}

func TestAttachPolicy(t *testing.T) {
	ctx := context.Background()

	tests := map[string]struct {
		returnError bool
		identity    Identity
		iamMock     *iamMock
	}{
		"AttachToUser": {
			identity: userIdentity(),
			iamMock: &iamMock{
				attachUserPolicy: true,
			},
		},
		"AttachToRole": {
			identity: roleIdentity(),
			iamMock: &iamMock{
				attachRolePolicy: true,
			},
		},
		"UnsupportedIdentity": {
			returnError: true,
			identity:    unknownIdentity(),
			iamMock: &iamMock{
				// "enable" both attach to ensure the error doesn't come from
				// the IAM client.
				attachUserPolicy: true,
				attachRolePolicy: true,
			},
		},
		"AttachError": {
			returnError: true,
			identity:    userIdentity(),
			iamMock:     &iamMock{},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			policies := NewPolicies("", "", test.iamMock)

			err := policies.Attach(ctx, "", test.identity)
			if test.returnError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestAttachPolicyBoundary(t *testing.T) {
	ctx := context.Background()

	tests := map[string]struct {
		returnError bool
		identity    Identity
		iamMock     *iamMock
	}{
		"AttachToUser": {
			identity: userIdentity(),
			iamMock: &iamMock{
				attachUserBoundary: true,
			},
		},
		"AttachToRole": {
			identity: roleIdentity(),
			iamMock: &iamMock{
				attachRoleBoundary: true,
			},
		},
		"UnsupportedIdentity": {
			returnError: true,
			identity:    unknownIdentity(),
			iamMock: &iamMock{
				// "enable" both attach to ensure the error doesn't come from
				// the IAM client.
				attachUserBoundary: true,
				attachRoleBoundary: true,
			},
		},
		"AttachError": {
			returnError: true,
			identity:    userIdentity(),
			iamMock:     &iamMock{},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			policies := NewPolicies("", "", test.iamMock)

			err := policies.AttachBoundary(ctx, "", test.identity)
			if test.returnError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestResourceARN(t *testing.T) {
	for _, tt := range []struct {
		name         string
		resourceType string
		partition    string
		accountID    string
		resourceName string
		expected     string
	}{
		{
			name:         "role",
			resourceType: "role",
			partition:    "aws",
			accountID:    "123456789012",
			resourceName: "MyRole",
			expected:     "arn:aws:iam::123456789012:role/MyRole",
		},
		{
			name:         "policy",
			resourceType: "policy",
			partition:    "aws",
			accountID:    "123456789012",
			resourceName: "MyPolicy",
			expected:     "arn:aws:iam::123456789012:policy/MyPolicy",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			switch tt.resourceType {
			case "role":
				require.Equal(t, tt.expected, RoleARN(tt.partition, tt.accountID, tt.resourceName))
			case "policy":
				require.Equal(t, tt.expected, PolicyARN(tt.partition, tt.accountID, tt.resourceName))
			}
		})
	}
}

// userIdentity helper function to generate an user `Identity` .
func userIdentity() Identity {
	return &User{
		identityBase: identityBase{
			arn: arn.ARN{AccountID: "1234567", Resource: "user/example-user"},
		},
	}
}

// roleIdentity helper function to generate a role `Identity` .
func roleIdentity() Identity {
	return &Role{
		identityBase: identityBase{
			arn: arn.ARN{AccountID: "1234567", Resource: "role/example-role"},
		},
	}
}

// roleIdentity helper function to generate a role `Identity` .
func unknownIdentity() Identity {
	return &Unknown{}
}

type iamMock struct {
	iamiface.IAMAPI

	policy               *iam.Policy
	policyVersions       []*iam.PolicyVersion
	policyCreated        *iam.Policy
	policyVersionCreated *iam.PolicyVersion
	policyVersionDeleted bool

	attachUserPolicy   bool
	attachRolePolicy   bool
	attachUserBoundary bool
	attachRoleBoundary bool
}

func (m *iamMock) GetPolicyWithContext(context.Context, *iam.GetPolicyInput, ...request.Option) (*iam.GetPolicyOutput, error) {
	if m.policy == nil {
		return nil, awserr.NewRequestFailure(awserr.New(iam.ErrCodeNoSuchEntityException, "not found", nil), 404, "")
	}

	return &iam.GetPolicyOutput{Policy: m.policy}, nil
}

func (m *iamMock) ListPolicyVersionsWithContext(context.Context, *iam.ListPolicyVersionsInput, ...request.Option) (*iam.ListPolicyVersionsOutput, error) {
	if len(m.policyVersions) == 0 {
		return nil, awserr.NewRequestFailure(awserr.New(iam.ErrCodeNoSuchEntityException, "not found", nil), 404, "")
	}

	return &iam.ListPolicyVersionsOutput{Versions: m.policyVersions}, nil
}

func (m *iamMock) CreatePolicyWithContext(context.Context, *iam.CreatePolicyInput, ...request.Option) (*iam.CreatePolicyOutput, error) {
	if m.policyCreated == nil {
		return nil, awserr.NewRequestFailure(awserr.New(iam.ErrCodeServiceNotSupportedException, "not implemented", nil), 501, "")
	}

	return &iam.CreatePolicyOutput{Policy: m.policyCreated}, nil
}

func (m *iamMock) CreatePolicyVersionWithContext(context.Context, *iam.CreatePolicyVersionInput, ...request.Option) (*iam.CreatePolicyVersionOutput, error) {
	if m.policyVersionCreated == nil {
		return nil, awserr.NewRequestFailure(awserr.New(iam.ErrCodeServiceNotSupportedException, "not implemented", nil), 501, "")
	}

	return &iam.CreatePolicyVersionOutput{PolicyVersion: m.policyVersionCreated}, nil
}

func (m *iamMock) DeletePolicyVersionWithContext(context.Context, *iam.DeletePolicyVersionInput, ...request.Option) (*iam.DeletePolicyVersionOutput, error) {
	if !m.policyVersionDeleted {
		return nil, awserr.NewRequestFailure(awserr.New(iam.ErrCodeServiceNotSupportedException, "not implemented", nil), 501, "")
	}

	return &iam.DeletePolicyVersionOutput{}, nil
}

func (m *iamMock) AttachUserPolicyWithContext(context.Context, *iam.AttachUserPolicyInput, ...request.Option) (*iam.AttachUserPolicyOutput, error) {
	if !m.attachUserPolicy {
		return nil, awserr.NewRequestFailure(awserr.New(iam.ErrCodeServiceNotSupportedException, "not implemented", nil), 501, "")
	}

	return &iam.AttachUserPolicyOutput{}, nil
}

func (m *iamMock) AttachRolePolicyWithContext(context.Context, *iam.AttachRolePolicyInput, ...request.Option) (*iam.AttachRolePolicyOutput, error) {
	if !m.attachRolePolicy {
		return nil, awserr.NewRequestFailure(awserr.New(iam.ErrCodeServiceNotSupportedException, "not implemented", nil), 501, "")
	}

	return &iam.AttachRolePolicyOutput{}, nil
}

func (m *iamMock) PutUserPermissionsBoundaryWithContext(context.Context, *iam.PutUserPermissionsBoundaryInput, ...request.Option) (*iam.PutUserPermissionsBoundaryOutput, error) {
	if !m.attachUserBoundary {
		return nil, awserr.NewRequestFailure(awserr.New(iam.ErrCodeServiceNotSupportedException, "not implemented", nil), 501, "")
	}

	return &iam.PutUserPermissionsBoundaryOutput{}, nil
}

func (m *iamMock) PutRolePermissionsBoundaryWithContext(context.Context, *iam.PutRolePermissionsBoundaryInput, ...request.Option) (*iam.PutRolePermissionsBoundaryOutput, error) {
	if !m.attachRoleBoundary {
		return nil, awserr.NewRequestFailure(awserr.New(iam.ErrCodeServiceNotSupportedException, "not implemented", nil), 501, "")
	}

	return &iam.PutRolePermissionsBoundaryOutput{}, nil
}
