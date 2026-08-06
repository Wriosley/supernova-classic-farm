package rpcauth

import (
	"context"
	"testing"
	"time"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const testMethod = "/classicfarm.rpc.v1.GameCommandService/ExecutePlayerCommand"

var testKey = []byte("phase-one-test-hmac-key-32-bytes-minimum")

func TestUnaryInterceptorsAuthenticateAndRejectReplay(t *testing.T) {
	now := time.Date(2026, 8, 6, 3, 0, 0, 0, time.UTC)
	request := &rpcv1.ExecutePlayerCommandRequest{CallerPlayerId: 17}
	incoming := signedIncomingContext(t, now, "gate", "00112233445566778899aabbccddeeff", request)
	server := testServerInterceptor(t, now, map[string][]string{
		testMethod: {"gate"},
	})
	info := &grpc.UnaryServerInfo{FullMethod: testMethod}
	handled := false
	handler := func(context.Context, any) (any, error) {
		handled = true
		return &rpcv1.ExecutePlayerCommandResponse{}, nil
	}
	if _, err := server(incoming, request, info, handler); err != nil {
		t.Fatalf("valid signed call failed: %v", err)
	}
	if !handled {
		t.Fatal("valid signed call did not reach handler")
	}
	if _, err := server(incoming, request, info, handler); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("replay code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestServerInterceptorRejectsAuthenticationFailures(t *testing.T) {
	now := time.Date(2026, 8, 6, 3, 0, 0, 0, time.UTC)
	request := &rpcv1.ExecutePlayerCommandRequest{CallerPlayerId: 17}
	info := &grpc.UnaryServerInfo{FullMethod: testMethod}
	handler := func(context.Context, any) (any, error) {
		t.Fatal("rejected request reached handler")
		return nil, nil
	}
	tests := []struct {
		name       string
		context    func() context.Context
		request    *rpcv1.ExecutePlayerCommandRequest
		wantStatus codes.Code
	}{
		{
			name: "expired timestamp",
			context: func() context.Context {
				return signedIncomingContext(
					t, now.Add(-DefaultClockWindow-time.Millisecond), "gate",
					"10112233445566778899aabbccddeeff", request,
				)
			},
			request: request, wantStatus: codes.Unauthenticated,
		},
		{
			name: "forged caller",
			context: func() context.Context {
				ctx := signedIncomingContext(
					t, now, "gate", "20112233445566778899aabbccddeeff", request,
				)
				md, _ := metadata.FromIncomingContext(ctx)
				md = md.Copy()
				md.Set(CallerServiceMetadata, "zone-a")
				return metadata.NewIncomingContext(context.Background(), md)
			},
			request: request, wantStatus: codes.PermissionDenied,
		},
		{
			name: "wrong signature",
			context: func() context.Context {
				ctx := signedIncomingContext(
					t, now, "gate", "30112233445566778899aabbccddeeff", request,
				)
				md, _ := metadata.FromIncomingContext(ctx)
				md = md.Copy()
				md.Set(SignatureMetadata, "00"+md.Get(SignatureMetadata)[0][2:])
				return metadata.NewIncomingContext(context.Background(), md)
			},
			request: request, wantStatus: codes.Unauthenticated,
		},
		{
			name: "changed body",
			context: func() context.Context {
				return signedIncomingContext(
					t, now, "gate", "40112233445566778899aabbccddeeff", request,
				)
			},
			request:    &rpcv1.ExecutePlayerCommandRequest{CallerPlayerId: 18},
			wantStatus: codes.Unauthenticated,
		},
	}
	server := testServerInterceptor(t, now, map[string][]string{
		testMethod: {"gate"},
	})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := server(test.context(), test.request, info, handler)
			if status.Code(err) != test.wantStatus {
				t.Fatalf("status = %v, want %v: %v", status.Code(err), test.wantStatus, err)
			}
		})
	}
}

func TestReplayCacheRetainsFutureDatedNonceUntilSignatureExpires(t *testing.T) {
	base := time.Date(2026, 8, 6, 3, 0, 0, 0, time.UTC)
	current := base
	request := &rpcv1.ExecutePlayerCommandRequest{CallerPlayerId: 17}
	incoming := signedIncomingContext(
		t, base.Add(DefaultClockWindow-time.Second), "gate",
		"50112233445566778899aabbccddeeff", request,
	)
	server, err := NewServerUnaryInterceptor(ServerConfig{
		Key: testKey,
		AllowedCallers: map[string][]string{
			testMethod: {"gate"},
		},
		Now: func() time.Time { return current },
	})
	if err != nil {
		t.Fatal(err)
	}
	info := &grpc.UnaryServerInfo{FullMethod: testMethod}
	handler := func(context.Context, any) (any, error) {
		return &rpcv1.ExecutePlayerCommandResponse{}, nil
	}
	if _, err := server(incoming, request, info, handler); err != nil {
		t.Fatalf("future-dated valid call failed: %v", err)
	}
	current = base.Add(DefaultClockWindow + time.Second)
	if _, err := server(incoming, request, info, handler); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("future-dated replay code = %v", status.Code(err))
	}
}

func TestLoadKeyFromEnvRequiresStrongKey(t *testing.T) {
	t.Setenv(KeyEnvironment, "short")
	if _, err := LoadKeyFromEnv(); err == nil {
		t.Fatal("short HMAC key was accepted")
	}
	t.Setenv(KeyEnvironment, string(testKey))
	key, err := LoadKeyFromEnv()
	if err != nil || string(key) != string(testKey) {
		t.Fatalf("LoadKeyFromEnv() = %q, %v", key, err)
	}
}

func signedIncomingContext(
	t *testing.T,
	now time.Time,
	service, nonce string,
	request *rpcv1.ExecutePlayerCommandRequest,
) context.Context {
	t.Helper()
	client, err := NewClientUnaryInterceptor(ClientConfig{
		Service: service,
		Key:     testKey,
		Now:     func() time.Time { return now },
		Nonce:   func() (string, error) { return nonce, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	var outgoing context.Context
	err = client(
		context.Background(), testMethod, request,
		&rpcv1.ExecutePlayerCommandResponse{}, nil,
		func(
			ctx context.Context, _ string, _, _ any,
			_ *grpc.ClientConn, _ ...grpc.CallOption,
		) error {
			outgoing = ctx
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	md, ok := metadata.FromOutgoingContext(outgoing)
	if !ok {
		t.Fatal("client interceptor did not attach metadata")
	}
	return metadata.NewIncomingContext(context.Background(), md)
}

func testServerInterceptor(
	t *testing.T,
	now time.Time,
	allowlist map[string][]string,
) grpc.UnaryServerInterceptor {
	t.Helper()
	server, err := NewServerUnaryInterceptor(ServerConfig{
		Key: testKey, AllowedCallers: allowlist,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return server
}
