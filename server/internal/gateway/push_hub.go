package gateway

import (
	"context"
	"errors"
	"sort"
	"sync"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	reasonv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/reason"
	"google.golang.org/protobuf/proto"
)

const maxBufferedPushes = 256

type PushHub struct {
	mu          sync.Mutex
	subscribers map[uint64]map[*connectionSubscription]struct{}
}

type bufferedPush struct {
	envelope *wsv1.WsEnvelope
	body     []byte
}

type connectionSubscription struct {
	playerID uint64
	writer   *serializedWriter
	ctx      context.Context

	mu        sync.Mutex
	ready     bool
	buffering bool
	buffer    []bufferedPush
}

func newPushHub() *PushHub {
	return &PushHub{subscribers: make(map[uint64]map[*connectionSubscription]struct{})}
}

func (h *PushHub) subscribe(playerID uint64, writer *serializedWriter, ctx context.Context) *connectionSubscription {
	subscription := &connectionSubscription{playerID: playerID, writer: writer, ctx: ctx}
	h.mu.Lock()
	if h.subscribers[playerID] == nil {
		h.subscribers[playerID] = make(map[*connectionSubscription]struct{})
	}
	h.subscribers[playerID][subscription] = struct{}{}
	h.mu.Unlock()
	return subscription
}

func (h *PushHub) unsubscribe(subscription *connectionSubscription) {
	if subscription == nil {
		return
	}
	h.mu.Lock()
	delete(h.subscribers[subscription.playerID], subscription)
	if len(h.subscribers[subscription.playerID]) == 0 {
		delete(h.subscribers, subscription.playerID)
	}
	h.mu.Unlock()
}

func (h *PushHub) Publish(envelope *wsv1.WsEnvelope) error {
	if err := validatePushEnvelope(envelope); err != nil {
		return err
	}
	body, err := proto.Marshal(envelope)
	if err != nil {
		return err
	}
	h.mu.Lock()
	subscriptions := make([]*connectionSubscription, 0, len(h.subscribers[envelope.TargetPlayerId]))
	for subscription := range h.subscribers[envelope.TargetPlayerId] {
		subscriptions = append(subscriptions, subscription)
	}
	h.mu.Unlock()
	for _, subscription := range subscriptions {
		_ = subscription.enqueue(envelope, body)
	}
	return nil
}

func (s *connectionSubscription) enqueue(envelope *wsv1.WsEnvelope, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready || s.buffering {
		if len(s.buffer) == maxBufferedPushes {
			s.buffer = s.buffer[1:]
		}
		s.buffer = append(s.buffer, bufferedPush{
			envelope: proto.Clone(envelope).(*wsv1.WsEnvelope),
			body:     append([]byte(nil), body...),
		})
		return nil
	}
	return s.writer.write(s.ctx, body)
}

func (s *connectionSubscription) beginSnapshot() {
	s.mu.Lock()
	s.buffering = true
	s.mu.Unlock()
}

func (s *connectionSubscription) abortSnapshot() {
	s.mu.Lock()
	s.buffering = false
	s.mu.Unlock()
}

func (s *connectionSubscription) finishSnapshot(
	ctx context.Context,
	responseBody []byte,
	snapshotVersion *wsv1.StateVersion,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sort.SliceStable(s.buffer, func(i, j int) bool {
		// FARM_PRESENCE_CHANGED pushes are unversioned (nil StateVersion) and
		// are filtered out below regardless of order, so the getters' nil
		// receiver handling (returning 0) just needs to avoid a panic here.
		left, right := s.buffer[i].envelope.GetStateVersion(), s.buffer[j].envelope.GetStateVersion()
		if left.GetOwnerEpoch() != right.GetOwnerEpoch() {
			return left.GetOwnerEpoch() < right.GetOwnerEpoch()
		}
		return left.GetPlayerSeq() < right.GetPlayerSeq()
	})
	bodies := [][]byte{responseBody}
	for _, push := range s.buffer {
		// Unversioned pushes (e.g. FARM_PRESENCE_CHANGED) do not participate
		// in snapshot catch-up ordering, so they are always delivered once
		// buffered; only versioned pushes are deduplicated against the
		// just-delivered snapshot's StateVersion.
		if push.envelope.StateVersion != nil && !stateVersionAfter(push.envelope.StateVersion, snapshotVersion) {
			continue
		}
		bodies = append(bodies, push.body)
	}
	if err := s.writer.writeBatch(ctx, bodies...); err != nil {
		return err
	}
	s.buffer = nil
	s.ready = true
	s.buffering = false
	return nil
}

func stateVersionAfter(candidate, baseline *wsv1.StateVersion) bool {
	if candidate == nil || baseline == nil {
		return false
	}
	if candidate.OwnerEpoch != baseline.OwnerEpoch {
		return candidate.OwnerEpoch > baseline.OwnerEpoch
	}
	return candidate.PlayerSeq > baseline.PlayerSeq
}

func validatePushEnvelope(envelope *wsv1.WsEnvelope) error {
	if envelope == nil ||
		envelope.ProtocolVersion != ProtocolVersion ||
		envelope.MessageKind != wsv1.MessageKind_PUSH ||
		envelope.RequestId != "" ||
		envelope.TargetPlayerId == 0 ||
		envelope.ServerTimeMs <= 0 ||
		envelope.Error != nil ||
		envelope.Replayed {
		return errors.New("invalid push envelope")
	}
	switch envelope.Action {
	case wsv1.Action_PLAYER_STATE_CHANGED:
		if envelope.StateVersion == nil || envelope.StateVersion.OwnerEpoch == 0 {
			return errors.New("invalid push envelope")
		}
		push := envelope.GetPlayerStateChangedPush()
		if push == nil || push.Reason == reasonv1.StateChangeReason_STATE_CHANGE_REASON_UNSPECIFIED ||
			push.Patch == nil {
			return errors.New("invalid push payload")
		}
	case wsv1.Action_FARM_PRESENCE_CHANGED:
		// Presence is unversioned: it never carries a StateVersion and never
		// participates in GET_PLAYER_SNAPSHOT catch-up ordering.
		if envelope.StateVersion != nil {
			return errors.New("invalid push envelope")
		}
		push := envelope.GetFarmPresenceChangedPush()
		if push == nil || push.OwnerPlayerId != envelope.TargetPlayerId ||
			push.Kind == wsv1.FarmPresenceKind_FARM_PRESENCE_KIND_UNSPECIFIED {
			return errors.New("invalid push payload")
		}
	case wsv1.Action_FARM_VIEW_CHANGED:
		// The public farm view has its own independent (epoch, seq) carried
		// inside the patch itself; it never carries the player's own
		// StateVersion and never participates in GET_PLAYER_SNAPSHOT catch-up
		// ordering (recipients may not even be the owner).
		if envelope.StateVersion != nil {
			return errors.New("invalid push envelope")
		}
		push := envelope.GetFarmViewChangedPush()
		if push == nil || push.OwnerPlayerId == 0 ||
			len(push.GetVersion().GetFarmViewEpoch()) == 0 ||
			push.GetVersion().GetFarmViewSeq() == 0 {
			return errors.New("invalid push payload")
		}
	default:
		return errors.New("invalid push envelope")
	}
	return nil
}
