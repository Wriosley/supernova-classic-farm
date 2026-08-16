// Package connection holds the per-Zone in-memory player WebSocket connection
// registry. Records never enter Player Checkpoint; Gate must re-register after
// Zone restart.
package connection

import (
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

// LeaseTTL is the Zone-side connection lease. Gate refreshes every 30s.
const LeaseTTL = 90 * time.Second

var (
	ErrInvalidConnection  = errors.New("invalid player connection")
	ErrConnectionMismatch = errors.New("player connection mismatch")
)

// PlayerConnection is one live Gate WebSocket for a player owned by this Zone.
type PlayerConnection struct {
	PlayerID     uint64
	GateID       string
	GateEndpoint string
	ConnectionID string
	ExpiresAt    time.Time
}

func (c PlayerConnection) clone() PlayerConnection { return c }

func (c PlayerConnection) key() string {
	return c.GateID + "\x00" + c.ConnectionID
}

// Registry stores temporary Gate connections for players this Zone owns.
type Registry struct {
	mu      sync.Mutex
	players map[uint64]map[string]PlayerConnection // playerID -> (gateID\0connID) -> record
}

func NewRegistry() *Registry {
	return &Registry{players: make(map[uint64]map[string]PlayerConnection)}
}

// Register upserts one connection lease. Same (player, gate, connection)
// repeats are idempotent and extend ExpiresAt.
func (r *Registry) Register(conn PlayerConnection) error {
	if strings.TrimSpace(conn.GateEndpoint) == "" {
		conn.GateEndpoint = "http://legacy-gate:8081"
	}
	if err := validate(conn); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	byKey := r.players[conn.PlayerID]
	for _, playerConnections := range r.players {
		for _, existing := range playerConnections {
			if existing.GateID == conn.GateID && existing.GateEndpoint != conn.GateEndpoint {
				return ErrConnectionMismatch
			}
		}
	}
	if byKey == nil {
		byKey = make(map[string]PlayerConnection)
		r.players[conn.PlayerID] = byKey
	}
	if existing, ok := byKey[conn.key()]; ok && existing.GateEndpoint != conn.GateEndpoint {
		return ErrConnectionMismatch
	}
	byKey[conn.key()] = conn.clone()
	return nil
}

// Refresh extends an existing lease when the full triplet matches.
func (r *Registry) Refresh(playerID uint64, gateID, connectionID string, expiresAt time.Time, gateEndpoints ...string) error {
	gateEndpoint := "http://legacy-gate:8081"
	if len(gateEndpoints) > 0 {
		gateEndpoint = gateEndpoints[0]
	}
	conn := PlayerConnection{
		PlayerID: playerID, GateID: gateID, GateEndpoint: gateEndpoint,
		ConnectionID: connectionID, ExpiresAt: expiresAt,
	}
	if err := validate(conn); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	byKey := r.players[playerID]
	if byKey == nil {
		return ErrConnectionMismatch
	}
	key := conn.key()
	existing, ok := byKey[key]
	if !ok || existing.GateEndpoint != conn.GateEndpoint {
		return ErrConnectionMismatch
	}
	byKey[key] = conn.clone()
	return nil
}

// Unregister removes one connection when the full triplet matches. A stale
// connection_id never deletes a newer registration.
func (r *Registry) Unregister(playerID uint64, gateID, connectionID string) {
	if playerID == 0 || strings.TrimSpace(gateID) == "" || strings.TrimSpace(connectionID) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	byKey := r.players[playerID]
	if byKey == nil {
		return
	}
	key := PlayerConnection{GateID: gateID, ConnectionID: connectionID}.key()
	delete(byKey, key)
	if len(byKey) == 0 {
		delete(r.players, playerID)
	}
}

// List returns every live connection for playerID, sorted by (gate_id, connection_id).
func (r *Registry) List(playerID uint64) []PlayerConnection {
	r.mu.Lock()
	defer r.mu.Unlock()
	byKey := r.players[playerID]
	if len(byKey) == 0 {
		return nil
	}
	out := make([]PlayerConnection, 0, len(byKey))
	for _, conn := range byKey {
		out = append(out, conn.clone())
	}
	sortConnections(out)
	return out
}

// ListAll returns a stable snapshot of every registered connection for
// periodic best-effort presence reconciliation.
func (r *Registry) ListAll() []PlayerConnection {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []PlayerConnection
	for _, byKey := range r.players {
		for _, conn := range byKey {
			out = append(out, conn.clone())
		}
	}
	sortConnections(out)
	return out
}

// Has reports whether playerID currently has at least one registered connection.
func (r *Registry) Has(playerID uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.players[playerID]) > 0
}

// EvictExpired removes leases that are not strictly after now.
func (r *Registry) EvictExpired(now time.Time) []PlayerConnection {
	r.mu.Lock()
	defer r.mu.Unlock()
	var removed []PlayerConnection
	for playerID, byKey := range r.players {
		for key, conn := range byKey {
			if !conn.ExpiresAt.After(now) {
				removed = append(removed, conn.clone())
				delete(byKey, key)
			}
		}
		if len(byKey) == 0 {
			delete(r.players, playerID)
		}
	}
	sortConnections(removed)
	return removed
}

// RemoveShard drops every connection whose player maps to shardID.
func (r *Registry) RemoveShard(shardID uint32) []PlayerConnection {
	r.mu.Lock()
	defer r.mu.Unlock()
	var removed []PlayerConnection
	for playerID, byKey := range r.players {
		if routing.ShardForPlayer(playerID) != shardID {
			continue
		}
		for _, conn := range byKey {
			removed = append(removed, conn.clone())
		}
		delete(r.players, playerID)
	}
	sortConnections(removed)
	return removed
}

func validate(conn PlayerConnection) error {
	if conn.PlayerID == 0 ||
		strings.TrimSpace(conn.GateID) == "" ||
		!ValidGateEndpoint(conn.GateEndpoint) ||
		strings.TrimSpace(conn.ConnectionID) == "" ||
		conn.ExpiresAt.IsZero() {
		return ErrInvalidConnection
	}
	return nil
}

// ValidGateEndpoint accepts loopback development targets and exact
// StatefulSet Pod DNS targets. It deliberately rejects the Gate ClusterIP.
func ValidGateEndpoint(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	host, port := parsed.Hostname(), parsed.Port()
	if port != "8081" {
		return false
	}
	if host == "127.0.0.1" || host == "localhost" || host == "legacy-gate" {
		return true
	}
	return regexp.MustCompile(`^gate-[0-9]+\.gate-headless\.[a-z0-9-]+\.svc\.cluster\.local$`).MatchString(host)
}

func sortConnections(conns []PlayerConnection) {
	sort.Slice(conns, func(i, j int) bool {
		if conns[i].GateID != conns[j].GateID {
			return conns[i].GateID < conns[j].GateID
		}
		return conns[i].ConnectionID < conns[j].ConnectionID
	})
}
