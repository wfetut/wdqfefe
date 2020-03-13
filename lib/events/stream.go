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
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	//"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gravitational/teleport/lib/auth/proto"
	"github.com/gravitational/teleport/lib/session"

	"github.com/gravitational/trace"
	"github.com/gravitational/trace/trail"

	"github.com/sirupsen/logrus"
)

var sss = 0

const (
	stateInit     = 0
	stateOpen     = 1
	stateChunk    = 2
	stateClose    = 3
	stateComplete = 4
)

const (
	concurrentStreams = 2
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
	zipBuffer *bytes.Buffer

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

func (s *StreamManager) takeSemaphore() error {
	select {
	case s.semaphore <- struct{}{}:
		return nil
	case <-s.closeContext.Done():
		return errContext
	}
}

func (s *StreamManager) releaseSemaphore() error {
	select {
	case <-s.semaphore:
		return nil
	case <-s.closeContext.Done():
		return errContext
	}
}

func (s *Stream) Process(chunk *proto.SessionChunk) error {
	var err error

	switch chunk.GetState() {
	case stateInit:
		//fmt.Printf("--> Process: stateInit.\n")

		// Create a reader/writer pipe to reduce overall memory usage. In the
		// previous version of Teleport the entire archive was read in, expanded,
		// validated, then uploaded. Now instead chunks are validated and uploaded
		// as they come in.
		pr, pw := io.Pipe()
		s.tarWriter = tar.NewWriter(pw)

		// Start uploading data as it's written to the pipe.
		go s.upload(chunk.GetNamespace(), session.ID(chunk.GetSessionID()), pr)
	case stateOpen:
		err := s.tarWriter.WriteHeader(&tar.Header{
			Name: chunk.GetFilename(),
			Mode: 0600,
			Size: int64(chunk.GetTotalSize()),
		})
		if err != nil {
			return trace.Wrap(err)
		}
		// sss = 0
	case stateChunk:
		//fmt.Printf("--> Process: stateChunk.\n")

		// If the chunk is an events chunk, then validate it.
		//switch {
		//case strings.Contains(chunk.GetType(), "events"):
		//	// TODO: Validate event.
		//	//fmt.Printf("--> Process: stateChunk: %v.\n", string(chunk.GetPayload()))

		//	_, err = s.zipWriter.Write(append(chunk.GetPayload(), '\n'))
		//	if err != nil {
		//		return trace.Wrap(err)
		//	}
		//default:
		//	_, err = s.zipWriter.Write(chunk.GetPayload())
		//	if err != nil {
		//		return trace.Wrap(err)
		//	}
		//}
		//sss += len(chunk.GetPayload())
		//fmt.Printf("--> Got %v: %v.\n", sss, string(chunk.GetPayload()))
		_, err := s.tarWriter.Write(append(chunk.GetPayload(), '\n'))
		if err != nil {
			return trace.Wrap(err)
		}
	case stateClose:
		//fmt.Printf("--> Process: stateClose. Filename: %v, Len: %v.\n", chunk.GetFilename(), s.zipBuffer.Len())

		//err := s.tarWriter.WriteHeader(&tar.Header{
		//	Name: chunk.GetFilename(),
		//	Mode: 0600,
		//	Size: int64(s.zipBuffer.Len()),
		//})
		//if err != nil {
		//	return trace.Wrap(err)
		//}
		//_, err = io.Copy(s.tarWriter, s.zipBuffer)
		//if err != nil {
		//	return trace.Wrap(err)
		//}

		//s.zipBuffer.Reset()
		//s.manager.pool.Put(s.zipBuffer)

		//err = s.manager.releaseSemaphore()
		//if err != nil {
		//	return trace.Wrap(err)
		//}
	case stateComplete:
		//fmt.Printf("--> Process: stateComplete.\n")

		err = s.tarWriter.Close()
		if err != nil {
			return trace.Wrap(err)
		}

		return fmt.Errorf("blahblah")

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
	// Initialize stream.
	err := stream.Send(&proto.SessionChunk{
		State:     stateInit,
		Namespace: r.Namespace,
		SessionID: r.SessionID.String(),
	})
	if err != nil {
		return trail.FromGRPC(err)
	}

	tr := tar.NewReader(r.Recording)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return trace.Wrap(err)
		}

		if !strings.Contains(hdr.Name, "events.gz") {
			_, err = io.Copy(ioutil.Discard, tr)
			if err != nil {
				return trace.Wrap(err)
			}
			continue
		}

		var buf bytes.Buffer
		_, err = io.Copy(&buf, tr)
		if err != nil {
			return trace.Wrap(err)
		}

		zr, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
		if err != nil {
			return trace.Wrap(err)
		}
		var buf2 bytes.Buffer
		_, err = io.Copy(&buf2, zr)
		if err != nil {
			return trace.Wrap(err)
		}
		zr.Close()

		//fmt.Printf("--> sending length: %v.\n", buf2.Len())
		//fmt.Printf("--> %v\n", buf.Bytes())

		// Send file open event.
		err = stream.Send(&proto.SessionChunk{
			State:     stateOpen,
			Filename:  hdr.Name,
			TotalSize: int64(buf2.Len()),
		})
		if err != nil {
			return trail.FromGRPC(err)
		}

		//eee := 0

		//zr, err := gzip.NewReader(&buf)
		//if err != nil {
		//	return trace.Wrap(err)
		//}
		if strings.Contains(hdr.Name, "events.gz") {
			scanner := bufio.NewScanner(&buf2)
			for scanner.Scan() {

				//eee += len(scanner.Bytes())
				//fmt.Printf("--> scanner: %v %v.\n", eee, string(scanner.Bytes()))
				// Send chunk.
				err = stream.Send(&proto.SessionChunk{
					State:   stateChunk,
					Type:    "events",
					Payload: scanner.Bytes(),
				})
			}
			err = scanner.Err()
			if err != nil {
				return trace.Wrap(err)
			}
		} else {
			_, err = io.Copy(ioutil.Discard, zr)
			if err != nil {
				return trace.Wrap(err)
			}
		}
	}

	// Send complete event.
	err = stream.Send(&proto.SessionChunk{
		State: stateComplete,
	})
	if err != nil {
		return trace.Wrap(err)
	}
	fmt.Printf("--> Client: Complete.\n")

	// All done, send a complete message and close the stream.
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
