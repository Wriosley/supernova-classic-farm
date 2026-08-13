package publisher

import (
	"sync"

	coordinatorv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator"
)

type Session struct {
	id       string
	kind     coordinatorv1.SubscriberKind
	messages chan *coordinatorv1.WatchRoutesResponse
	done     chan struct{}
	once     sync.Once
}

func (s *Session) ID() string                                          { return s.id }
func (s *Session) Kind() coordinatorv1.SubscriberKind                  { return s.kind }
func (s *Session) Messages() <-chan *coordinatorv1.WatchRoutesResponse { return s.messages }
func (s *Session) Done() <-chan struct{}                               { return s.done }
func (s *Session) close()                                              { s.once.Do(func() { close(s.done) }) }
func (s *Session) enqueue(message *coordinatorv1.WatchRoutesResponse) bool {
	select {
	case <-s.done:
		return false
	default:
	}
	select {
	case s.messages <- message:
		return true
	default:
		return false
	}
}
