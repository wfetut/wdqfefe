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

package types

import "time"

// 	GetNamespace() string
//	GetCommand() string
//	GetName() string
//	GetLabels() map[string]string

func (c *CommandV1) GetNamespace() string {
	return c.Metadata.Namespace
}

func (c *CommandV1) SetNamespace(namespace string) {
	c.Metadata.Namespace = namespace
}

func (c *CommandV1) GetKind() string {
	return c.Kind
}

func (c *CommandV1) GetSubKind() string {
	return c.SubKind
}

func (c *CommandV1) SetSubKind(s string) {
	c.SubKind = s
}

func (c *CommandV1) GetVersion() string {
	return c.Version
}

func (c *CommandV1) GetName() string {
	return c.Metadata.Name
}

func (c *CommandV1) SetName(s string) {
	c.Metadata.Name = s
}

func (c *CommandV1) Expiry() time.Time {
	return c.Metadata.Expiry()
}

func (c *CommandV1) SetExpiry(t time.Time) {
	c.Metadata.SetExpiry(t)
}

func (c *CommandV1) GetMetadata() Metadata {
	return c.Metadata
}

func (c *CommandV1) GetResourceID() int64 {
	return c.Metadata.GetID()
}

func (c *CommandV1) SetResourceID(i int64) {
	c.Metadata.SetID(i)
}

func (c *CommandV1) CheckAndSetDefaults() error {
	//TODO implement me
	return nil
}

func (c *CommandV1) Origin() string {
	return c.Metadata.Origin()
}

func (c *CommandV1) SetOrigin(s string) {
	c.Metadata.SetOrigin(s)
}

func (c *CommandV1) GetLabel(key string) (value string, ok bool) {
	//TODO implement me
	panic("implement me")
}

func (c *CommandV1) GetAllLabels() map[string]string {
	return c.GetLabels()
}

func (c *CommandV1) GetStaticLabels() map[string]string {
	//TODO implement me
	panic("implement me")
}

func (c *CommandV1) SetStaticLabels(sl map[string]string) {
	//TODO implement me
	panic("implement me")
}

func (c *CommandV1) MatchSearch(searchValues []string) bool {
	//TODO implement me
	panic("implement me")
}

func (c *CommandV1) GetCommand() string {
	return c.Spec.Command
}

func (c *CommandV1) GetLabels() map[string]string {
	return c.Metadata.Labels
}
