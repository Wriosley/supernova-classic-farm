package membership

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	coordinatorv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator"
)

type sequenceProber struct {
	mu      sync.Mutex
	results []ProbeResult
	calls   int
}

func (prober *sequenceProber) Probe(context.Context, string) ProbeResult {
	prober.mu.Lock()
	defer prober.mu.Unlock()
	index := prober.calls
	if index >= len(prober.results) {
		index = len(prober.results) - 1
	}
	prober.calls++
	return prober.results[index]
}

type recordingPublisher struct {
	batches chan *coordinatorv1.AvailabilityBatch
}

func (publisher *recordingPublisher) PublishAvailability(batch *coordinatorv1.AvailabilityBatch) error {
	publisher.batches <- batch
	return nil
}

func TestControllerRecoversAfterTwoFailures(t *testing.T) {
	logical := "d859cea1-ac5b-5524-bffa-4e542301cd95"
	prober := &sequenceProber{results: []ProbeResult{{Err: errors.New("down")}, {Err: errors.New("down")}, {LogicalZoneID: logical, IncarnationID: "9e398c48-4c67-41e8-8655-d33167d42fb4", Endpoint: "http://10.0.0.8:8082", Live: true}}}
	publisher := &recordingPublisher{batches: make(chan *coordinatorv1.AvailabilityBatch, 8)}
	controller, err := NewController(nil, NewRegistry(time.Now), prober, publisher, ControllerConfig{ProbeInterval: 10 * time.Millisecond, ProbeTimeout: time.Second, FailureThreshold: 3, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = controller.Run(ctx) }()
	controller.UpsertEndpoint(testEndpointObservation())
	waitAvailability(t, publisher.batches, coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_SUSPECT)
	waitAvailability(t, publisher.batches, coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_HEALTHY)
}

func TestControllerThirdFailureAndTerminalPodBecomeDead(t *testing.T) {
	prober := &sequenceProber{results: []ProbeResult{{Err: errors.New("down")}}}
	publisher := &recordingPublisher{batches: make(chan *coordinatorv1.AvailabilityBatch, 8)}
	controller, err := NewController(nil, NewRegistry(time.Now), prober, publisher, ControllerConfig{ProbeInterval: 10 * time.Millisecond, ProbeTimeout: time.Second, FailureThreshold: 3, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = controller.Run(ctx) }()
	observation := testEndpointObservation()
	controller.UpsertEndpoint(observation)
	waitAvailability(t, publisher.batches, coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_SUSPECT)
	waitAvailability(t, publisher.batches, coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_DEAD)

	// A terminal observation is immediate and does not need another HTTP probe.
	logical := "d859cea1-ac5b-5524-bffa-4e542301cd95"
	prober2 := &sequenceProber{results: []ProbeResult{{LogicalZoneID: logical, IncarnationID: "9e398c48-4c67-41e8-8655-d33167d42fb4", Endpoint: observation.Endpoint, Live: true}}}
	publisher2 := &recordingPublisher{batches: make(chan *coordinatorv1.AvailabilityBatch, 8)}
	controller2, _ := NewController(nil, NewRegistry(time.Now), prober2, publisher2, ControllerConfig{ProbeInterval: time.Hour, ProbeTimeout: time.Second, FailureThreshold: 3, Workers: 1})
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() { _ = controller2.Run(ctx2) }()
	controller2.UpsertEndpoint(observation)
	waitAvailability(t, publisher2.batches, coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_HEALTHY)
	observation.PodPhase = "Failed"
	controller2.UpsertEndpoint(observation)
	waitAvailability(t, publisher2.batches, coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_DEAD)
}

func testEndpointObservation() EndpointObservation {
	return EndpointObservation{Namespace: "classic-farm", PodName: "zone-pool-0", PodUID: "uid", ResourceVersion: "10", ClusterID: "classic-farm-local", StatefulSetName: "zone-pool", Ordinal: 0, Endpoint: "http://10.0.0.8:8082", EndpointReady: true, PodPhase: "Running"}
}
func waitAvailability(t *testing.T, batches <-chan *coordinatorv1.AvailabilityBatch, want coordinatorv1.ZoneAvailability) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case batch := <-batches:
			if len(batch.Zones) == 1 && batch.Zones[0].Availability == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", want)
		}
	}
}
