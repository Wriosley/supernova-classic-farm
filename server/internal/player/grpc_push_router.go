package player

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/connection"
	"github.com/Wriosley/supernova-classic-farm/server/internal/push"
)

// GRPCPushRouter owns one direct gRPC client per advertised Gate Pod. It never
// dials the load-balanced Gate Service: a push is valid only for the exact
// Gate incarnation recorded in the player's connection lease.
type GRPCPushRouter struct {
	key         []byte
	serviceName string
	connections interface {
		List(uint64) []connection.PlayerConnection
	}
	mu      sync.Mutex
	clients map[string]*GRPCPushForwarder
}

func NewGRPCPushRouter(key []byte, serviceName string, connections interface {
	List(uint64) []connection.PlayerConnection
}) (*GRPCPushRouter, error) {
	if len(key) == 0 || strings.TrimSpace(serviceName) == "" || connections == nil {
		return nil, errors.New("push router key, service name, and connection registry are required")
	}
	return &GRPCPushRouter{key: append([]byte(nil), key...), serviceName: serviceName, connections: connections, clients: make(map[string]*GRPCPushForwarder)}, nil
}

func (r *GRPCPushRouter) Resolve(gateID, gateEndpoint string) (push.GateClient, error) {
	return r.client(gateID, gateEndpoint)
}

func (r *GRPCPushRouter) client(gateID, endpoint string) (*GRPCPushForwarder, error) {
	if strings.TrimSpace(gateID) == "" || strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("gate target is incomplete")
	}
	key := gateID + "\x00" + endpoint
	r.mu.Lock()
	defer r.mu.Unlock()
	if client := r.clients[key]; client != nil {
		return client, nil
	}
	client, err := NewGRPCPushForwarder(r.key, r.serviceName, endpoint, gateID)
	if err != nil {
		return nil, err
	}
	r.clients[key] = client
	return client, nil
}

func (r *GRPCPushRouter) Forward(ctx context.Context, envelope *wsv1.WsEnvelope) error {
	if envelope == nil || envelope.TargetPlayerId == 0 {
		return errors.New("push envelope target is required")
	}
	return r.forPlayer(envelope.TargetPlayerId, func(client *GRPCPushForwarder) error { return client.Forward(ctx, envelope) })
}

func (r *GRPCPushRouter) PublishFarmPresence(ctx context.Context, playerID uint64, presence *wsv1.FarmPresencePush) error {
	return r.forPlayer(playerID, func(client *GRPCPushForwarder) error { return client.PublishFarmPresence(ctx, playerID, presence) })
}

func (r *GRPCPushRouter) forPlayer(playerID uint64, publish func(*GRPCPushForwarder) error) error {
	seen := make(map[string]struct{})
	var result error
	for _, lease := range r.connections.List(playerID) {
		if !lease.ExpiresAt.After(time.Now()) {
			continue
		}
		key := lease.GateID + "\x00" + lease.GateEndpoint
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		client, err := r.client(lease.GateID, lease.GateEndpoint)
		if err == nil {
			err = publish(client)
		}
		if err != nil {
			result = errors.Join(result, fmt.Errorf("gate %s: %w", lease.GateID, err))
		}
	}
	return result
}

func (r *GRPCPushRouter) PublishFarmViewPatch(ctx context.Context, gateID, endpoint string, recipients []uint64, patch *wsv1.FarmViewPatch) error {
	client, err := r.client(gateID, endpoint)
	if err != nil {
		return err
	}
	return client.PublishFarmViewPatch(ctx, gateID, endpoint, recipients, patch)
}

func (r *GRPCPushRouter) PublishRedDotChanged(ctx context.Context, gateID, endpoint string, recipients []uint64, payload *push.RedDotChanged) error {
	client, err := r.client(gateID, endpoint)
	if err != nil {
		return err
	}
	return client.PublishRedDotChanged(ctx, gateID, endpoint, recipients, payload)
}

func (r *GRPCPushRouter) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result error
	for key, client := range r.clients {
		result = errors.Join(result, client.Close())
		delete(r.clients, key)
	}
	return result
}
