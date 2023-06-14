/*
Copyright 2020 Gravitational, Inc.

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

package db

import (
	"path/filepath"

	"github.com/gravitational/trace"

	"github.com/gravitational/teleport"
	apidefaults "github.com/gravitational/teleport/api/defaults"
	libevents "github.com/gravitational/teleport/lib/events"
	"github.com/gravitational/teleport/lib/events/recorder"
	"github.com/gravitational/teleport/lib/session"
	"github.com/gravitational/teleport/lib/srv/db/common"
)

// newStreamWriter creates a streamer that will be used to stream the
// requests that occur within this session to the audit log.
func (s *Server) newStreamWriter(sessionCtx *common.Session) (libevents.StreamWriter, error) {
	recConfig, err := s.cfg.AccessPoint.GetSessionRecordingConfig(s.closeContext)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	clusterName, err := s.cfg.AccessPoint.GetClusterName()
	if err != nil {
		return nil, trace.Wrap(err)
	}

	cfg := libevents.AuditWriterConfig{
		// Audit stream is using server context, not session context,
		// to make sure that session is uploaded even after it is closed
		Context:     s.closeContext,
		Clock:       s.cfg.Clock,
		SessionID:   session.ID(sessionCtx.ID),
		Namespace:   apidefaults.Namespace,
		ServerID:    sessionCtx.HostID,
		Component:   teleport.ComponentDatabase,
		ClusterName: clusterName.GetClusterName(),
	}
	uploadDir := filepath.Join(
		s.cfg.DataDir, teleport.LogsDir, teleport.ComponentUpload,
		libevents.StreamingSessionsDir, apidefaults.Namespace,
	)

	return recorder.NewRecorder(recConfig, cfg, uploadDir, s.cfg.AuthClient)
}
