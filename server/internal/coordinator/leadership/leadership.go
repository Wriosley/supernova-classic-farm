// Package leadership elects a single Coordinator mutator via an opaque Elector.
// Followers may still serve route SDK reads from durable-aligned local state.
package leadership

import (
	"context"
	"sync"
)

// State is a point-in-time view of local election status.
type State struct {
	IsLeader       bool
	Identity       string
	LeaderIdentity string
	Generation     uint64
}

// Callbacks are invoked by an Elector. OnStartedLeading receives a context that
// is cancelled when leadership is lost; Generation strictly increases on each
// local acquire.
type Callbacks struct {
	OnStartedLeading func(ctx context.Context, generation uint64)
	OnStoppedLeading func(generation uint64)
	OnNewLeader      func(identity string)
}

// Elector runs leader election until ctx is cancelled.
type Elector interface {
	Run(ctx context.Context, callbacks Callbacks) error
	State() State
}

// Tracker is a concurrency-safe leadership state holder used by wiring and tests.
type Tracker struct {
	mu             sync.RWMutex
	identity       string
	leaderIdentity string
	isLeader       bool
	generation     uint64
}

func NewTracker(identity string) *Tracker {
	return &Tracker{identity: identity}
}

func (t *Tracker) State() State {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return State{
		IsLeader:       t.isLeader,
		Identity:       t.identity,
		LeaderIdentity: t.leaderIdentity,
		Generation:     t.generation,
	}
}

func (t *Tracker) Identity() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.identity
}

func (t *Tracker) SetLeaderIdentity(identity string) {
	t.mu.Lock()
	t.leaderIdentity = identity
	t.mu.Unlock()
}

func (t *Tracker) BeginLeading() (generation uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.generation++
	t.isLeader = true
	t.leaderIdentity = t.identity
	return t.generation
}

func (t *Tracker) EndLeading(generation uint64) (stopped bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.isLeader || t.generation != generation {
		return false
	}
	t.isLeader = false
	return true
}
