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

package recorder

import (
	"github.com/gravitational/trace"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/events"
	"github.com/gravitational/teleport/lib/events/filesessions"
	"github.com/gravitational/teleport/lib/services"
)

func NewRecorder(recConfig types.SessionRecordingConfig, cfg events.AuditWriterConfig, uploadDir string, syncStream events.Streamer) (events.StreamWriter, error) {
	if cfg.Streamer != nil {
		return nil, trace.BadParameter("Streamer must be unset")
	}

	if recConfig.GetMode() == types.RecordOff {
		return &events.DiscardStream{}, nil
	}

	var streamer events.Streamer = syncStream
	if !services.IsRecordSync(recConfig.GetMode()) {
		fileStreamer, err := filesessions.NewStreamer(uploadDir)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		streamer = fileStreamer
	}

	cfg.Streamer = streamer

	return events.NewAuditWriter(cfg)
}
