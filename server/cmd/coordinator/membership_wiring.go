package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	coordinatorv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator"
	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/membership"
	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/publisher"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type membershipConfig struct {
	Source, ClusterID, Namespace, ServiceName, HeadlessServiceName string
	ProbeInterval, ProbeTimeout                                    time.Duration
	FailureThreshold, Workers                                      int
}

type membershipRuntime struct {
	ready    <-chan struct{}
	registry *membership.Registry
}

func membershipConfigFromEnvironment() (membershipConfig, error) {
	config := membershipConfig{Source: environmentOr("COORDINATOR_MEMBERSHIP_SOURCE", "static"), ClusterID: strings.TrimSpace(os.Getenv("CLUSTER_ID")), Namespace: strings.TrimSpace(os.Getenv("POD_NAMESPACE")), ServiceName: environmentOr("ZONE_DISCOVERY_SERVICE", "zone-discovery"), HeadlessServiceName: environmentOr("ZONE_HEADLESS_SERVICE", "zone-headless")}
	if config.Source != "static" && config.Source != "kubernetes" {
		return config, errors.New("COORDINATOR_MEMBERSHIP_SOURCE must be static or kubernetes")
	}
	var err error
	config.ProbeInterval, err = time.ParseDuration(environmentOr("ZONE_LIVE_PROBE_INTERVAL", "10s"))
	if err != nil || config.ProbeInterval <= 0 {
		return config, errors.New("invalid ZONE_LIVE_PROBE_INTERVAL")
	}
	config.ProbeTimeout, err = time.ParseDuration(environmentOr("ZONE_LIVE_PROBE_TIMEOUT", "2s"))
	if err != nil || config.ProbeTimeout <= 0 {
		return config, errors.New("invalid ZONE_LIVE_PROBE_TIMEOUT")
	}
	config.FailureThreshold, err = strconv.Atoi(environmentOr("ZONE_LIVE_FAILURE_THRESHOLD", "3"))
	if err != nil || config.FailureThreshold <= 0 {
		return config, errors.New("invalid ZONE_LIVE_FAILURE_THRESHOLD")
	}
	config.Workers, err = strconv.Atoi(environmentOr("ZONE_PROBE_WORKERS", "8"))
	if err != nil || config.Workers <= 0 {
		return config, errors.New("invalid ZONE_PROBE_WORKERS")
	}
	if config.Source == "kubernetes" && (config.ClusterID == "" || config.Namespace == "" || config.ServiceName == "" || config.HeadlessServiceName == "") {
		return config, errors.New("Kubernetes membership requires cluster, namespace and discovery services")
	}
	return config, nil
}

func startMembership(ctx context.Context, routePublisher *publisher.Publisher, zones []routing.ZoneCandidate, config membershipConfig) (*membershipRuntime, error) {
	registry := membership.NewRegistry(time.Now)
	if config.Source == "static" {
		if err := seedStaticMembership(registry, zones, time.Now().UTC()); err != nil {
			return nil, err
		}
		staticSnapshot := registry.Snapshot()
		entries := make([]*coordinatorv1.ZoneAvailabilityEntry, 0, len(zones))
		for _, zone := range zones {
			entries = append(entries, &coordinatorv1.ZoneAvailabilityEntry{LogicalZoneId: zone.ZoneID, Availability: coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_HEALTHY, IncarnationId: "static-" + zone.ZoneID, ObservedAtMs: time.Now().UTC().UnixMilli()})
		}
		if err := routePublisher.PublishAvailability(&coordinatorv1.AvailabilityBatch{AvailabilityVersion: staticSnapshot.AvailabilityVersion, Zones: entries}); err != nil {
			return nil, err
		}
		ready := make(chan struct{})
		close(ready)
		return &membershipRuntime{ready: ready, registry: registry}, nil
	}
	inCluster, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("create in-cluster Kubernetes config: %w", err)
	}
	client, err := kubernetes.NewForConfig(inCluster)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
	controller, err := membership.NewController(nil, registry, membership.NewHTTPProber(config.ProbeTimeout), routePublisher, membership.ControllerConfig{ProbeInterval: config.ProbeInterval, ProbeTimeout: config.ProbeTimeout, FailureThreshold: config.FailureThreshold, Workers: config.Workers})
	if err != nil {
		return nil, err
	}
	source, err := membership.NewKubernetesSource(client, config.Namespace, config.ServiceName, config.HeadlessServiceName, config.ClusterID, controller)
	if err != nil {
		return nil, err
	}
	if err := controller.SetSource(source); err != nil {
		return nil, err
	}
	go func() {
		if runErr := controller.Run(ctx); runErr != nil && ctx.Err() == nil {
			slog.Error("membership controller stopped", "error", runErr)
		}
	}()
	return &membershipRuntime{ready: source.Ready(), registry: registry}, nil
}

func seedStaticMembership(registry *membership.Registry, zones []routing.ZoneCandidate, observedAt time.Time) error {
	for index, zone := range zones {
		identity := "static-" + zone.ZoneID
		if _, _, err := registry.Apply(membership.Observation{
			LogicalZoneID: zone.ZoneID, IncarnationID: identity, Endpoint: zone.Endpoint,
			PodName: identity, PodUID: identity, ResourceVersion: strconv.Itoa(index + 1),
			State: membership.StateHealthy, ObservedAt: observedAt.UTC(),
		}); err != nil {
			return fmt.Errorf("seed static membership %q: %w", zone.ZoneID, err)
		}
	}
	return nil
}

func (runtime *membershipRuntime) Check(context.Context) error {
	select {
	case <-runtime.ready:
		return nil
	default:
		return errors.New("membership source cache is not ready")
	}
}
