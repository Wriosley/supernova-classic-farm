package routing

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// Clock supplies Coordinator time for lease checks.
type Clock func() time.Time

// NewHTTPHandler exposes the loopback route lookup API.
func NewHTTPHandler(routes *Map, clock Clock) http.Handler {
	return newHTTPHandler(routes, nil, clock)
}

// NewHTTPHandlerWithRuntimeLeases exposes durable Current with process-local
// effective expiry while preserving every durable route identity and version.
func NewHTTPHandlerWithRuntimeLeases(routes *Map, leases *RuntimeLeaseOverlay, clock Clock) http.Handler {
	return newHTTPHandler(routes, leases, clock)
}

func newHTTPHandler(routes *Map, leases *RuntimeLeaseOverlay, clock Clock) http.Handler {
	if clock == nil {
		clock = time.Now
	}
	mux := http.NewServeMux()
	var snapshotLookups atomic.Uint64
	var shardLookups atomic.Uint64
	mux.HandleFunc("GET /internal/v1/routes", func(w http.ResponseWriter, r *http.Request) {
		snapshotLookups.Add(1)
		now := clock().UTC()
		snapshot := routes.Snapshot()
		response := snapshotResponse{
			ShardCount:                 snapshot.ShardCount,
			HashAlgorithmVersion:       snapshot.HashAlgorithmVersion,
			AssignmentAlgorithmVersion: snapshot.AssignmentAlgorithmVersion,
			MapVersion:                 strconv.FormatUint(snapshot.MapVersion, 10),
			CommittedTerm:              strconv.FormatUint(snapshot.CommittedTerm, 10),
			CommittedIndex:             strconv.FormatUint(snapshot.CommittedIndex, 10),
			Entries:                    make([]routeResponse, len(snapshot.Entries)),
		}
		for index, entry := range snapshot.Entries {
			routable := entry.State == RouteStateActive && now.Before(entry.LeaseExpiresAt)
			if leases != nil {
				effective, err := leases.Effective(entry, now)
				if err == nil {
					entry = effective
					routable = true
				} else {
					routable = false
				}
			}
			response.Entries[index] = routeResponseFrom(entry, snapshot.MapVersion, routable)
		}
		writeJSON(w, http.StatusOK, response)
	})
	mux.HandleFunc("GET /internal/v1/routes/{shard_id}", func(w http.ResponseWriter, r *http.Request) {
		shardLookups.Add(1)
		shardValue, err := strconv.ParseUint(r.PathValue("shard_id"), 10, 32)
		if err != nil || shardValue >= uint64(ShardCount) {
			writeJSON(w, http.StatusBadRequest, errorResponse{
				Code:    "INVALID_SHARD_ID",
				Message: "shard_id must be a decimal integer in [0,4096)",
			})
			return
		}
		shardID := uint32(shardValue)
		now := clock().UTC()
		entry, mapVersionErr := routes.Entry(shardID)
		mapVersion := routes.Snapshot().MapVersion
		err = mapVersionErr
		if err == nil && leases != nil {
			var effective RouteEntry
			effective, err = leases.Effective(entry, now)
			if err == nil {
				entry = effective
			}
		} else if err == nil {
			entry, mapVersion, err = routes.RouteWithMapVersion(shardID, now)
		}
		if err == nil {
			writeJSON(w, http.StatusOK, routeResponseFrom(entry, mapVersion, true))
			return
		}

		if errors.Is(err, ErrRuntimeLeaseUnavailable) {
			response := routeResponseFrom(entry, mapVersion, false)
			response.Error = &errorResponse{Code: "NOT_OWNER", Message: "runtime lease is unavailable"}
			writeJSON(w, http.StatusConflict, response)
			return
		}
		var notOwner *NotOwnerError
		if errors.As(err, &notOwner) {
			response := routeResponseFrom(notOwner.Current, mapVersion, false)
			response.Error = &errorResponse{
				Code:    notOwner.Code,
				Message: notOwner.Reason,
			}
			writeJSON(w, http.StatusConflict, response)
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{
			Code:    "INTERNAL",
			Message: "route lookup failed",
		})
	})
	mux.HandleFunc("GET /internal/v1/debug/route-lookups", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, routeLookupStats{
			Snapshot: snapshotLookups.Load(),
			Shard:    shardLookups.Load(),
		})
	})
	return mux
}

type routeResponse struct {
	ShardID             uint32         `json:"shard_id"`
	OwnerZoneID         string         `json:"owner_zone_id,omitempty"`
	OwnerEndpoint       string         `json:"owner_endpoint,omitempty"`
	OwnerEpoch          string         `json:"owner_epoch"`
	RouteVersion        string         `json:"route_version"`
	MapVersion          string         `json:"map_version"`
	State               RouteState     `json:"state"`
	LeaseTerm           string         `json:"lease_term"`
	LeaseID             string         `json:"lease_id,omitempty"`
	LeaseExpiresAtMS    int64          `json:"lease_expires_at_ms"`
	PreviousOwnerZoneID string         `json:"previous_owner_zone_id,omitempty"`
	TransitionID        string         `json:"transition_id,omitempty"`
	UpdatedAtMS         int64          `json:"updated_at_ms"`
	Routable            bool           `json:"routable"`
	Error               *errorResponse `json:"error,omitempty"`
}

type snapshotResponse struct {
	ShardCount                 uint32          `json:"shard_count"`
	HashAlgorithmVersion       uint32          `json:"hash_algorithm_version"`
	AssignmentAlgorithmVersion uint32          `json:"assignment_algorithm_version"`
	MapVersion                 string          `json:"map_version"`
	CommittedTerm              string          `json:"committed_term"`
	CommittedIndex             string          `json:"committed_index"`
	Entries                    []routeResponse `json:"entries"`
}

type routeLookupStats struct {
	Snapshot uint64 `json:"snapshot"`
	Shard    uint64 `json:"shard"`
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func routeResponseFrom(entry RouteEntry, mapVersion uint64, routable bool) routeResponse {
	return routeResponse{
		ShardID:             entry.ShardID,
		OwnerZoneID:         entry.OwnerZoneID,
		OwnerEndpoint:       entry.OwnerEndpoint,
		OwnerEpoch:          strconv.FormatUint(entry.OwnerEpoch, 10),
		RouteVersion:        strconv.FormatUint(entry.RouteVersion, 10),
		MapVersion:          strconv.FormatUint(mapVersion, 10),
		State:               entry.State,
		LeaseTerm:           strconv.FormatUint(entry.LeaseTerm, 10),
		LeaseID:             entry.LeaseID,
		LeaseExpiresAtMS:    entry.LeaseExpiresAt.UnixMilli(),
		PreviousOwnerZoneID: entry.PreviousOwnerZoneID,
		TransitionID:        entry.TransitionID,
		UpdatedAtMS:         entry.UpdatedAt.UnixMilli(),
		Routable:            routable,
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
