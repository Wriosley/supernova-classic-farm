// Package rpcauth authenticates internal unary gRPC calls with HMAC metadata.
package rpcauth

import (
	"container/heap"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	KeyEnvironment = "INTERNAL_GRPC_HMAC_KEY"

	CallerServiceMetadata = "x-cf-caller-service"
	TimestampMetadata     = "x-cf-timestamp"
	NonceMetadata         = "x-cf-nonce"
	BodySHA256Metadata    = "x-cf-body-sha256"
	SignatureMetadata     = "x-cf-signature"

	DefaultClockWindow = 30 * time.Second
	minimumKeyBytes    = 32
)

var (
	servicePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	noncePattern   = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

type ClientConfig struct {
	Service string
	Key     []byte
	Now     func() time.Time
	Nonce   func() (string, error)
}

type ServerConfig struct {
	Key            []byte
	AllowedCallers map[string][]string
	Now            func() time.Time
	ClockWindow    time.Duration
}

type replayCache struct {
	mu          sync.Mutex
	entries     map[string]time.Time
	expirations replayExpiryHeap
}

type serverVerifier struct {
	key     []byte
	allowed map[string]map[string]struct{}
	now     func() time.Time
	window  time.Duration
	replays *replayCache
}

type replayExpiry struct {
	key       string
	expiresAt time.Time
}

type replayExpiryHeap []replayExpiry

func LoadKeyFromEnv() ([]byte, error) {
	key := []byte(strings.TrimSpace(os.Getenv(KeyEnvironment)))
	if err := validateKey(key); err != nil {
		return nil, fmt.Errorf("%s: %w", KeyEnvironment, err)
	}
	return key, nil
}

func NewClientUnaryInterceptor(cfg ClientConfig) (grpc.UnaryClientInterceptor, error) {
	service, key, now, nonce, err := clientSigner(cfg)
	if err != nil {
		return nil, err
	}
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		message, ok := req.(proto.Message)
		if !ok {
			return status.Error(codes.Internal, "internal RPC request is not protobuf")
		}
		bodyHash, err := deterministicBodyHash(message)
		if err != nil {
			return status.Error(codes.Internal, "hash internal RPC request")
		}
		requestNonce, err := nonce()
		if err != nil || !noncePattern.MatchString(requestNonce) {
			return status.Error(codes.Internal, "create internal RPC nonce")
		}
		timestamp := strconv.FormatInt(now().UTC().UnixMilli(), 10)
		outgoing := signedMetadataContext(ctx, service, key, method, timestamp, requestNonce, bodyHash)
		return invoker(
			outgoing,
			method, req, reply, cc, opts...,
		)
	}, nil
}

func NewClientStreamInterceptor(cfg ClientConfig) (grpc.StreamClientInterceptor, error) {
	service, key, now, nonce, err := clientSigner(cfg)
	if err != nil {
		return nil, err
	}
	emptyHash := hashBytes(nil)
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string,
		streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		requestNonce, nonceErr := nonce()
		if nonceErr != nil || !noncePattern.MatchString(requestNonce) {
			return nil, status.Error(codes.Internal, "create internal RPC nonce")
		}
		timestamp := strconv.FormatInt(now().UTC().UnixMilli(), 10)
		return streamer(signedMetadataContext(ctx, service, key, method, timestamp, requestNonce, emptyHash), desc, cc, method, opts...)
	}, nil
}

func NewServerUnaryInterceptor(cfg ServerConfig) (grpc.UnaryServerInterceptor, error) {
	verifier, err := newServerVerifier(cfg)
	if err != nil {
		return nil, err
	}

	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		message, ok := req.(proto.Message)
		if !ok {
			return nil, unauthenticated()
		}
		actualBodyHash, err := deterministicBodyHash(message)
		if err != nil {
			return nil, unauthenticated()
		}
		if err := verifier.verify(ctx, info.FullMethod, actualBodyHash); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}, nil
}

func NewServerStreamInterceptor(cfg ServerConfig) (grpc.StreamServerInterceptor, error) {
	verifier, err := newServerVerifier(cfg)
	if err != nil {
		return nil, err
	}
	emptyHash := hashBytes(nil)
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := verifier.verify(stream.Context(), info.FullMethod, emptyHash); err != nil {
			return err
		}
		return handler(srv, stream)
	}, nil
}

func clientSigner(cfg ClientConfig) (string, []byte, func() time.Time, func() (string, error), error) {
	if !servicePattern.MatchString(cfg.Service) {
		return "", nil, nil, nil, errors.New("caller service identity is invalid")
	}
	if err := validateKey(cfg.Key); err != nil {
		return "", nil, nil, nil, err
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	nonce := cfg.Nonce
	if nonce == nil {
		nonce = randomNonce
	}
	return cfg.Service, append([]byte(nil), cfg.Key...), now, nonce, nil
}

func signedMetadataContext(ctx context.Context, service string, key []byte, method, timestamp, nonce, bodyHash string) context.Context {
	outgoing, _ := metadata.FromOutgoingContext(ctx)
	outgoing = outgoing.Copy()
	outgoing.Set(CallerServiceMetadata, service)
	outgoing.Set(TimestampMetadata, timestamp)
	outgoing.Set(NonceMetadata, nonce)
	outgoing.Set(BodySHA256Metadata, bodyHash)
	outgoing.Set(SignatureMetadata, sign(key, service, method, timestamp, nonce, bodyHash))
	return metadata.NewOutgoingContext(ctx, outgoing)
}

func newServerVerifier(cfg ServerConfig) (*serverVerifier, error) {
	if err := validateKey(cfg.Key); err != nil {
		return nil, err
	}
	if len(cfg.AllowedCallers) == 0 {
		return nil, errors.New("internal RPC caller allowlist is required")
	}
	allowed := make(map[string]map[string]struct{}, len(cfg.AllowedCallers))
	for method, services := range cfg.AllowedCallers {
		if !strings.HasPrefix(method, "/") || len(services) == 0 {
			return nil, errors.New("internal RPC caller allowlist is invalid")
		}
		allowed[method] = make(map[string]struct{}, len(services))
		for _, service := range services {
			if !servicePattern.MatchString(service) {
				return nil, errors.New("allowed caller service identity is invalid")
			}
			allowed[method][service] = struct{}{}
		}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	window := cfg.ClockWindow
	if window == 0 {
		window = DefaultClockWindow
	}
	if window <= 0 {
		return nil, errors.New("internal RPC clock window must be positive")
	}
	return &serverVerifier{key: append([]byte(nil), cfg.Key...), allowed: allowed, now: now, window: window, replays: &replayCache{entries: make(map[string]time.Time)}}, nil
}

func (v *serverVerifier) verify(ctx context.Context, method, actualBodyHash string) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return unauthenticated()
	}
	caller, ok := one(md, CallerServiceMetadata)
	if !ok || !servicePattern.MatchString(caller) {
		return unauthenticated()
	}
	callers, methodAllowed := v.allowed[method]
	if _, callerAllowed := callers[caller]; !methodAllowed || !callerAllowed {
		return status.Error(codes.PermissionDenied, "internal RPC caller is not allowed")
	}
	timestamp, ok := one(md, TimestampMetadata)
	if !ok {
		return unauthenticated()
	}
	timestampMS, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return unauthenticated()
	}
	current := v.now().UTC()
	signedAt := time.UnixMilli(timestampMS).UTC()
	if delta := current.Sub(signedAt); delta < -v.window || delta > v.window {
		return unauthenticated()
	}
	nonce, ok := one(md, NonceMetadata)
	if !ok || !noncePattern.MatchString(nonce) {
		return unauthenticated()
	}
	suppliedBodyHash, ok := one(md, BodySHA256Metadata)
	if !ok || !equalHex(actualBodyHash, suppliedBodyHash, sha256.Size) {
		return unauthenticated()
	}
	suppliedSignature, ok := one(md, SignatureMetadata)
	if !ok {
		return unauthenticated()
	}
	expected := sign(v.key, caller, method, timestamp, nonce, actualBodyHash)
	if !equalHex(expected, suppliedSignature, sha256.Size) {
		return unauthenticated()
	}
	if !v.replays.admit(caller+"\x00"+nonce, current, signedAt.Add(v.window)) {
		return unauthenticated()
	}
	return nil
}

func hashBytes(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func deterministicBodyHash(message proto.Message) (string, error) {
	body, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func sign(key []byte, caller, method, timestamp, nonce, bodyHash string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(strings.Join(
		[]string{caller, method, timestamp, nonce, bodyHash}, "\n",
	)))
	return hex.EncodeToString(mac.Sum(nil))
}

func equalHex(expected, supplied string, size int) bool {
	expectedBytes, expectedErr := hex.DecodeString(expected)
	suppliedBytes, suppliedErr := hex.DecodeString(supplied)
	return expectedErr == nil && suppliedErr == nil &&
		len(expectedBytes) == size && len(suppliedBytes) == size &&
		hmac.Equal(expectedBytes, suppliedBytes)
}

func randomNonce() (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(nonce[:]), nil
}

func validateKey(key []byte) error {
	if len(key) < minimumKeyBytes {
		return fmt.Errorf("HMAC key must contain at least %d bytes", minimumKeyBytes)
	}
	return nil
}

func one(md metadata.MD, key string) (string, bool) {
	values := md.Get(key)
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", false
	}
	return values[0], true
}

func unauthenticated() error {
	return status.Error(codes.Unauthenticated, "internal RPC authentication failed")
}

func (c *replayCache) admit(key string, now, expiresAt time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.expirations.Len() > 0 &&
		!c.expirations[0].expiresAt.After(now) {
		expired := heap.Pop(&c.expirations).(replayExpiry)
		if current, exists := c.entries[expired.key]; exists &&
			current.Equal(expired.expiresAt) {
			delete(c.entries, expired.key)
		}
	}
	if _, exists := c.entries[key]; exists {
		return false
	}
	if !expiresAt.After(now) {
		return false
	}
	c.entries[key] = expiresAt
	heap.Push(&c.expirations, replayExpiry{key: key, expiresAt: expiresAt})
	return true
}

func (h replayExpiryHeap) Len() int { return len(h) }

func (h replayExpiryHeap) Less(i, j int) bool {
	return h[i].expiresAt.Before(h[j].expiresAt)
}

func (h replayExpiryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *replayExpiryHeap) Push(value any) {
	*h = append(*h, value.(replayExpiry))
}

func (h *replayExpiryHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}
