package publisher

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"time"

	coordinatorv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GRPCConfig struct {
	PingInterval time.Duration
	AckTimeout   time.Duration
	Now          func() time.Time
}
type GRPCServer struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer
	source    SnapshotSource
	publisher *Publisher
	cfg       GRPCConfig
	pingID    atomic.Uint64
}

func NewGRPCServer(source SnapshotSource, publisher *Publisher, cfg GRPCConfig) (*GRPCServer, error) {
	if source == nil || publisher == nil {
		return nil, errors.New("snapshot source and publisher are required")
	}
	if cfg.PingInterval == 0 {
		cfg.PingInterval = 30 * time.Second
	}
	if cfg.AckTimeout == 0 {
		cfg.AckTimeout = 90 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.PingInterval <= 0 || cfg.AckTimeout <= 0 {
		return nil, errors.New("watch durations must be positive")
	}
	return &GRPCServer{source: source, publisher: publisher, cfg: cfg}, nil
}

func (s *GRPCServer) GetRouteSnapshot(context.Context, *coordinatorv1.GetRouteSnapshotRequest) (*coordinatorv1.GetRouteSnapshotResponse, error) {
	encoded, err := SnapshotProto(s.source.Snapshot())
	if err != nil {
		return nil, status.Error(codes.Internal, "encode route snapshot")
	}
	return &coordinatorv1.GetRouteSnapshotResponse{Snapshot: encoded}, nil
}
func (s *GRPCServer) GetShardRoute(_ context.Context, request *coordinatorv1.GetShardRouteRequest) (*coordinatorv1.GetShardRouteResponse, error) {
	if request.GetShardId() >= routing.ShardCount {
		return nil, status.Error(codes.InvalidArgument, "shard ID is outside route set")
	}
	snapshot := s.source.Snapshot()
	encoded, err := RouteProto(snapshot.Entries[request.GetShardId()])
	if err != nil {
		return nil, status.Error(codes.Internal, "encode shard route")
	}
	return &coordinatorv1.GetShardRouteResponse{Route: encoded, MapVersion: snapshot.MapVersion}, nil
}
func (s *GRPCServer) ReportZoneFailure(context.Context, *coordinatorv1.ReportZoneFailureRequest) (*coordinatorv1.ReportZoneFailureResponse, error) {
	return nil, status.Error(codes.Unimplemented, "zone failure reporting starts in phase 07")
}

func (s *GRPCServer) WatchRoutes(stream coordinatorv1.CoordinatorService_WatchRoutesServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	subscribe := first.GetSubscribe()
	if subscribe == nil {
		return status.Error(codes.FailedPrecondition, "first watch message must be Subscribe")
	}
	session, err := s.publisher.Register(subscribe.SubscriberId, subscribe.Kind, subscribe.LastMapVersion, subscribe.LastAvailabilityVersion)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	defer s.publisher.Unregister(session)
	type received struct {
		request *coordinatorv1.WatchRoutesRequest
		err     error
	}
	incoming := make(chan received, 1)
	go func() {
		for {
			request, recvErr := stream.Recv()
			select {
			case incoming <- received{request, recvErr}:
			case <-stream.Context().Done():
				return
			}
			if recvErr != nil {
				return
			}
		}
	}()
	pingTicker := time.NewTicker(s.cfg.PingInterval)
	defer pingTicker.Stop()
	timeoutTicker := time.NewTicker(minDuration(s.cfg.AckTimeout/4, time.Second))
	defer timeoutTicker.Stop()
	lastActivity := s.cfg.Now()
	var lastSent, acked, outstandingPing uint64
	for {
		select {
		case <-session.Done():
			_ = stream.Send(&coordinatorv1.WatchRoutesResponse{Payload: &coordinatorv1.WatchRoutesResponse_ResyncRequired{ResyncRequired: &coordinatorv1.ResyncRequired{Reason: "subscriber queue overflow"}}})
			return status.Error(codes.ResourceExhausted, "route resync required")
		default:
		}
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case item := <-incoming:
			if item.err != nil {
				if errors.Is(item.err, io.EOF) {
					return nil
				}
				return item.err
			}
			if item.request.GetSubscribe() != nil {
				return status.Error(codes.FailedPrecondition, "Subscribe was already received")
			}
			if ack := item.request.GetAck(); ack != nil {
				if ack.MapVersion < acked || ack.MapVersion > lastSent {
					return status.Error(codes.InvalidArgument, "route Ack version is invalid")
				}
				acked = ack.MapVersion
				lastActivity = s.cfg.Now()
				continue
			}
			if pong := item.request.GetPong(); pong != nil {
				if outstandingPing == 0 || pong.PingId != outstandingPing {
					return status.Error(codes.InvalidArgument, "watch Pong does not match outstanding Ping")
				}
				outstandingPing = 0
				lastActivity = s.cfg.Now()
				continue
			}
			return status.Error(codes.InvalidArgument, "watch request payload is required")
		case message := <-session.Messages():
			if snapshot := message.GetSnapshot(); snapshot != nil {
				lastSent = snapshot.MapVersion
			}
			if batch := message.GetRouteBatch(); batch != nil {
				lastSent = batch.MapVersion
			}
			if err := stream.Send(message); err != nil {
				return err
			}
		case <-pingTicker.C:
			id := s.pingID.Add(1)
			outstandingPing = id
			if err := stream.Send(&coordinatorv1.WatchRoutesResponse{Payload: &coordinatorv1.WatchRoutesResponse_Ping{Ping: &coordinatorv1.WatchPing{PingId: id}}}); err != nil {
				return err
			}
		case <-timeoutTicker.C:
			if s.cfg.Now().Sub(lastActivity) > s.cfg.AckTimeout {
				return status.Error(codes.DeadlineExceeded, "watch acknowledgement timeout")
			}
		}
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
