package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// FetchSnapshot loads the Coordinator's complete committed ShardMap.
func FetchSnapshot(
	ctx context.Context,
	client *http.Client,
	baseURL string,
) (Snapshot, error) {
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/internal/v1/routes"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Snapshot{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return Snapshot{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return Snapshot{}, fmt.Errorf("route snapshot returned %s", response.Status)
	}
	var body snapshotResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, (8<<20)+1))
	if err := decoder.Decode(&body); err != nil {
		return Snapshot{}, fmt.Errorf("decode route snapshot: %w", err)
	}
	mapVersion, err := parsePositiveVersion(body.MapVersion, "map_version")
	if err != nil {
		return Snapshot{}, err
	}
	term, err := parsePositiveVersion(body.CommittedTerm, "committed_term")
	if err != nil {
		return Snapshot{}, err
	}
	index, err := parsePositiveVersion(body.CommittedIndex, "committed_index")
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{
		ShardCount:                 body.ShardCount,
		HashAlgorithmVersion:       body.HashAlgorithmVersion,
		AssignmentAlgorithmVersion: body.AssignmentAlgorithmVersion,
		MapVersion:                 mapVersion,
		CommittedTerm:              term,
		CommittedIndex:             index,
		Entries:                    make([]RouteEntry, len(body.Entries)),
	}
	for entryIndex, encoded := range body.Entries {
		entry, entryErr := decodeRouteResponse(encoded)
		if entryErr != nil {
			return Snapshot{}, fmt.Errorf("decode route %d: %w", entryIndex, entryErr)
		}
		snapshot.Entries[entryIndex] = entry
	}
	if snapshot.ShardCount != ShardCount ||
		snapshot.HashAlgorithmVersion != HashAlgorithmVersion ||
		snapshot.AssignmentAlgorithmVersion != AssignmentAlgorithmVersion ||
		len(snapshot.Entries) != int(ShardCount) {
		return Snapshot{}, errors.New("route snapshot metadata is incompatible")
	}
	return snapshot, nil
}

func decodeRouteResponse(encoded routeResponse) (RouteEntry, error) {
	epoch, err := parsePositiveVersion(encoded.OwnerEpoch, "owner_epoch")
	if err != nil {
		return RouteEntry{}, err
	}
	routeVersion, err := parsePositiveVersion(encoded.RouteVersion, "route_version")
	if err != nil {
		return RouteEntry{}, err
	}
	leaseTerm, err := parsePositiveVersion(encoded.LeaseTerm, "lease_term")
	if err != nil {
		return RouteEntry{}, err
	}
	if encoded.ShardID >= ShardCount || encoded.OwnerZoneID == "" ||
		encoded.OwnerEndpoint == "" || encoded.LeaseExpiresAtMS <= 0 {
		return RouteEntry{}, errors.New("route identity is invalid")
	}
	return RouteEntry{
		ShardID:             encoded.ShardID,
		OwnerZoneID:         encoded.OwnerZoneID,
		OwnerEndpoint:       encoded.OwnerEndpoint,
		OwnerEpoch:          epoch,
		RouteVersion:        routeVersion,
		State:               encoded.State,
		LeaseTerm:           leaseTerm,
		LeaseID:             encoded.LeaseID,
		LeaseExpiresAt:      time.UnixMilli(encoded.LeaseExpiresAtMS).UTC(),
		PreviousOwnerZoneID: encoded.PreviousOwnerZoneID,
		TransitionID:        encoded.TransitionID,
		UpdatedAt:           time.UnixMilli(encoded.UpdatedAtMS).UTC(),
	}, nil
}

func parsePositiveVersion(raw, field string) (uint64, error) {
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("%s is invalid", field)
	}
	return value, nil
}
