package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	apievents "github.com/gravitational/teleport/api/types/events"
	"github.com/gravitational/teleport/lib/client/terminal"
	"github.com/gravitational/teleport/lib/session"
	"github.com/gravitational/trace"
	"github.com/jonboulle/clockwork"
	"github.com/sirupsen/logrus"
)

func newStreamingPlayer(streamer streamer, sid session.ID, term *terminal.Terminal) *streamingTermPlayer {
	player := &streamingTermPlayer{
		streamer: streamer,
		sid:      sid,
		term:     term,

		clock: clockwork.NewRealClock(),
		log:   log,
	}
	player.cond = sync.NewCond(&player.mu)
	return player
}

// streamer is the interface that can provide with a stream of events related to
// a particular session.
type streamer interface {
	StreamSessionEvents(ctx context.Context, sessionID session.ID,
		startIndex int64) (chan apievents.AuditEvent, chan error)
}

// streamingTermPlayer plays a recorded SSH session to the terminal
// by streaming session events from the auth server
type streamingTermPlayer struct {
	streamer streamer
	sid      session.ID

	term *terminal.Terminal

	mu                 sync.Mutex
	cond               *sync.Cond
	lastPlayedEvent    int64
	fastForwardToEvent int64
	playing            bool

	clock clockwork.Clock
	log   logrus.FieldLogger
}

func (s *streamingTermPlayer) Play(ctx context.Context) error {
	// for now, assume this is only called once at startup
	// and that there's no way playback can be in progress

	s.cond.L.Lock()
	s.playing = true
	s.cond.L.Unlock()

	s.log.Debugf("playing from beginning")

	// clear screen to start
	s.term.Stdout().Write([]byte("\x1bc"))

	return s.streamFromBeginning(ctx)
}

func (s *streamingTermPlayer) streamFromBeginning(ctx context.Context) error {
	var lastDelay int64
	eventsC, errorC := s.streamer.StreamSessionEvents(ctx, s.sid, 0)
	for {
		// if paused, wait until told to play
		// TODO: avoid deadlock here on ctrl+c, maybe via a channel that
		// we can select on like anything else
		s.waitUntilPlaying()

		select {
		case err := <-errorC:
			if err != nil && !errors.Is(err, context.Canceled) {
				return trace.Wrap(err)
			}
			return nil
		case <-ctx.Done():
			if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
				return trace.Wrap(err)
			}
			return nil
		case evt := <-eventsC:
			if evt == nil {
				s.log.Debug("reached end of playback stream")
				return nil
			}
			switch e := evt.(type) {
			case *apievents.SessionPrint:
				if e.DelayMilliseconds > lastDelay {
					s.mu.Lock()

					// only apply timing delay if we've "caught up"
					// to real time
					shouldApplyDelay := s.fastForwardToEvent == 0 ||
						s.lastPlayedEvent < s.fastForwardToEvent
					s.mu.Unlock()
					if shouldApplyDelay {
						s.applyDelay(lastDelay, e)
					}
					timestampFrame(s.term, e.Time.String())
					lastDelay = e.DelayMilliseconds
				}
				s.term.Stdout().Write(e.Data)
			case *apievents.Resize:
				s.resize(e.TerminalSize)
			case *apievents.SessionStart:
				s.resize(e.TerminalSize)
			}

			s.mu.Lock()
			s.lastPlayedEvent = evt.GetIndex()
			s.mu.Unlock()
		}
	}
}

func (s *streamingTermPlayer) applyDelay(lastTimestamp int64, e *apievents.SessionPrint) {
	delayMillis := e.DelayMilliseconds - lastTimestamp

	// smooth out playback
	switch {
	case delayMillis < 10:
		delayMillis = 0
	case delayMillis > 250 && delayMillis < 500:
		delayMillis = 250
	case delayMillis > 500 && delayMillis < 1000:
		delayMillis = 500
	case delayMillis > 1000:
		delayMillis = 1000
	}

	s.clock.Sleep(time.Duration(delayMillis) * time.Millisecond)
}

func (s *streamingTermPlayer) waitUntilPlaying() {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()

	// TODO: consider writing player state on the top row with timestamp
	// writeAtLocation(s.term, 0, 2, []byte("PAUSED "))

	for !s.playing {
		s.cond.Wait()
	}
}

func (s *streamingTermPlayer) resize(terminalSize string) {
	parts := strings.Split(terminalSize, ":")
	if len(parts) != 2 {
		return
	}
	width, height := parts[0], parts[1]
	fmt.Fprintf(s.term.Stdout(), "\x1b[8;%s;%st", height, width)
}

// TogglePause toggles the player between playing and paused states.
func (s *streamingTermPlayer) TogglePause() {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()

	s.playing = !s.playing
	s.cond.Broadcast()
}

// Rewind moves the current playback position back
// towards the beginning of the session.
func (s *streamingTermPlayer) Rewind() {
	// The state of the terminal is a function of all the output
	// that has occurred since the beginning of the session, so
	// in order to rewind we go back to the beginning and
	// "fast forward" up to some point. This makes rewinding
	// a more costly operation than fast-forwarding, so we rewind
	// by more events to compensate.
	s.mu.Lock()
	s.fastForwardToEvent = s.lastPlayedEvent - 5
	if s.fastForwardToEvent < 0 {
		s.fastForwardToEvent = 0
	}
	s.log.Debugf("rewinding from %v to %v", s.lastPlayedEvent, s.fastForwardToEvent)
	// TODO..
	s.mu.Unlock()
}

// Forward advances the current playback position
// towards the end of the session.
func (s *streamingTermPlayer) Forward() {
	s.mu.Lock()
	s.fastForwardToEvent = s.lastPlayedEvent + 2
	s.log.Debugf("forwarding from %v to %v", s.lastPlayedEvent, s.fastForwardToEvent)
	s.mu.Unlock()
}
