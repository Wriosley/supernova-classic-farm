package main

import (
	"context"
	"errors"
	"sync"
)

type readinessState struct {
	mu     sync.RWMutex
	ready  bool
	reason string
}

func newReadinessState() *readinessState {
	return &readinessState{reason: "startup incomplete"}
}

func (state *readinessState) SetReady() {
	state.mu.Lock()
	state.ready = true
	state.reason = ""
	state.mu.Unlock()
}

func (state *readinessState) SetNotReady(reason string) {
	if reason == "" {
		reason = "not ready"
	}
	state.mu.Lock()
	state.ready = false
	state.reason = reason
	state.mu.Unlock()
}

func (state *readinessState) Check(context.Context) error {
	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.ready {
		return nil
	}
	return errors.New(state.reason)
}
