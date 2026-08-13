package placement

import (
	"reflect"
	"testing"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestComputeMatchesFrozenEightZoneVectors(t *testing.T) {
	candidates := eightCandidates()

	desired, err := Compute(routing.ShardCount, routing.AssignmentAlgorithmVersion, candidates)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(desired) != int(routing.ShardCount) {
		t.Fatalf("entry count = %d, want %d", len(desired), routing.ShardCount)
	}

	wantOwners := map[uint32]string{
		0: "zone-2", 1: "zone-6", 2: "zone-4", 3: "zone-2",
		17: "zone-3", 255: "zone-3", 1024: "zone-4",
		2048: "zone-4", 4095: "zone-2",
	}
	for shardID, wantOwner := range wantOwners {
		entry := desired[shardID]
		if entry.ShardID != shardID || entry.OwnerZoneID != wantOwner ||
			entry.OwnerEndpoint != "http://"+wantOwner+":8082" {
			t.Errorf("shard %d = %+v, want owner %s", shardID, entry, wantOwner)
		}
	}

	gotCounts := make(map[string]int, len(candidates))
	for _, entry := range desired {
		gotCounts[entry.OwnerZoneID]++
	}
	wantCounts := map[string]int{
		"zone-0": 479, "zone-1": 520, "zone-2": 517, "zone-3": 556,
		"zone-4": 517, "zone-5": 515, "zone-6": 505, "zone-7": 487,
	}
	if !reflect.DeepEqual(gotCounts, wantCounts) {
		t.Fatalf("owner counts = %#v, want %#v", gotCounts, wantCounts)
	}
}

func TestComputeIsIndependentOfInputOrderAndExactDuplicates(t *testing.T) {
	ordered := eightCandidates()
	shuffledWithDuplicate := []Candidate{
		ordered[5], ordered[1], ordered[7], ordered[3], ordered[5],
		ordered[0], ordered[6], ordered[2], ordered[4],
	}

	want, err := Compute(routing.ShardCount, routing.AssignmentAlgorithmVersion, ordered)
	if err != nil {
		t.Fatalf("ordered Compute: %v", err)
	}
	got, err := Compute(routing.ShardCount, routing.AssignmentAlgorithmVersion, shuffledWithDuplicate)
	if err != nil {
		t.Fatalf("shuffled Compute: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("placement changed with candidate order or exact duplicate")
	}

	restarted, err := Compute(routing.ShardCount, routing.AssignmentAlgorithmVersion, ordered)
	if err != nil {
		t.Fatalf("restart Compute: %v", err)
	}
	if !reflect.DeepEqual(restarted, want) {
		t.Fatal("placement changed across repeated calculation")
	}
}

func TestComputeAddingNinthZoneHasMinimalMovement(t *testing.T) {
	base, err := Compute(routing.ShardCount, routing.AssignmentAlgorithmVersion, eightCandidates())
	if err != nil {
		t.Fatalf("base Compute: %v", err)
	}
	expandedCandidates := append(eightCandidates(), Candidate{
		LogicalZoneID: "zone-8", Endpoint: "http://zone-8:8082",
	})
	expanded, err := Compute(routing.ShardCount, routing.AssignmentAlgorithmVersion, expandedCandidates)
	if err != nil {
		t.Fatalf("expanded Compute: %v", err)
	}

	changed := 0
	for shardID := range base {
		if base[shardID].OwnerZoneID == expanded[shardID].OwnerZoneID {
			continue
		}
		changed++
		if expanded[shardID].OwnerZoneID != "zone-8" {
			t.Fatalf("shard %d moved old-to-old: %s -> %s", shardID,
				base[shardID].OwnerZoneID, expanded[shardID].OwnerZoneID)
		}
	}
	if changed != 442 {
		t.Fatalf("changed shards = %d, want 442", changed)
	}
}

func TestComputeRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name              string
		shardCount        uint32
		assignmentVersion uint32
		candidates        []Candidate
	}{
		{name: "empty candidates", shardCount: routing.ShardCount, assignmentVersion: routing.AssignmentAlgorithmVersion},
		{name: "unsupported shard count", shardCount: routing.ShardCount - 1, assignmentVersion: routing.AssignmentAlgorithmVersion, candidates: eightCandidates()},
		{name: "unsupported assignment version", shardCount: routing.ShardCount, assignmentVersion: routing.AssignmentAlgorithmVersion + 1, candidates: eightCandidates()},
		{name: "empty logical ID", shardCount: routing.ShardCount, assignmentVersion: routing.AssignmentAlgorithmVersion, candidates: []Candidate{{Endpoint: "http://zone:8082"}}},
		{name: "empty endpoint", shardCount: routing.ShardCount, assignmentVersion: routing.AssignmentAlgorithmVersion, candidates: []Candidate{{LogicalZoneID: "zone"}}},
		{name: "conflicting duplicate", shardCount: routing.ShardCount, assignmentVersion: routing.AssignmentAlgorithmVersion, candidates: []Candidate{
			{LogicalZoneID: "zone", Endpoint: "http://one:8082"},
			{LogicalZoneID: "zone", Endpoint: "http://two:8082"},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Compute(test.shardCount, test.assignmentVersion, test.candidates); err == nil {
				t.Fatal("Compute succeeded, want error")
			}
		})
	}
}

func eightCandidates() []Candidate {
	return []Candidate{
		{LogicalZoneID: "zone-0", Endpoint: "http://zone-0:8082"},
		{LogicalZoneID: "zone-1", Endpoint: "http://zone-1:8082"},
		{LogicalZoneID: "zone-2", Endpoint: "http://zone-2:8082"},
		{LogicalZoneID: "zone-3", Endpoint: "http://zone-3:8082"},
		{LogicalZoneID: "zone-4", Endpoint: "http://zone-4:8082"},
		{LogicalZoneID: "zone-5", Endpoint: "http://zone-5:8082"},
		{LogicalZoneID: "zone-6", Endpoint: "http://zone-6:8082"},
		{LogicalZoneID: "zone-7", Endpoint: "http://zone-7:8082"},
	}
}
