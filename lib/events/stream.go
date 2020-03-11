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
	"context"
	"io"
	"sync"
	"sync/atomic"

	"github.com/gravitational/teleport/lib/auth/proto"
	"github.com/gravitational/teleport/lib/session"

	"github.com/sirupsen/logrus"
)

const (
	stateInit  = 0
	stateOpen  = 1
	stateClose = 2
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
	tarWriter io.Writer

	// zipWriter is used to create the zip files within the archive.
	zipWriter io.Writer
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

func (s *StreamManager) NewStream(uploader SessionUploader) (*Stream, error) {
	return &Stream{
		manager:  s,
		state:    stateInit,
		uploader: uploader,
	}, nil
}

func (s *Stream) Process(chunk *proto.SessionChunk) error {
	switch chunk.GetState() {
	case stateInit:
		// Create a reader/writer pipe to reduce overall memory usage. In the
		// previous version of Teleport the entire archive was read in, expanded,
		// validated, then uploaded. Now instead chunks are validated and uploaded
		// as they come in.
		pr, pw := io.Pipe()
		s.tarWriter = tar.NewWriter(pw)

		// Start uploading data as it's written to the pipe.
		go s.upload(chunk.GetNamespace(), session.ID(chunk.GetSessionID()), r)
	case stateOpen:
		//// TODO: Use a sync.Pool here.
		//zw := gzip.NewWriter(&buf)

		//zw.Name = "a-new-hope.txt"
		//zw.Comment = "an epic space opera by George Lucas"
		//zw.ModTime = time.Date(1977, time.May, 25, 0, 0, 0, 0, time.UTC)

		//_, err := zw.Write([]byte("A long time ago in a galaxy far, far away..."))
		//if err != nil {
		//	log.Fatal(err)
		//}

		//if err := zw.Close(); err != nil {
		//	log.Fatal(err)
		//}
	case stateClose:
	}
	return nil
}

func (s *Stream) GetState() int64 {
	return atomic.LoadInt64(&s.state)
}

func (s *Stream) Reader() io.Reader {
	return nil
}

func (s *Stream) upload(namespace string, sessionID session.ID, reader io.Reader) {
	err := s.uploader.UploadSessionRecording(&SessionRecording{
		SessionID: session.ID(chunk.GetSessionID()),
		Namespace: chunk.Namespace,
		Recording: pr,
	})
	if err != nil {
		s.manager.log.Warnf("Failed to upload session recording: %v.", err)
	}
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
