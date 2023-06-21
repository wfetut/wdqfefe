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
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/api/utils/sshutils"
	"github.com/gravitational/teleport/lib/auth/native"
	"github.com/gravitational/trace"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
	"golang.org/x/exp/slices"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
)

func convertGoogleError(err error) error {
	var googleError *googleapi.Error
	if errors.As(err, &googleError) {
		return trace.ReadError(googleError.Code, []byte(googleError.Message))
	}
	return err
}

// InstanceClient is a client to interact with GCP VMs.
type InstancesClient interface {
	// ListInstances lists the GCP VMs that belong to the given project and
	// location.
	// location supports wildcard "*".
	ListInstances(ctx context.Context, projectID, location string) ([]*Instance, error)
	// GetInstance gets a GCP VM.
	GetInstance(ctx context.Context, req *InstanceRequest) (*Instance, error)
	// AddSSHKey adds an SSH key to a GCP VM's metadata.
	AddSSHKey(ctx context.Context, req *SSHKeyRequest) error
	// RemoveSSHKey removes an SSH key from a GCP VM's metadata.
	RemoveSSHKey(ctx context.Context, req *SSHKeyRequest) error
}

// InstancesClientConfig is the client configuration for InstancesClient.
type InstancesClientConfig struct {
	// InstanceClient is the underlying GCP client for the instances service.
	InstanceClient *compute.InstancesClient
}

// CheckAndSetsDefaults checks and sets defaults for InstancesClientConfig.
func (c *InstancesClientConfig) CheckAndSetDefaults(ctx context.Context) (err error) {
	if c.InstanceClient == nil {
		if c.InstanceClient, err = compute.NewInstancesRESTClient(ctx); err != nil {
			return trace.Wrap(err)
		}
	}
	return nil
}

// Instance represents a GCP VM.
type Instance struct {
	// Name is the instance's name.
	Name string
	// Zone is the instance's zone.
	Zone string
	// ProjectID is the ID of the project the VM is in.
	ProjectID string
	// ServiceAccount is the email address of the VM's service account, if any.
	ServiceAccount string
	// Labels is the instance's labels.
	Labels    map[string]string
	ipAddress string
	hostKeys  []ssh.PublicKey
	metadata  *computepb.Metadata
}

func (i *Instance) InstanceRequest() InstanceRequest {
	return InstanceRequest{
		ProjectID: i.ProjectID,
		Zone:      i.Zone,
		Name:      i.Name,
	}
}

// NewInstanccesClient creates a new InstancesClient.
func NewInstancesClient(ctx context.Context) (InstancesClient, error) {
	var cfg InstancesClientConfig
	client, err := NewInstancesClientWithConfig(ctx, cfg)
	return client, trace.Wrap(err)
}

// NewInstanccesClientWithConfig creates a new InstancesClient with custom
// config.
func NewInstancesClientWithConfig(ctx context.Context, cfg InstancesClientConfig) (InstancesClient, error) {
	if err := cfg.CheckAndSetDefaults(ctx); err != nil {
		return nil, trace.Wrap(err)
	}
	return &instancesClient{InstancesClientConfig: cfg}, nil
}

// instancesClient implements the InstancesClient interface by wrapping
// compute.InstancesClient.
type instancesClient struct {
	InstancesClientConfig
}

func isExternalNAT(s string) bool {
	return slices.Contains([]string{
		"external-nat",
		"External NAT",
	}, s)
}

func toInstance(origInstance *computepb.Instance) *Instance {
	zoneParts := strings.Split(origInstance.GetZone(), "/")
	zone := zoneParts[len(zoneParts)-1]
	var ipAddress string
outer:
	for _, netInterface := range origInstance.GetNetworkInterfaces() {
		for _, accessConfig := range netInterface.AccessConfigs {
			if isExternalNAT(accessConfig.GetName()) {
				ipAddress = accessConfig.GetNatIP()
				break outer
			}
		}
	}
	inst := &Instance{
		Name:      origInstance.GetName(),
		Zone:      zone,
		Labels:    origInstance.GetLabels(),
		ipAddress: ipAddress,
		metadata:  origInstance.GetMetadata(),
	}
	// GCP VMs can have at most one service account.
	if len(origInstance.ServiceAccounts) > 0 {
		inst.ServiceAccount = origInstance.ServiceAccounts[0].GetEmail()
	}
	return inst
}

// ListInstances lists the GCP VMs that belong to the given project and
// zone.
// zone supports wildcard "*".
func (clt *instancesClient) ListInstances(ctx context.Context, projectID, zone string) ([]*Instance, error) {
	if len(projectID) == 0 {
		return nil, trace.BadParameter("projectID must be set")
	}
	if len(zone) == 0 {
		return nil, trace.BadParameter("location must be set")
	}

	var instances []*Instance
	var getInstances func() ([]*computepb.Instance, error)

	if zone == types.Wildcard {
		it := clt.InstanceClient.AggregatedList(ctx, &computepb.AggregatedListInstancesRequest{
			Project: projectID,
		})
		getInstances = func() ([]*computepb.Instance, error) {
			resp, err := it.Next()
			if resp.Value == nil {
				return nil, trace.Wrap(err)
			}
			return resp.Value.GetInstances(), trace.Wrap(err)
		}
	} else {
		it := clt.InstanceClient.List(ctx, &computepb.ListInstancesRequest{
			Project: projectID,
			Zone:    zone,
		},
		)
		getInstances = func() ([]*computepb.Instance, error) {
			resp, err := it.Next()
			if resp == nil {
				return nil, trace.Wrap(err)
			}
			return []*computepb.Instance{resp}, trace.Wrap(err)
		}
	}

	for {
		resp, err := getInstances()
		if errors.Is(err, iterator.Done) {
			return instances, nil
		}
		if err != nil {
			return nil, trace.Wrap(err)
		}
		for _, rawInst := range resp {
			inst := toInstance(rawInst)
			inst.ProjectID = projectID
			instances = append(instances, inst)
		}
	}
}

// InstanceRequest contains parameters for making a request to a specific instance.
type InstanceRequest struct {
	// ProjectID is the ID of the VM's project.
	ProjectID string
	// Zone is the instance's zone.
	Zone string
	// Name is the instance's name.
	Name string
}

func (req *InstanceRequest) CheckAndSetDefaults() error {
	if len(req.ProjectID) == 0 {
		trace.BadParameter("projectID must be set")
	}
	if len(req.Zone) == 0 {
		trace.BadParameter("zone must be set")
	}
	if len(req.Name) == 0 {
		trace.BadParameter("name must be set")
	}
	return nil
}

// getHostKeys gets the SSH host keys from the VM, if available.
func (clt *instancesClient) getHostKeys(ctx context.Context, req *InstanceRequest) ([]ssh.PublicKey, error) {
	queryPath := "hostkeys/"
	guestAttributes, err := clt.InstanceClient.GetGuestAttributes(ctx, &computepb.GetGuestAttributesInstanceRequest{
		Instance:  req.Name,
		Project:   req.ProjectID,
		Zone:      req.Zone,
		QueryPath: &queryPath,
	})
	if err != nil {
		return nil, trace.Wrap(convertGoogleError(err))
	}
	items := guestAttributes.GetQueryValue().GetItems()
	keys := make([]ssh.PublicKey, 0, len(items))
	var errors []error
	for _, item := range items {
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(fmt.Sprintf("%v %v", item.GetKey(), item.GetValue())))
		if err == nil {
			keys = append(keys, key)
		} else {
			errors = append(errors, err)
		}
	}
	return keys, trace.NewAggregate(errors...)
}

// GetInstance gets a GCP VM.
func (clt *instancesClient) GetInstance(ctx context.Context, req *InstanceRequest) (*Instance, error) {
	if err := req.CheckAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}
	resp, err := clt.InstanceClient.Get(ctx, &computepb.GetInstanceRequest{
		Instance: req.Name,
		Project:  req.ProjectID,
		Zone:     req.Zone,
	})
	if err != nil {
		return nil, trace.Wrap(convertGoogleError(err))
	}
	inst := toInstance(resp)
	inst.ProjectID = req.ProjectID

	hostKeys, err := clt.getHostKeys(ctx, req)
	if err == nil {
		inst.hostKeys = hostKeys
	} else if !trace.IsNotFound(err) {
		return nil, trace.Wrap(err)
	}
	return inst, nil
}

func formatSSHKey(user string, pubKey ssh.PublicKey, expires time.Time) string {
	const iso8601Format = "2006-01-02T15:04:05-0700"
	return fmt.Sprintf(`%s:%s %s google-ssh {"userName":%q,"expireOn":%q}`,
		user,
		pubKey.Type(),
		base64.StdEncoding.EncodeToString(bytes.TrimSpace(pubKey.Marshal())),
		user,
		expires.Format(iso8601Format),
	)
}

func addSSHKey(meta *computepb.Metadata, user string, pubKey ssh.PublicKey, expires time.Time) {
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

// SSHKeyRequest contains parameters to add/removed SSH keys from an instance.
type SSHKeyRequest struct {
	// Instance is the instance to add/remove keys form.
	Instance *Instance
	// User is the user associated with the key.
	User string
	// PublicKey is the key to add. Ignored when removing a key.
	PublicKey ssh.PublicKey
	// Expires is the expiration time of the key. Ignored when removing a key.
	Expires time.Time
}

func (req *SSHKeyRequest) CheckAndSetDefaults() error {
	if req.Instance == nil {
		return trace.BadParameter("instance not set")
	}
	if req.User == "" {
		req.User = "teleport"
	}
	if req.Expires.IsZero() {
		req.Expires = time.Now().Add(10 * time.Minute)
	}
	return nil
}

// AddSSHKey adds an SSH key to a GCP VM's metadata.
func (clt *instancesClient) AddSSHKey(ctx context.Context, req *SSHKeyRequest) error {
	if err := req.CheckAndSetDefaults(); err != nil {
		return trace.Wrap(err)
	}
	fmt.Printf("AddSSHKey: %+v %s\n", req, req.PublicKey.Marshal())
	if req.PublicKey == nil {
		return trace.BadParameter("public key not set")
	}
	addSSHKey(req.Instance.metadata, req.User, req.PublicKey, req.Expires)
	op, err := clt.InstanceClient.SetMetadata(ctx, &computepb.SetMetadataInstanceRequest{
		Instance:         req.Instance.Name,
		MetadataResource: req.Instance.metadata,
		Project:          req.Instance.ProjectID,
		Zone:             req.Instance.Zone,
	})
	if err != nil {
		return trace.Wrap(err)
	}
	if err := op.Wait(ctx); err != nil {
		return trace.Wrap(err)
	}
	return nil
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

// RemoveSSHKey removes an SSH key from a GCP VM's metadata.
func (clt *instancesClient) RemoveSSHKey(ctx context.Context, req *SSHKeyRequest) error {
	if err := req.CheckAndSetDefaults(); err != nil {
		return trace.Wrap(err)
	}
	removeSSHKey(req.Instance.metadata, req.User)
	op, err := clt.InstanceClient.SetMetadata(ctx, &computepb.SetMetadataInstanceRequest{
		Instance:         req.Instance.Name,
		MetadataResource: req.Instance.metadata,
		Project:          req.Instance.ProjectID,
		Zone:             req.Instance.Zone,
	})
	if err != nil {
		return trace.Wrap(err)
	}
	if err := op.Wait(ctx); err != nil {
		return trace.Wrap(err)
	}
	return nil
}

// RunCommandRequest contains parameters for running a command on an instance.
type RunCommandRequest struct {
	// Client is the instance client to use.
	Client InstancesClient
	// InstanceRequest is the set of parameters identifying the instance.
	InstanceRequest
	// Script is the script to execute.
	Script string
	dialer func(ctx context.Context, network, addr string) (net.Conn, error)
}

func (req *RunCommandRequest) CheckAndSetDefaults() error {
	if err := req.InstanceRequest.CheckAndSetDefaults(); err != nil {
		return trace.Wrap(err)
	}
	if len(req.Script) == 0 {
		return trace.BadParameter("script must be set")
	}
	if req.dialer == nil {
		dialer := net.Dialer{}
		req.dialer = dialer.DialContext
	}
	return nil
}

func generateKeyPair() (ssh.Signer, ssh.PublicKey, error) {
	rawPriv, rawPub, err := native.GenerateKeyPair()
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	signer, err := ssh.ParsePrivateKey(rawPriv)
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	publicKey, _, _, _, err := ssh.ParseAuthorizedKey(rawPub)
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	return signer, publicKey, nil
}

// RunCommand runs a command on an instance.
func RunCommand(ctx context.Context, req *RunCommandRequest) error {
	if err := req.CheckAndSetDefaults(); err != nil {
		return trace.Wrap(err)
	}
	fmt.Printf("RunCommand: %+v\n", req)

	// Generate keys and add them to the instance.
	signer, publicKey, err := generateKeyPair()
	if err != nil {
		return trace.Wrap(err)
	}
	instance, err := req.Client.GetInstance(ctx, &req.InstanceRequest)
	fmt.Printf("Instance: %+v\n", instance)
	if err != nil {
		return trace.Wrap(err)
	}
	if len(instance.hostKeys) == 0 {
		// TODO: link to docs for fix.
		return trace.NotFound("Instances %v is missing host keys.", req.Name)
	}
	if len(instance.ipAddress) == 0 {
		return trace.NotFound("Instance %v has no ip address.", req.Name)
	}
	user := "teleport"
	keyReq := &SSHKeyRequest{
		Instance:  instance,
		PublicKey: publicKey,
		User:      user,
	}
	if err := req.Client.AddSSHKey(ctx, keyReq); err != nil {
		return trace.Wrap(err)
	}

	// Clean up the key when we're done.
	defer func() {
		var err error
		if keyReq.Instance, err = req.Client.GetInstance(ctx, &req.InstanceRequest); err != nil {
			logrus.WithError(err).Warn("Error fetching instance.")
		}
		if err := req.Client.RemoveSSHKey(ctx, keyReq); err != nil {
			logrus.WithError(err).Warn("Error deleting SSH Key.")
		}
	}()

	// Configure the SSH client.
	callback, err := sshutils.HostKeyCallback(instance.hostKeys, true)
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
	addr := net.JoinHostPort(instance.ipAddress, "22")
	conn, err := req.dialer(ctx, "tcp", addr)
	if err != nil {
		return trace.Wrap(err)
	}

	clientConn, newCh, requestsCh, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		return trace.Wrap(err)
	}
	sshClient := ssh.NewClient(clientConn, newCh, requestsCh)
	defer sshClient.Close()
	session, err := sshClient.NewSession()
	if err != nil {
		return trace.Wrap(err)
	}
	defer session.Close()

	// Execute the command.
	var b bytes.Buffer
	session.Stdout = &b
	fmt.Printf("running command: %q\n", req.Script)
	if err := session.Run(req.Script); err != nil {
		if errors.Is(err, &ssh.ExitError{}) {
			logrus.WithError(err).Debugf("Command exited with error.")
			logrus.Debugf(b.String())
		}
		fmt.Println("command finished with error")
		return trace.Wrap(err)
	}
	fmt.Println("command finished")
	fmt.Println(b.String())

	return nil
}
