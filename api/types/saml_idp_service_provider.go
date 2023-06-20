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

import (
	"encoding/xml"
	"fmt"
	"sort"
	"time"

	"github.com/gravitational/teleport/api/utils"
	"github.com/gravitational/trace"
)

// SAMLIdPServiceProvider specifies configuration for service providers for Teleport's built in SAML IdP.
//
// Note: The EntityID is the entity ID for the entity descriptor. This ID is checked that it
// matches the entity ID in the entity descriptor at upsert time to avoid having to parse the
// XML blob in the entity descriptor every time we need to use this resource.
type SAMLIdPServiceProvider interface {
	ResourceWithLabels
	// GetEntityDescriptor returns the entity descriptor of the service provider.
	GetEntityDescriptor() string
	// SetEntityDescriptor sets the entity descriptor of the service provider.
	SetEntityDescriptor(string)
	// GetEntityID returns the entity ID.
	GetEntityID() string
	// SetEntityID sets the entity ID.
	SetEntityID(string)
}

// NewSAMLIdPServiceProvider returns a new SAMLIdPServiceProvider based off a metadata object and SAMLIdPServiceProviderSpecV1.
func NewSAMLIdPServiceProvider(metadata Metadata, spec SAMLIdPServiceProviderSpecV1) (SAMLIdPServiceProvider, error) {
	s := &SAMLIdPServiceProviderV1{
		ResourceHeader: ResourceHeader{
			Metadata: metadata,
		},
		Spec: spec,
	}

	if err := s.CheckAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}
	return s, nil
}

// GetEntityDescriptor returns the entity descriptor.
func (s *SAMLIdPServiceProviderV1) GetEntityDescriptor() string {
	return s.Spec.EntityDescriptor
}

// SetEntityDescriptor sets the entity descriptor.
func (s *SAMLIdPServiceProviderV1) SetEntityDescriptor(entityDescriptor string) {
	s.Spec.EntityDescriptor = entityDescriptor
}

// GetEntityID returns the entity ID.
func (s *SAMLIdPServiceProviderV1) GetEntityID() string {
	return s.Spec.EntityID
}

// SetEntityID sets the entity ID.
func (s *SAMLIdPServiceProviderV1) SetEntityID(entityID string) {
	s.Spec.EntityID = entityID
}

// String returns the SAML IdP service provider string representation.
func (s *SAMLIdPServiceProviderV1) String() string {
	return fmt.Sprintf("SAMLIdPServiceProviderV1(Name=%v)",
		s.GetName())
}

// MatchSearch goes through select field values and tries to
// match against the list of search values.
func (s *SAMLIdPServiceProviderV1) MatchSearch(values []string) bool {
	fieldVals := append(utils.MapToStrings(s.GetAllLabels()), s.GetEntityID(), s.GetName(), SAMLIdPServiceProviderDescription)
	return MatchSearch(fieldVals, values, nil)
}

// setStaticFields sets static resource header and metadata fields.
func (s *SAMLIdPServiceProviderV1) setStaticFields() {
	s.Kind = KindSAMLIdPServiceProvider
	s.Version = V1
}

// CheckAndSetDefaults checks and sets default values
func (s *SAMLIdPServiceProviderV1) CheckAndSetDefaults() error {
	s.setStaticFields()
	if err := s.Metadata.CheckAndSetDefaults(); err != nil {
		return trace.Wrap(err)
	}

	if s.Spec.EntityDescriptor == "" {
		return trace.BadParameter("missing entity descriptor")
	}

	if s.Spec.EntityID == "" {
		// Extract just the entityID attribute from the descriptor
		ed := &struct {
			EntityID string `xml:"entityID,attr"`
		}{}
		err := xml.Unmarshal([]byte(s.Spec.EntityDescriptor), ed)
		if err != nil {
			return trace.Wrap(err)
		}

		s.Spec.EntityID = ed.EntityID
	}

	return nil
}

// SAMLIdPServiceProviders is a list of SAML IdP service provider resources.
type SAMLIdPServiceProviders []SAMLIdPServiceProvider

// AsResources returns these service providers as resources with labels.
func (s SAMLIdPServiceProviders) AsResources() ResourcesWithLabels {
	resources := make([]ResourceWithLabels, 0, len(s))
	for _, sp := range s {
		resources = append(resources, sp)
	}
	return resources
}

// Len returns the slice length.
func (s SAMLIdPServiceProviders) Len() int { return len(s) }

// Less compares service providers by name.
func (s SAMLIdPServiceProviders) Less(i, j int) bool { return s[i].GetName() < s[j].GetName() }

// Swap swaps two service providers.
func (s SAMLIdPServiceProviders) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// AppServerOrSAMLIdPServiceProvider holds either an AppServer or a SAMLIdPServiceProvider resource (never both).
// This is the resource used for WebUI requests to list Applications since we want to list both AppServers and
// SAMLIdPServiceProviders in the UI.
type AppServerOrSAMLIdPServiceProvider interface {
	ResourceWithLabels
	GetAppServer() *AppServerV3
	GetSAMLIdPServiceProvider() *SAMLIdPServiceProviderV1
	GetAppOrServiceProviderName() string
	GetAppOrServiceProviderDescription() string
	GetAppOrServiceProviderPublicAddr() string
	IsAppServer() bool
}

const (
	// This is the `Description` of a SAML IdP Service Provider to show when listing it in the WebUI.
	SAMLIdPServiceProviderDescription = "SAML Application"
)

// // GetAppServer returns the AppServer in this AppServerOrSAMLIdPServiceProvider.
// func (a *AppServerOrSAMLIdPServiceProviderV1) GetAppServer() AppServer {
// 	appOrSP := a.AppServerOrSAMLIdPServiceProvider
// 	return appOrSP.(AppServer)
// }

// // GetAppServer returns the GetSAMLIdPServiceProvider in this AppServerOrSAMLIdPServiceProvider.
// func (a *AppServerOrSAMLIdPServiceProviderV1) GetSAMLIdPServiceProvider() SAMLIdPServiceProvider {
// 	appOrSP := a.AppServerOrSAMLIdPServiceProvider
// 	return appOrSP.(SAMLIdPServiceProvider)
// }

func (a *AppServerOrSAMLIdPServiceProviderV1) GetKind() string {
	if a.IsAppServer() {
		return KindSAMLIdPServiceProvider
	}
	return KindAppServer
}

// GetAppOrServiceProviderName returns the name of either the App or the SAMLIdPServiceProvider, depending on which one
// the AppServerOrSAMLIdPServiceProvider holds.
func (a *AppServerOrSAMLIdPServiceProviderV1) GetAppOrServiceProviderName() string {
	if a.IsAppServer() {
		return a.GetAppServer().GetApp().GetName()
	}
	return a.GetSAMLIdPServiceProvider().GetName()
}

// GetAppOrServiceProviderDescription returns the name of either the App or the SAMLIdPServiceProvider, depending on which one
// the AppServerOrSAMLIdPServiceProvider holds.
func (a *AppServerOrSAMLIdPServiceProviderV1) GetAppOrServiceProviderDescription() string {
	if a.IsAppServer() {
		return a.GetAppServer().GetApp().GetDescription()
	}
	return SAMLIdPServiceProviderDescription
}

// GetAppOrServiceProviderPublicAddr returns the name of either the App or the SAMLIdPServiceProvider, depending on which one
// the AppServerOrSAMLIdPServiceProvider holds.
func (a *AppServerOrSAMLIdPServiceProviderV1) GetAppOrServiceProviderPublicAddr() string {
	if a.IsAppServer() {
		return a.GetAppServer().GetApp().GetPublicAddr()
	}
	// SAMLIdPServiceProviders don't have a PublicAddr
	return ""
}

// IsAppServer returns a bool that determines whether this AppServerOrSAMLIdPServiceProvider holds an AppServer.
// If it is false, it means it holds a SAMLIdPServiceProvider instead.
func (a *AppServerOrSAMLIdPServiceProviderV1) IsAppServer() bool {
	appOrSP := a.AppServerOrSP
	_, ok := appOrSP.(*AppServerOrSAMLIdPServiceProviderV1_AppServer)
	return ok
}

// AppServersOrSAMLIdPServiceProviders is a list of AppServers or SAMLIdPServiceProviders.
type AppServersOrSAMLIdPServiceProviders []AppServerOrSAMLIdPServiceProvider

func (s AppServersOrSAMLIdPServiceProviders) AsResources() []ResourceWithLabels {
	resources := make([]ResourceWithLabels, 0, len(s))
	for _, app := range s {
		if app.IsAppServer() {
			resources = append(resources, ResourceWithLabels(app.GetAppServer()))
		} else {
			resources = append(resources, ResourceWithLabels(app.GetSAMLIdPServiceProvider()))
		}
	}
	return resources
}

// SortByCustom custom sorts by given sort criteria.
func (s AppServersOrSAMLIdPServiceProviders) SortByCustom(sortBy SortBy) error {
	if sortBy.Field == "" {
		return nil
	}

	isDesc := sortBy.IsDesc
	switch sortBy.Field {
	case ResourceMetadataName:
		sort.SliceStable(s, func(i, j int) bool {
			return stringCompare(s[i].GetAppOrServiceProviderName(), s[j].GetAppOrServiceProviderName(), isDesc)
		})
	case ResourceSpecDescription:
		sort.SliceStable(s, func(i, j int) bool {
			return stringCompare(s[i].GetAppOrServiceProviderDescription(), s[j].GetAppOrServiceProviderDescription(), isDesc)
		})
	case ResourceSpecPublicAddr:
		sort.SliceStable(s, func(i, j int) bool {
			return stringCompare(s[i].GetAppOrServiceProviderPublicAddr(), s[j].GetAppOrServiceProviderPublicAddr(), isDesc)
		})
	default:
		return trace.NotImplemented("sorting by field %q for resource %q is not supported", sortBy.Field, KindAppAndIdPServiceProvider)
	}

	return nil
}

// CheckAndSetDefaults checks and sets default values for any missing fields.
func (a *AppServerOrSAMLIdPServiceProviderV1) CheckAndSetDefaults() error {
	if a.IsAppServer() {
		if err := a.GetAppServer().CheckAndSetDefaults(); err != nil {
			return trace.Wrap(err)
		}
	} else {
		if err := a.GetSAMLIdPServiceProvider().CheckAndSetDefaults(); err != nil {
			return trace.Wrap(err)
		}
	}
	return nil
}

func (a *AppServerOrSAMLIdPServiceProviderV1) Expiry() time.Time {
	if a.IsAppServer() {
		appServer := a.GetAppServer()
		return appServer.Metadata.Expiry()
	} else {
		sp := a.GetSAMLIdPServiceProvider()
		return sp.Metadata.Expiry()
	}
}

func (a *AppServerOrSAMLIdPServiceProviderV1) GetAllLabels() map[string]string {
	if a.IsAppServer() {
		appServer := a.GetAppServer()
		staticLabels := make(map[string]string)
		for name, value := range appServer.Metadata.Labels {
			staticLabels[name] = value
		}

		var dynamicLabels map[string]CommandLabelV2
		if appServer.Spec.App != nil {
			for name, value := range appServer.Spec.App.Metadata.Labels {
				staticLabels[name] = value
			}

			dynamicLabels = appServer.Spec.App.Spec.DynamicLabels
		}

		return CombineLabels(staticLabels, dynamicLabels)
	} else {
		sp := a.GetSAMLIdPServiceProvider()
		return sp.Metadata.Labels
	}
}

func (a *AppServerOrSAMLIdPServiceProviderV1) GetLabel(key string) (value string, ok bool) {
	if a.IsAppServer() {
		appServer := a.GetAppServer()
		if cmd, ok := appServer.Spec.App.Spec.DynamicLabels[key]; ok {
			return cmd.Result, ok
		}

		v, ok := appServer.Spec.App.Metadata.Labels[key]
		return v, ok
	} else {
		return "", true
	}
}

func (a *AppServerOrSAMLIdPServiceProviderV1) GetMetadata() Metadata {
	if a.IsAppServer() {
		appServer := a.GetAppServer()
		return appServer.Metadata
	} else {
		sp := a.GetSAMLIdPServiceProvider()
		return sp.Metadata
	}
}

// GetName returns the name of either the App or the SAMLIdPServiceProvider, depending on which one
// the AppServerOrSAMLIdPServiceProvider holds.
func (a *AppServerOrSAMLIdPServiceProviderV1) GetName() string {
	if a.IsAppServer() {
		appServer := a.GetAppServer()
		return appServer.Metadata.Name
	} else {
		sp := a.GetSAMLIdPServiceProvider()
		return sp.Metadata.Name
	}
}

func (a *AppServerOrSAMLIdPServiceProviderV1) SetName(name string) {
	if a.IsAppServer() {
		appServer := a.GetAppServer()
		appServer.Metadata.Name = name
	} else {
		sp := a.GetSAMLIdPServiceProvider()
		sp.Metadata.Name = name
	}
}

func (a *AppServerOrSAMLIdPServiceProviderV1) GetResourceID() int64 {
	if a.IsAppServer() {
		appServer := a.GetAppServer()
		return appServer.Metadata.ID
	} else {
		sp := a.GetSAMLIdPServiceProvider()
		return sp.Metadata.ID
	}
}

func (a *AppServerOrSAMLIdPServiceProviderV1) SetResourceID(id int64) {
	if a.IsAppServer() {
		appServer := a.GetAppServer()
		appServer.Metadata.ID = id
	} else {
		sp := a.GetSAMLIdPServiceProvider()
		sp.Metadata.ID = id
	}
}

func (a *AppServerOrSAMLIdPServiceProviderV1) GetStaticLabels() map[string]string {
	if a.IsAppServer() {
		appServer := a.GetAppServer()
		return appServer.Metadata.Labels
	} else {
		sp := a.GetSAMLIdPServiceProvider()
		return sp.Metadata.Labels
	}
}

func (a *AppServerOrSAMLIdPServiceProviderV1) SetStaticLabels(sl map[string]string) {
	if a.IsAppServer() {
		appServer := a.GetAppServer()
		appServer.Metadata.Labels = sl
	} else {
		sp := a.GetSAMLIdPServiceProvider()
		sp.Metadata.Labels = sl
	}
}

func (a *AppServerOrSAMLIdPServiceProviderV1) GetSubKind() string {
	if a.IsAppServer() {
		appServer := a.GetAppServer()
		return appServer.SubKind
	} else {
		sp := a.GetSAMLIdPServiceProvider()
		return sp.SubKind
	}
}

func (a *AppServerOrSAMLIdPServiceProviderV1) SetSubKind(sk string) {
	if a.IsAppServer() {
		appServer := a.GetAppServer()
		appServer.SubKind = sk
	} else {
		sp := a.GetSAMLIdPServiceProvider()
		sp.SubKind = sk
	}
}

func (a *AppServerOrSAMLIdPServiceProviderV1) GetVersion() string {
	if a.IsAppServer() {
		appServer := a.GetAppServer()
		return appServer.Version
	} else {
		sp := a.GetSAMLIdPServiceProvider()
		return sp.Version
	}
}

// MatchSearch goes through select field values and tries to
// match against the list of search values.
func (a *AppServerOrSAMLIdPServiceProviderV1) MatchSearch(values []string) bool {
	return MatchSearch(nil, values, nil)
}

func (a *AppServerOrSAMLIdPServiceProviderV1) Origin() string {
	if a.IsAppServer() {
		appServer := a.GetAppServer()
		return appServer.Metadata.Origin()
	} else {
		sp := a.GetSAMLIdPServiceProvider()
		return sp.Metadata.Origin()
	}
}

// SetOrigin sets the origin value of the resource.
func (a *AppServerOrSAMLIdPServiceProviderV1) SetOrigin(origin string) {
	if a.IsAppServer() {
		appServer := a.GetAppServer()
		appServer.Metadata.SetOrigin(origin)
	} else {
		sp := a.GetSAMLIdPServiceProvider()
		sp.Metadata.SetOrigin(origin)
	}
}

func (a *AppServerOrSAMLIdPServiceProviderV1) SetExpiry(expiry time.Time) {
	if a.IsAppServer() {
		appServer := a.GetAppServer()
		appServer.Metadata.SetExpiry(expiry)
	} else {
		sp := a.GetSAMLIdPServiceProvider()
		sp.Metadata.SetExpiry(expiry)
	}
}

func (a *AppServerOrSAMLIdPServiceProviderV1) String() string {
	if a.IsAppServer() {
		appServer := a.GetAppServer()
		return fmt.Sprintf("AppServer(Name=%v, Version=%v, Hostname=%v, HostID=%v, App=%v)",
			appServer.GetName(), appServer.GetVersion(), appServer.GetHostname(), appServer.GetHostID(), appServer.GetApp())
	} else {
		sp := a.GetSAMLIdPServiceProvider()
		return fmt.Sprintf("SAMLIdPServiceProvider(Name=%v, Version=%v, EntityID=%v)",
			sp.GetName(), sp.GetVersion(), sp.GetEntityID())
	}
}
