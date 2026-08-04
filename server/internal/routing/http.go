package routing

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
)

// Clock supplies Coordinator time for lease checks.
type Clock func() time.Time

// NewHTTPHandler exposes the loopback route lookup API.
func NewHTTPHandler(routes *Map, clock Clock) http.Handler {
	if clock == nil {
		clock = time.Now
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/v1/routes/{shard_id}", func(w http.ResponseWriter, r *http.Request) {
		shardValue, err := strconv.ParseUint(r.PathValue("shard_id"), 10, 32)
		if err != nil || shardValue >= uint64(ShardCount) {
			writeJSON(w, http.StatusBadRequest, errorResponse{
				Code:    "INVALID_SHARD_ID",
				Message: "shard_id must be a decimal integer in [0,4096)",
			})
			return
		}
		shardID := uint32(shardValue)
		entry, err := routes.Route(shardID, clock())
		if err == nil {
			writeJSON(w, http.StatusOK, routeResponseFrom(entry, true))
			return
		}

		var notOwner *NotOwnerError
		if errors.As(err, &notOwner) {
			response := routeResponseFrom(notOwner.Current, false)
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
	return mux
}

type routeResponse struct {
	ShardID             uint32         `json:"shard_id"`
	OwnerZoneID          string         `json:"owner_zone_id,omitempty"`
	OwnerEndpoint        string         `json:"owner_endpoint,omitempty"`
	OwnerEpoch           string         `json:"owner_epoch"`
	RouteVersion         string         `json:"route_version"`
	State                RouteState     `json:"state"`
	LeaseTerm            string         `json:"lease_term"`
	LeaseID              string         `json:"lease_id,omitempty"`
	LeaseExpiresAtMS     int64          `json:"lease_expires_at_ms"`
	PreviousOwnerZoneID  string         `json:"previous_owner_zone_id,omitempty"`
	TransitionID         string         `json:"transition_id,omitempty"`
	UpdatedAtMS          int64          `json:"updated_at_ms"`
	Routable             bool           `json:"routable"`
	Error                *errorResponse `json:"error,omitempty"`
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func routeResponseFrom(entry RouteEntry, routable bool) routeResponse {
	return routeResponse{
		ShardID:            entry.ShardID,
		OwnerZoneID:         entry.OwnerZoneID,
		OwnerEndpoint:       entry.OwnerEndpoint,
		OwnerEpoch:          strconv.FormatUint(entry.OwnerEpoch, 10),
		RouteVersion:        strconv.FormatUint(entry.RouteVersion, 10),
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
