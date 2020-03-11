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

package events

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gravitational/teleport/lib/auth/proto"
	"github.com/gravitational/teleport/lib/session"

	"github.com/gravitational/trace"
	"github.com/gravitational/trace/trail"

	"github.com/sirupsen/logrus"
)

const (
	stateInit     = 0
	stateOpen     = 1
	stateChunk    = 2
	stateClose    = 3
	stateComplete = 4
)

const (
	concurrentStreams = 10
)

type SessionUploader interface {
	UploadSessionRecording(r SessionRecording) error
}

type StreamManager struct {
	log *logrus.Entry

	pool         sync.Pool
	semaphore    chan struct{}
	closeContext context.Context
}

type Stream struct {
	manager  *StreamManager
	state    int64
	uploader SessionUploader

	// tarWriter is used to create the archive itself.
	tarWriter *tar.Writer

	// zipBuffer
	zipBuffer bytes.Buffer

	// zipWriter is used to create the zip files within the archive.
	zipWriter *gzip.Writer

	closeContext context.Context
	closeCancel  context.CancelFunc
}

func NewStreamManger(ctx context.Context) *StreamManager {
	return &StreamManager{
		log: logrus.WithFields(logrus.Fields{
			trace.Component: "stream",
		}),
		pool: sync.Pool{
			New: func() interface{} {
				return new(bytes.Buffer)
			},
		},
		semaphore:    make(chan struct{}, concurrentStreams),
		closeContext: ctx,
	}
}

func (s *StreamManager) NewStream(ctx context.Context, uploader SessionUploader) (*Stream, error) {
	ctx, cancel := context.WithCancel(ctx)
	return &Stream{
		manager:      s,
		state:        stateInit,
		uploader:     uploader,
		closeContext: ctx,
		closeCancel:  cancel,
	}, nil
}

func (s *Stream) Process(chunk *proto.SessionChunk) error {
	var err error

	switch chunk.GetState() {
	case stateInit:
		fmt.Printf("--> Process: stateInit.\n")

		// Create a reader/writer pipe to reduce overall memory usage. In the
		// previous version of Teleport the entire archive was read in, expanded,
		// validated, then uploaded. Now instead chunks are validated and uploaded
		// as they come in.
		pr, pw := io.Pipe()
		s.tarWriter = tar.NewWriter(pw)

		// Start uploading data as it's written to the pipe.
		go s.upload(chunk.GetNamespace(), session.ID(chunk.GetSessionID()), pr)
	case stateOpen:
		// TODO: Use a sync.Pool here.
		s.zipWriter = gzip.NewWriter(&s.zipBuffer)
	case stateChunk:
		// If the chunk is an events chunk, then validate it.
		switch {
		case strings.Contains(chunk.GetType(), "events.gz"):
			// TODO: Validate event.
			_, err = s.zipWriter.Write(append(chunk.GetPayload(), '\n'))
		default:
			_, err = s.zipWriter.Write(chunk.GetPayload())
		}
	case stateClose:
		err = s.zipWriter.Close()
		if err != nil {
			return trace.Wrap(err)
		}
		err := s.tarWriter.WriteHeader(&tar.Header{
			Name: chunk.GetFilename(),
			Mode: 0600,
			Size: int64(s.zipBuffer.Len()),
		})
		if err != nil {
			return trace.Wrap(err)
		}
		_, err = io.Copy(s.tarWriter, &s.zipBuffer)
		if err != nil {
			return trace.Wrap(err)
		}
	case stateComplete:
		err = s.tarWriter.Close()
		if err != nil {
			return trace.Wrap(err)
		}
	}
	return nil
}

func (s *Stream) GetState() int64 {
	return atomic.LoadInt64(&s.state)
}

func (s *Stream) Reader() io.Reader {
	return nil
}

func (s *Stream) Close() error {
	s.closeCancel()
	return nil
}

func (s *Stream) upload(namespace string, sessionID session.ID, reader io.Reader) {
	err := s.uploader.UploadSessionRecording(SessionRecording{
		CancelContext: s.closeContext,
		SessionID:     sessionID,
		Namespace:     namespace,
		Recording:     reader,
	})
	if err != nil {
		s.manager.log.Warnf("Failed to upload session recording: %v.", err)
	}
}

func StreamSessionRecording(stream proto.AuthService_StreamSessionRecordingClient, r SessionRecording) error {
	err := stream.Send(&proto.SessionChunk{
		State: stateInit,
	})
	if err != nil {
		return trail.FromGRPC(err)
	}

	// All done, send a complete message and close the stream.
	// TODO: Send the close message.
	err = stream.CloseSend()
	if err != nil {
		return trail.FromGRPC(err)
	}
	return nil
}

//func Log(w io.Writer, key, val string) {
//	b := bufPool.Get().(*bytes.Buffer)
//	b.Reset()
//	// Replace this with time.Now() in a real logger.
//	b.WriteString(timeNow().UTC().Format(time.RFC3339))
//	b.WriteByte(' ')
//	b.WriteString(key)
//	b.WriteByte('=')
//	b.WriteString(val)
//	w.Write(b.Bytes())
//	bufPool.Put(b)
//}
