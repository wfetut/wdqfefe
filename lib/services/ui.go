package services

import (
	"fmt"

	"github.com/gravitational/trace"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/utils"
)

// UnmarshalSessionRecordingConfig unmarshals the SessionRecordingConfig resource from JSON.
func UnmarshalUiConfig(bytes []byte, opts ...MarshalOption) (types.UiConfig, error) {
	var uiConfig types.UiConfigV1

	if len(bytes) == 0 {
		return nil, trace.BadParameter("missing resource data")
	}

	cfg, err := CollectOptions(opts)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	if err := utils.FastUnmarshal(bytes, &uiConfig); err != nil {
		return nil, trace.BadParameter(err.Error())
	}

	err = uiConfig.CheckAndSetDefaults()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	//
	if cfg.ID != 0 {
		uiConfig.SetResourceID(cfg.ID)
	}
	if !cfg.Expires.IsZero() {
		uiConfig.SetExpiry(cfg.Expires)
	}
	return &uiConfig, nil
}

// MarshalSessionRecordingConfig marshals the SessionRecordingConfig resource to JSON.
func MarshalUiConfig(uiConfig types.UiConfig, opts ...MarshalOption) ([]byte, error) {
	if err := uiConfig.CheckAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}

	cfg, err := CollectOptions(opts)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	fmt.Printf("%+v\n", cfg)

	switch uiConfig := uiConfig.(type) {
	case *types.UiConfigV1:
		if version := uiConfig.GetVersion(); version != types.V2 {
			return nil, trace.BadParameter("mismatched ui config version %v and type %T", version, uiConfig)
		}
		if !cfg.PreserveResourceID {
			// avoid modifying the original object
			// to prevent unexpected data races
			copy := *uiConfig
			copy.SetResourceID(0)
			uiConfig = &copy
		}
		return utils.FastMarshal(uiConfig)
	default:
		return nil, trace.BadParameter("unrecognized ui config version %T", uiConfig)
	}
}
