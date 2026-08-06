package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type GRPCZoneCommander struct {
	mu        sync.Mutex
	conns     map[string]*grpc.ClientConn
	clients   map[string]rpcv1.GameCommandServiceClient
	dialOpts  []grpc.DialOption
	gatewayID string
}

type zoneCommandError struct {
	kind string
	err  error
}

func (e *zoneCommandError) Error() string {
	return e.err.Error()
}

func (e *zoneCommandError) Unwrap() error {
	return e.err
}

func NewGRPCZoneCommander(
	key []byte,
	gatewayID string,
) (*GRPCZoneCommander, error) {
	if strings.TrimSpace(gatewayID) == "" {
		gatewayID = DefaultGatewayID
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: "gate",
		Key:     key,
	})
	if err != nil {
		return nil, err
	}
	return &GRPCZoneCommander{
		conns:   make(map[string]*grpc.ClientConn),
		clients: make(map[string]rpcv1.GameCommandServiceClient),
		dialOpts: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithUnaryInterceptor(interceptor),
			grpc.WithDefaultCallOptions(
				grpc.MaxCallSendMsgSize(128<<10),
				grpc.MaxCallRecvMsgSize(128<<10),
			),
		},
		gatewayID: gatewayID,
	}, nil
}

func (z *GRPCZoneCommander) Command(
	ctx context.Context,
	route Route,
	caller uint64,
	body []byte,
) ([]byte, error) {
	if z == nil {
		return nil, errors.New("gRPC Zone commander is not configured")
	}
	envelope := &wsv1.WsEnvelope{}
	if len(body) == 0 || proto.Unmarshal(body, envelope) != nil {
		return nil, &zoneCommandError{
			kind: "request",
			err:  errors.New("invalid Zone command protobuf"),
		}
	}
	client, err := z.client(route.OwnerEndpoint) // ← 拿到 gRPC 客户端（内部可能触发上面的 dial）
	if err != nil {
		return nil, &zoneCommandError{kind: "target", err: err}
	}
	response, err := client.ExecutePlayerCommand(ctx, &rpcv1.ExecutePlayerCommandRequest{
		CallerPlayerId: caller,
		GateId:         z.gatewayID,
		Route: &rpcv1.CommittedRoute{
			LogicalShardId: route.ShardID,
			OwnerZoneId:    route.OwnerZoneID,
			OwnerEpoch:     route.OwnerEpoch,
			RouteVersion:   route.RouteVersion,
		},
		Envelope: envelope,
	})
	if status.Code(err) == codes.FailedPrecondition {
		return nil, ErrNotOwner
	}
	if err != nil {
		return nil, &zoneCommandError{
			kind: "grpc_" + strings.ToLower(status.Code(err).String()),
			err:  fmt.Errorf("Zone gRPC command: %w", err),
		}
	}
	if response == nil || response.Envelope == nil {
		return nil, &zoneCommandError{
			kind: "empty_response",
			err:  errors.New("Zone gRPC response has no envelope"),
		}
	}
	encoded, err := proto.Marshal(response.Envelope)
	if err != nil {
		return nil, &zoneCommandError{kind: "encode", err: err}
	}
	if len(encoded) > MaxMessageBytes {
		return nil, &zoneCommandError{
			kind: "too_large",
			err:  errors.New("Zone response exceeds 64 KiB"),
		}
	}
	return encoded, nil
}

func (z *GRPCZoneCommander) Close() error {
	if z == nil {
		return nil
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	var result error
	for target, conn := range z.conns {
		if err := conn.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close Zone %s: %w", target, err))
		}
	}
	z.conns = make(map[string]*grpc.ClientConn)
	z.clients = make(map[string]rpcv1.GameCommandServiceClient)
	return result
}

func (z *GRPCZoneCommander) client(
	endpoint string,
) (rpcv1.GameCommandServiceClient, error) {
	target, err := rpcnet.TargetFromHTTPURL(endpoint) // 把路由表里的 endpoint 转成 gRPC 目标地址
	if err != nil {
		return nil, fmt.Errorf("invalid Zone gRPC endpoint: %w", err)
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	if client := z.clients[target]; client != nil {
		return client, nil // 已经连过，直接复用
	}
	conn, err := grpc.NewClient(target, z.dialOpts...) // ← 真正"拨号"建立 gRPC 连接
	if err != nil {
		return nil, fmt.Errorf("create Zone gRPC client: %w", err)
	}
	client := rpcv1.NewGameCommandServiceClient(conn)
	z.conns[target] = conn
	z.clients[target] = client
	return client, nil
}
