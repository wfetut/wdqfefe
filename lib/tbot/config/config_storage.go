/*
Copyright 2022 Gravitational, Inc.

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

package config

import (
	"path/filepath"

	"github.com/gravitational/trace"
	"gopkg.in/yaml.v3"

	"github.com/gravitational/teleport/lib/defaults"
)

var defaultStoragePath = filepath.Join(defaults.DataDir, "bot")

// StorageConfig contains config parameters for the bot's internal certificate
// storage.
type StorageConfig struct {
	// Destination's yaml is handled by MarshalYAML/UnmarshalYAML
	Destination destinationWrapper
}

func (sc *StorageConfig) CheckAndSetDefaults() error {
	if sc.Destination.Get() == nil {
		sc.Destination = WrapDestination(
			&DestinationDirectory{
				Path: defaultStoragePath,
			},
		)
	}

	return trace.Wrap(sc.Destination.Get().CheckAndSetDefaults())
}

func (sc *StorageConfig) MarshalYAML() (interface{}, error) {
	// Effectively inlines the destination
	return sc.Destination.MarshalYAML()
}

func (sc *StorageConfig) UnmarshalYAML(node *yaml.Node) error {
	// Effectively inlines the destination
	return sc.Destination.UnmarshalYAML(node)
}
