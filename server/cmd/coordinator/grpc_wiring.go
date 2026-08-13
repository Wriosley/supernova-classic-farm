package main

import (
	"encoding/json"
	"net/http"

	coordinatorv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator"
	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/publisher"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"google.golang.org/grpc"
)

func newCoordinatorGRPCServer(service coordinatorv1.CoordinatorServiceServer, key []byte) (*grpc.Server, error) {
	callers := append([]string{"gate", "info"}, rpcauth.ZoneAllowedCallers(true)...)
	allow := map[string][]string{
		coordinatorv1.CoordinatorService_GetRouteSnapshot_FullMethodName:  callers,
		coordinatorv1.CoordinatorService_GetShardRoute_FullMethodName:     callers,
		coordinatorv1.CoordinatorService_WatchRoutes_FullMethodName:       callers,
		coordinatorv1.CoordinatorService_ReportZoneFailure_FullMethodName: callers,
	}
	unary, err := rpcauth.NewServerUnaryInterceptor(rpcauth.ServerConfig{Key: key, AllowedCallers: allow})
	if err != nil {
		return nil, err
	}
	stream, err := rpcauth.NewServerStreamInterceptor(rpcauth.ServerConfig{Key: key, AllowedCallers: allow})
	if err != nil {
		return nil, err
	}
	server := grpc.NewServer(grpc.UnaryInterceptor(unary), grpc.StreamInterceptor(stream), grpc.MaxSendMsgSize(8<<20), grpc.MaxRecvMsgSize(8<<20))
	coordinatorv1.RegisterCoordinatorServiceServer(server, service)
	return server, nil
}

func subscriberDiagnosticsHandler(p *publisher.Publisher) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(p.Diagnostics())
	}
}
