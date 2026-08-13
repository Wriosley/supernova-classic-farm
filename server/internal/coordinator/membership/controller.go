package membership

import (
	"context"
	"errors"
	"sync"
	"time"

	coordinatorv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator"
	"github.com/Wriosley/supernova-classic-farm/server/internal/zoneidentity"
)

type Source interface{ Run(context.Context) error }
type AvailabilityPublisher interface {
	PublishAvailability(*coordinatorv1.AvailabilityBatch) error
}

type ControllerConfig struct {
	ProbeInterval    time.Duration
	ProbeTimeout     time.Duration
	FailureThreshold int
	Workers          int
}

type Controller struct {
	source    Source
	registry  *Registry
	prober    Prober
	publisher AvailabilityPublisher
	cfg       ControllerConfig
	events    chan controllerEvent
	jobs      chan EndpointObservation
	results   chan probeObservation
	runOnce   sync.Once
}

func (controller *Controller) SetSource(source Source) error {
	if source == nil {
		return errors.New("membership source is required")
	}
	if controller.source != nil {
		return errors.New("membership source is already configured")
	}
	controller.source = source
	return nil
}

type controllerEvent struct {
	endpoint *EndpointObservation
	deletion *podDeletion
}
type podDeletion struct{ namespace, name, uid, resourceVersion string }
type probeObservation struct {
	endpoint EndpointObservation
	result   ProbeResult
}

func NewController(source Source, registry *Registry, prober Prober, publisher AvailabilityPublisher, cfg ControllerConfig) (*Controller, error) {
	if registry == nil || prober == nil || publisher == nil || cfg.ProbeInterval <= 0 || cfg.ProbeTimeout <= 0 || cfg.FailureThreshold <= 0 || cfg.Workers <= 0 {
		return nil, errors.New("complete membership controller configuration is required")
	}
	return &Controller{source: source, registry: registry, prober: prober, publisher: publisher, cfg: cfg, events: make(chan controllerEvent, 256), jobs: make(chan EndpointObservation, 256), results: make(chan probeObservation, 256)}, nil
}

func (controller *Controller) UpsertEndpoint(observation EndpointObservation) {
	copy := observation
	controller.events <- controllerEvent{endpoint: &copy}
}

func (controller *Controller) DeletePod(namespace, name, uid, resourceVersion string) {
	controller.events <- controllerEvent{deletion: &podDeletion{namespace, name, uid, resourceVersion}}
}

func (controller *Controller) Run(ctx context.Context) error {
	started := false
	controller.runOnce.Do(func() { started = true })
	if !started {
		return errors.New("membership controller already ran")
	}
	errorsChannel := make(chan error, 1)
	if controller.source != nil {
		go func() {
			if err := controller.source.Run(ctx); err != nil && ctx.Err() == nil {
				errorsChannel <- err
			}
		}()
	}
	for worker := 0; worker < controller.cfg.Workers; worker++ {
		go controller.worker(ctx)
	}
	ticker := time.NewTicker(controller.cfg.ProbeInterval)
	defer ticker.Stop()
	endpoints := make(map[string]EndpointObservation)
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errorsChannel:
			return err
		case event := <-controller.events:
			if event.endpoint != nil {
				observation := *event.endpoint
				endpoints[observation.PodUID] = observation
				if terminalEndpoint(observation) {
					if err := controller.applyFailure(observation, true); err != nil {
						return err
					}
					continue
				}
				if !observation.EndpointReady {
					if err := controller.applyFailure(observation, false); err != nil {
						return err
					}
				}
				controller.schedule(ctx, observation)
			} else if event.deletion != nil {
				if observation, ok := endpoints[event.deletion.uid]; ok {
					delete(endpoints, event.deletion.uid)
					observation.Deleting = true
					if err := controller.applyFailure(observation, true); err != nil {
						return err
					}
				}
			}
		case result := <-controller.results:
			if latest, ok := endpoints[result.endpoint.PodUID]; !ok || latest.ResourceVersion != result.endpoint.ResourceVersion || latest.Endpoint != result.endpoint.Endpoint {
				continue
			}
			if err := controller.applyProbe(result.endpoint, result.result); err != nil {
				return err
			}
		case <-ticker.C:
			for _, observation := range endpoints {
				if !terminalEndpoint(observation) {
					controller.schedule(ctx, observation)
				}
			}
		}
	}
}

func (controller *Controller) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case endpoint := <-controller.jobs:
			probeCtx, cancel := context.WithTimeout(ctx, controller.cfg.ProbeTimeout)
			result := controller.prober.Probe(probeCtx, endpoint.Endpoint)
			cancel()
			select {
			case controller.results <- probeObservation{endpoint, result}:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (controller *Controller) schedule(ctx context.Context, observation EndpointObservation) {
	select {
	case controller.jobs <- observation:
	case <-ctx.Done():
	}
}

func (controller *Controller) applyProbe(endpoint EndpointObservation, result ProbeResult) error {
	expected, err := zoneidentity.DeriveLogicalID(endpoint.ClusterID, endpoint.Namespace, endpoint.StatefulSetName, endpoint.Ordinal)
	if err != nil || result.Err != nil || !result.Live || result.LogicalZoneID != expected || result.Endpoint != endpoint.Endpoint {
		return controller.applyFailure(endpoint, false)
	}
	observedAt := result.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	return controller.apply(Member{LogicalZoneID: expected, IncarnationID: result.IncarnationID, Endpoint: endpoint.Endpoint, Namespace: endpoint.Namespace, PodName: endpoint.PodName, PodUID: endpoint.PodUID, ResourceVersion: endpoint.ResourceVersion, State: StateHealthy, ObservedAt: observedAt})
}

func (controller *Controller) applyFailure(endpoint EndpointObservation, terminal bool) error {
	logicalID, err := zoneidentity.DeriveLogicalID(endpoint.ClusterID, endpoint.Namespace, endpoint.StatefulSetName, endpoint.Ordinal)
	if err != nil {
		return err
	}
	member, exists := memberByLogicalID(controller.registry.Snapshot(), logicalID)
	if !exists {
		member = Member{LogicalZoneID: logicalID, IncarnationID: "unverified-" + endpoint.PodUID}
	}
	member.Endpoint, member.Namespace, member.PodName, member.PodUID, member.ResourceVersion = endpoint.Endpoint, endpoint.Namespace, endpoint.PodName, endpoint.PodUID, endpoint.ResourceVersion
	member.ObservedAt = time.Now().UTC()
	member.ConsecutiveFailures++
	member.State = StateSuspect
	if terminal || member.ConsecutiveFailures >= controller.cfg.FailureThreshold {
		member.State = StateDead
	}
	return controller.apply(member)
}

func (controller *Controller) apply(member Member) error {
	snapshot, changed, err := controller.registry.Apply(Observation(member))
	if errors.Is(err, ErrStaleObservation) || errors.Is(err, ErrIdentityConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	previous := uint64(0)
	if snapshot.AvailabilityVersion > 0 {
		previous = snapshot.AvailabilityVersion - 1
	}
	batch := &coordinatorv1.AvailabilityBatch{PreviousAvailabilityVersion: previous, AvailabilityVersion: snapshot.AvailabilityVersion, Zones: make([]*coordinatorv1.ZoneAvailabilityEntry, 0, len(snapshot.Members))}
	for _, member := range snapshot.Members {
		batch.Zones = append(batch.Zones, &coordinatorv1.ZoneAvailabilityEntry{LogicalZoneId: member.LogicalZoneID, Availability: protobufAvailability(member.State), IncarnationId: member.IncarnationID, ObservedAtMs: member.ObservedAt.UnixMilli()})
	}
	return controller.publisher.PublishAvailability(batch)
}

func memberByLogicalID(snapshot Snapshot, logicalID string) (Member, bool) {
	for _, member := range snapshot.Members {
		if member.LogicalZoneID == logicalID {
			return member, true
		}
	}
	return Member{}, false
}
func terminalEndpoint(endpoint EndpointObservation) bool {
	return endpoint.Deleting || endpoint.PodPhase == "Failed" || endpoint.PodPhase == "Succeeded"
}
func protobufAvailability(state State) coordinatorv1.ZoneAvailability {
	switch state {
	case StateHealthy:
		return coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_HEALTHY
	case StateSuspect:
		return coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_SUSPECT
	case StateDead:
		return coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_DEAD
	case StateDraining:
		return coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_DRAINING
	default:
		return coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_UNSPECIFIED
	}
}
