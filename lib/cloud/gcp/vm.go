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
package gcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	gax "github.com/googleapis/gax-go/v2"
	"github.com/gravitational/teleport/api/utils/sshutils"
	"github.com/gravitational/teleport/lib/auth/native"
	"github.com/gravitational/trace"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/iterator"
)

const (
	// computeEngineScope is the GCP Compute Engine Scope for OAuth2.
	// https://developers.google.com/identity/protocols/oauth2/scopes#compute
	computeEngineScope = "ttps://www.googleapis.com/auth/compute"
)

type InstancesClient interface {
	ListInstances(ctx context.Context, projectID, location string) ([]Instance, error)

	GetInstance(ctx context.Context, projectID, location, name string) (Instance, error)

	Run(ctx context.Context, req RunCommandRequest) error
}

type InstancesClientConfig struct {
	InstanceClient gcpInstanceClient
	TokenSource    oauth2.TokenSource
}

func (c *InstancesClientConfig) CheckAndSetDefaults(ctx context.Context) (err error) {
	if c.TokenSource == nil {
		if c.TokenSource, err = google.DefaultTokenSource(ctx, computeEngineScope); err != nil {
			return trace.Wrap(err)
		}
	}
	if c.InstanceClient == nil {
		if c.InstanceClient, err = compute.NewInstancesRESTClient(ctx); err != nil {
			return trace.Wrap(err)
		}
	}
	return nil
}

type gcpInstanceClient interface {
	List(ctx context.Context, req *computepb.ListInstancesRequest, opts ...gax.CallOption) *compute.InstanceIterator
	Get(ctx context.Context, req *computepb.GetInstanceRequest, opts ...gax.CallOption) (*computepb.Instance, error)
	GetGuestAttributes(ctx context.Context, req *computepb.GetGuestAttributesInstanceRequest, opts ...gax.CallOption) (*computepb.GuestAttributes, error)
	SetMetadata(ctx context.Context, req *computepb.SetMetadataInstanceRequest, opts ...gax.CallOption) (*compute.Operation, error)
}

var _ gcpInstanceClient = &compute.InstancesClient{}

type Instance struct {
	Name           string
	Location       string
	ServiceAccount string
	Labels         map[string]string
	hostname       string
	metadata       *computepb.Metadata
}

func NewInstancesClient(ctx context.Context) (InstancesClient, error) {
	var cfg InstancesClientConfig
	client, err := NewInstancesClientWithConfig(ctx, cfg)
	return client, trace.Wrap(err)
}

func NewInstancesClientWithConfig(ctx context.Context, cfg InstancesClientConfig) (InstancesClient, error) {
	if err := cfg.CheckAndSetDefaults(ctx); err != nil {
		return nil, trace.Wrap(err)
	}
	return &instancesClient{}, nil
}

type instancesClient struct {
	InstancesClientConfig
}

func toInstance(origInstance *computepb.Instance) Instance {
	inst := Instance{
		Name:     origInstance.GetName(),
		Location: origInstance.GetZone(),
		Labels:   origInstance.GetLabels(),
		hostname: origInstance.GetHostname(),
		metadata: origInstance.GetMetadata(),
	}
	// GCP VMs can have at most one service account.
	if len(origInstance.ServiceAccounts) > 0 {
		inst.ServiceAccount = origInstance.ServiceAccounts[0].GetEmail()
	}
	return inst
}

func (clt *instancesClient) ListInstances(ctx context.Context, projectID, location string) ([]Instance, error) {
	if len(projectID) == 0 {
		return nil, trace.BadParameter("projectID must be set")
	}
	if len(location) == 0 {
		return nil, trace.BadParameter("location must be set")
	}

	it := clt.InstanceClient.List(
		ctx,
		&computepb.ListInstancesRequest{
			Project: projectID,
			Zone:    convertLocationToGCP(location),
		},
	)
	var instances []Instance
	for {
		resp, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return instances, nil
		}
		if err != nil {
			return nil, trace.Wrap(err)
		}
		instances = append(instances, toInstance(resp))
	}
}

func (clt *instancesClient) GetInstance(ctx context.Context, projectID, zone, name string) (Instance, error) {
	if len(projectID) == 0 {
		return Instance{}, trace.BadParameter("projectID must be set")
	}
	if len(zone) == 0 {
		return Instance{}, trace.BadParameter("zone must be set")
	}
	if len(name) == 0 {
		return Instance{}, trace.BadParameter("name must be set")
	}

	resp, err := clt.InstanceClient.Get(ctx, &computepb.GetInstanceRequest{
		Instance: name,
		Project:  projectID,
		Zone:     zone,
	})
	if err != nil {
		return Instance{}, trace.Wrap(err)
	}
	return toInstance(resp), nil
}

func formatSSHKey(user string, pubKey []byte, expires time.Time) string {
	const iso8601Format = "2006-01-02T15:04:05-0700"
	return fmt.Sprintf(`%s:%s google-ssh {"userName":%q,"expireOn":%q}`,
		user, bytes.TrimSpace(pubKey), user, expires.Format(time.RFC3339),
	)
}

func addSSHKey(meta *computepb.Metadata, user string, pubKey []byte, expires time.Time) {
	var sshKeyItem *computepb.Items
	for _, item := range meta.GetItems() {
		if item.GetKey() == "ssh-keys" {
			sshKeyItem = item
			break
		}
	}
	if sshKeyItem == nil {
		k := "ssh-keys"
		sshKeyItem = &computepb.Items{Key: &k}
		meta.Items = append(meta.Items, sshKeyItem)
	}

	existingKeys := strings.Split(sshKeyItem.GetValue(), "\n")
	existingKeys = append(existingKeys, formatSSHKey(user, pubKey, expires))
	newKeys := strings.Join(existingKeys, "\n")
	sshKeyItem.Value = &newKeys
}

func removeSSHKey(meta *computepb.Metadata, user string) {
	for _, item := range meta.GetItems() {
		if item.GetKey() == "ssh-keys" {
			existingKeys := strings.Split(item.GetValue(), "\n")
			newKeys := make([]string, 0, len(existingKeys))
			for _, key := range existingKeys {
				if !strings.HasPrefix(key, user) {
					newKeys = append(newKeys, key)
				}
			}
			newKeysString := strings.Join(newKeys, "\n")
			item.Value = &newKeysString
			return
		}
	}
}

func (clt *instancesClient) getHostKeys(ctx context.Context, projectID, zone, name string) ([]ssh.PublicKey, error) {
	queryPath := "hostkeys/"
	guestAttributes, err := clt.InstanceClient.GetGuestAttributes(ctx, &computepb.GetGuestAttributesInstanceRequest{
		Instance:  name,
		Project:   projectID,
		Zone:      zone,
		QueryPath: &queryPath,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	items := guestAttributes.GetQueryValue().GetItems()
	keys := make([]ssh.PublicKey, 0, len(items))
	var errors []error
	for _, item := range items {
		key, err := ssh.ParsePublicKey([]byte(fmt.Sprintf("%v %v", item.GetKey(), item.GetValue())))
		if err == nil {
			keys = append(keys, key)
		} else {
			errors = append(errors, err)
		}
	}
	return keys, trace.NewAggregate(errors...)
}

type RunCommandRequest struct {
	ProjectID string
	Zone      string
	Name      string
	Script    string
}

func (clt *instancesClient) Run(ctx context.Context, req RunCommandRequest) error {
	// TODO: verify params
	instance, err := clt.GetInstance(ctx, req.ProjectID, req.Zone, req.Name)
	if err != nil {
		return trace.Wrap(err)
	}
	priv, pub, err := native.GenerateKeyPair()
	if err != nil {
		return trace.Wrap(err)
	}
	user := "teleport" // TODO: make sure this is unique
	expires := time.Now().Add(10 * time.Minute)
	addSSHKey(instance.metadata, user, pub, expires)
	op, err := clt.InstanceClient.SetMetadata(ctx, &computepb.SetMetadataInstanceRequest{
		Instance:         req.Name,
		MetadataResource: instance.metadata,
		Project:          req.ProjectID,
		Zone:             req.Zone,
	})
	if err == nil {
		return trace.Wrap(err)
	}
	if err := op.Wait(ctx); err != nil {
		return trace.Wrap(err)
	}

	defer func() {
		removeSSHKey(instance.metadata, user)
		op, err := clt.InstanceClient.SetMetadata(ctx, &computepb.SetMetadataInstanceRequest{
			Instance:         req.Name,
			MetadataResource: instance.metadata,
			Project:          req.ProjectID,
			Zone:             req.Zone,
		})
		if err == nil {
			logrus.WithError(err).Warnf("Error removing SSH key from instance.")
		}
		if err := op.Wait(ctx); err != nil {
			logrus.WithError(err).Warnf("Error removing SSH key from instance.")
		}
	}()

	signer, err := ssh.ParsePrivateKey(priv)
	if err != nil {
		return trace.Wrap(err)
	}
	hostKeys, err := clt.getHostKeys(ctx, req.ProjectID, req.Zone, req.Name)
	if err != nil {
		return trace.Wrap(err)
	}
	callback, err := sshutils.HostKeyCallback(hostKeys, true)
	if err != nil {
		return trace.Wrap(err)
	}
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: callback,
	}

	sshClient, err := ssh.Dial("tcp", net.JoinHostPort(instance.hostname, "22"), config)
	if err != nil {
		return trace.Wrap(err)
	}
	defer sshClient.Close()
	session, err := sshClient.NewSession()
	if err != nil {
		return trace.Wrap(err)
	}
	defer session.Close()

	var b bytes.Buffer
	session.Stdout = &b
	if err := session.Run(req.Script); err != nil {
		if errors.Is(err, &ssh.ExitError{}) {
			logrus.WithError(err).Debugf("Command exited with error.")
			logrus.Debugf(b.String())
		}
		return trace.Wrap(err)
	}

	return nil
}
