package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	httpv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/http"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const protobufMediaType = "application/x-protobuf"
const clientConfigPublishedAtMS int64 = 1785369600000

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type HandlerConfig struct {
	Origin          string
	GatewayID       string
	GatewayURL      string
	ClientConfigURL string
}

type Handler struct {
	store        *Store
	config       HandlerConfig
	clientConfig []byte
	configDigest [32]byte
}

func NewHandler(store *Store, config HandlerConfig) (http.Handler, error) {
	if store == nil {
		return nil, errors.New("auth store is required")
	}
	if config.Origin == "" {
		config.Origin = "http://localhost:5173"
	}
	if config.GatewayID == "" {
		config.GatewayID = "local-gateway"
	}
	if config.GatewayURL == "" {
		config.GatewayURL = "ws://127.0.0.1:8081/ws"
	}
	if config.ClientConfigURL == "" {
		config.ClientConfigURL = "http://127.0.0.1:8080/v1/client-config/1"
	}
	if err := validateDevelopmentURL(config.GatewayURL, "ws"); err != nil {
		return nil, fmt.Errorf("gateway URL: %w", err)
	}
	if err := validateDevelopmentURL(config.ClientConfigURL, "http"); err != nil {
		return nil, fmt.Errorf("client config URL: %w", err)
	}
	configBody, err := proto.Marshal(&httpv1.ClientConfigPackage{
		SchemaVersion: 1, ClientConfigVersion: 1, PublishedAtMs: clientConfigPublishedAtMS,
	})
	if err != nil {
		return nil, err
	}
	h := &Handler{store: store, config: config, clientConfig: configBody}
	h.configDigest = sha256.Sum256(configBody)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", h.livez)
	mux.HandleFunc("GET /readyz", h.livez)
	mux.HandleFunc("GET /v1/auth/csrf", h.csrf)
	mux.HandleFunc("POST /v1/auth/register", h.register)
	mux.HandleFunc("POST /v1/auth/login", h.login)
	mux.HandleFunc("GET /v1/auth/session", h.session)
	mux.HandleFunc("POST /v1/auth/logout", h.logout)
	mux.HandleFunc("GET /v1/gateways", h.gateways)
	mux.HandleFunc("GET /v1/bootstrap", h.bootstrap)
	mux.HandleFunc("POST /v1/ws-tickets", h.issueTicket)
	mux.HandleFunc("GET /v1/client-config/1", h.clientConfigPackage)
	mux.HandleFunc("POST /internal/v1/ws-tickets/consume", h.consumeTicket)
	return h.common(mux), nil
}

func (h *Handler) common(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if !uuidPattern.MatchString(requestID) {
			requestID = newUUID()
		}
		w.Header().Set("X-Request-ID", requestID)
		if strings.HasPrefix(r.URL.Path, "/v1/") && r.URL.Path != "/v1/client-config/1" {
			w.Header().Set("Cache-Control", "no-store")
			if !acceptsProtobuf(r.Header.Get("Accept")) {
				h.writeError(w, http.StatusNotAcceptable, httpv1.HttpErrorCode_NOT_ACCEPTABLE, requestID)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) livez(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"ready"}`)
}

func (h *Handler) csrf(w http.ResponseWriter, r *http.Request) {
	if !h.validOrigin(r) {
		h.writeError(w, http.StatusForbidden, httpv1.HttpErrorCode_CSRF_REJECTED, requestID(w))
		return
	}
	_, session := h.optionalSession(r)
	token, expires, err := h.store.NewCSRF(session)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, httpv1.HttpErrorCode_INTERNAL_ERROR, requestID(w))
		return
	}
	setCSRFCookie(w, token, expires)
	h.writeProto(w, http.StatusOK, &httpv1.CsrfResponse{CsrfToken: token, ExpiresAtMs: expires.UnixMilli()})
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	_, currentSession := h.optionalSession(r)
	if !h.validateMutation(w, r, currentSession) {
		return
	}
	var request httpv1.RegisterRequest
	if !h.decode(w, r, &request) {
		return
	}
	name := strings.ToLower(request.AccountName)
	if !ValidateCredentials(name, request.Password) || name != request.AccountName {
		h.writeError(w, http.StatusBadRequest, httpv1.HttpErrorCode_INVALID_ARGUMENT, requestID(w))
		return
	}
	raw, session, err := h.store.Register(name, request.Password)
	if errors.Is(err, ErrAccountUnavailable) {
		h.writeError(w, http.StatusConflict, httpv1.HttpErrorCode_ACCOUNT_NAME_UNAVAILABLE, requestID(w))
		return
	}
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, httpv1.HttpErrorCode_INTERNAL_ERROR, requestID(w))
		return
	}
	h.establishSession(w, raw, session)
	h.writeProto(w, http.StatusCreated, &httpv1.RegisterResponse{Session: sessionView(session)})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	_, currentSession := h.optionalSession(r)
	if !h.validateMutation(w, r, currentSession) {
		return
	}
	var request httpv1.LoginRequest
	if !h.decode(w, r, &request) {
		return
	}
	name := strings.ToLower(request.AccountName)
	if !ValidateCredentials(name, request.Password) || name != request.AccountName {
		h.writeError(w, http.StatusUnauthorized, httpv1.HttpErrorCode_INVALID_CREDENTIALS, requestID(w))
		return
	}
	raw, session, err := h.store.Login(name, request.Password)
	if errors.Is(err, ErrInvalidCredentials) {
		clearSessionCookie(w)
		h.writeError(w, http.StatusUnauthorized, httpv1.HttpErrorCode_INVALID_CREDENTIALS, requestID(w))
		return
	}
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, httpv1.HttpErrorCode_INTERNAL_ERROR, requestID(w))
		return
	}
	h.establishSession(w, raw, session)
	h.writeProto(w, http.StatusOK, &httpv1.LoginResponse{Session: sessionView(session)})
}

func (h *Handler) session(w http.ResponseWriter, r *http.Request) {
	session, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	h.writeProto(w, http.StatusOK, &httpv1.SessionResponse{
		Session: sessionView(session), ServerTimeMs: time.Now().UnixMilli(),
	})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	raw, session := h.optionalSession(r)
	if !h.validateMutation(w, r, session) {
		return
	}
	var request httpv1.LogoutRequest
	if !h.decode(w, r, &request) {
		return
	}
	h.store.Logout(raw)
	clearSessionCookie(w)
	clearCSRFCookie(w)
	h.writeProto(w, http.StatusOK, &httpv1.LogoutResponse{LoggedOut: true})
}

func (h *Handler) gateways(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireSession(w, r); !ok {
		return
	}
	now := time.Now()
	h.writeProto(w, http.StatusOK, &httpv1.GatewayDiscoveryResponse{
		Gateways: []*httpv1.GatewayEndpoint{h.gateway(now)}, ServerTimeMs: now.UnixMilli(),
	})
}

func (h *Handler) bootstrap(w http.ResponseWriter, r *http.Request) {
	session, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	now := time.Now()
	h.writeProto(w, http.StatusOK, &httpv1.ClientBootstrapResponse{
		AuthBootstrap: &httpv1.AuthBootstrap{
			PlayerId: session.PlayerID, HeartbeatIntervalMs: 30000,
			ClientConfigVersion: 1, ClientConfigUrl: h.config.ClientConfigURL,
			ClientConfigSha256: h.configDigest[:], ProtocolMin: 1, ProtocolMax: 1,
		},
		Gateways: []*httpv1.GatewayEndpoint{h.gateway(now)}, ServerTimeMs: now.UnixMilli(),
	})
}

func (h *Handler) issueTicket(w http.ResponseWriter, r *http.Request) {
	_, session := h.optionalSession(r)
	if session == nil {
		clearSessionCookie(w)
		h.writeError(w, http.StatusUnauthorized, httpv1.HttpErrorCode_UNAUTHENTICATED, requestID(w))
		return
	}
	if !h.validateMutation(w, r, session) {
		return
	}
	var request httpv1.WsTicketRequest
	if !h.decode(w, r, &request) {
		return
	}
	if !uuidPattern.MatchString(request.TicketRequestId) || request.GatewayId == "" {
		h.writeError(w, http.StatusBadRequest, httpv1.HttpErrorCode_INVALID_ARGUMENT, requestID(w))
		return
	}
	ticket, expires, err := h.store.IssueTicket(session, request.TicketRequestId, request.GatewayId)
	switch {
	case errors.Is(err, ErrGatewayNotFound):
		h.writeError(w, http.StatusNotFound, httpv1.HttpErrorCode_GATEWAY_NOT_FOUND, requestID(w))
	case errors.Is(err, ErrTicketConflict):
		h.writeError(w, http.StatusConflict, httpv1.HttpErrorCode_TICKET_REQUEST_CONFLICT, requestID(w))
	case errors.Is(err, ErrTicketReplay):
		h.writeError(w, http.StatusConflict, httpv1.HttpErrorCode_TICKET_REPLAY_EXPIRED, requestID(w))
	case errors.Is(err, ErrUnauthenticated):
		clearSessionCookie(w)
		h.writeError(w, http.StatusUnauthorized, httpv1.HttpErrorCode_UNAUTHENTICATED, requestID(w))
	case err != nil:
		h.writeError(w, http.StatusInternalServerError, httpv1.HttpErrorCode_INTERNAL_ERROR, requestID(w))
	default:
		h.writeProto(w, http.StatusCreated, &httpv1.WsTicketResponse{
			WsTicket: ticket, ExpiresAtMs: expires.UnixMilli(), GatewayId: request.GatewayId,
		})
	}
}

func (h *Handler) clientConfigPackage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", protobufMediaType)
	w.Header().Set("Content-Encoding", "identity")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", fmt.Sprintf(`"%x"`, h.configDigest))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(h.clientConfig)
}

func (h *Handler) consumeTicket(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "unsupported media type", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var request struct {
		Ticket    string `json:"ticket"`
		GatewayID string `json:"gateway_id"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Ticket == "" || request.GatewayID == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	playerID, err := h.store.ConsumeTicket(request.Ticket, request.GatewayID)
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		PlayerID string `json:"player_id"`
	}{PlayerID: strconv.FormatUint(playerID, 10)})
}

func (h *Handler) validateMutation(w http.ResponseWriter, r *http.Request, session *Session) bool {
	if !h.validOrigin(r) {
		h.writeError(w, http.StatusForbidden, httpv1.HttpErrorCode_CSRF_REJECTED, requestID(w))
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		h.writeError(w, http.StatusForbidden, httpv1.HttpErrorCode_CSRF_REJECTED, requestID(w))
		return false
	}
	cookie, err := r.Cookie(CSRFCookieName)
	header := r.Header.Get("X-CSRF-Token")
	if err != nil || header == "" || subtleString(cookie.Value, header) == 0 || !h.store.ValidateCSRF(header, session) {
		h.writeError(w, http.StatusForbidden, httpv1.HttpErrorCode_CSRF_REJECTED, requestID(w))
		return false
	}
	return true
}

func (h *Handler) validOrigin(r *http.Request) bool {
	return r.Header.Get("Origin") == h.config.Origin
}

func (h *Handler) decode(w http.ResponseWriter, r *http.Request, message proto.Message) bool {
	if r.Header.Get("Content-Encoding") != "" {
		h.writeError(w, http.StatusUnsupportedMediaType, httpv1.HttpErrorCode_UNSUPPORTED_MEDIA_TYPE, requestID(w))
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != protobufMediaType {
		h.writeError(w, http.StatusUnsupportedMediaType, httpv1.HttpErrorCode_UNSUPPORTED_MEDIA_TYPE, requestID(w))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeError(w, http.StatusRequestEntityTooLarge, httpv1.HttpErrorCode_PAYLOAD_TOO_LARGE, requestID(w))
		return false
	}
	if hasDuplicateSingular(body, message.ProtoReflect().Descriptor()) {
		h.writeError(w, http.StatusBadRequest, httpv1.HttpErrorCode_INVALID_ARGUMENT, requestID(w))
		return false
	}
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(body, message); err != nil {
		h.writeError(w, http.StatusBadRequest, httpv1.HttpErrorCode_INVALID_ARGUMENT, requestID(w))
		return false
	}
	return true
}

func hasDuplicateSingular(body []byte, descriptor protoreflect.MessageDescriptor) bool {
	seen := make(map[protowire.Number]struct{})
	for len(body) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(body)
		if tagLength < 0 {
			return false
		}
		body = body[tagLength:]
		valueLength := protowire.ConsumeFieldValue(number, wireType, body)
		if valueLength < 0 {
			return false
		}
		field := descriptor.Fields().ByNumber(protoreflect.FieldNumber(number))
		if field != nil && field.Cardinality() != protoreflect.Repeated {
			if _, exists := seen[number]; exists {
				return true
			}
			seen[number] = struct{}{}
		}
		body = body[valueLength:]
	}
	return false
}

func (h *Handler) requireSession(w http.ResponseWriter, r *http.Request) (*Session, bool) {
	_, session := h.optionalSession(r)
	if session == nil {
		clearSessionCookie(w)
		h.writeError(w, http.StatusUnauthorized, httpv1.HttpErrorCode_UNAUTHENTICATED, requestID(w))
		return nil, false
	}
	return session, true
}

func (h *Handler) optionalSession(r *http.Request) (string, *Session) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return "", nil
	}
	session, err := h.store.Session(cookie.Value)
	if err != nil {
		return cookie.Value, nil
	}
	return cookie.Value, session
}

func (h *Handler) establishSession(w http.ResponseWriter, raw string, session *Session) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookieName, Value: raw, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Expires: session.AbsoluteExpiresAt,
	})
	token, expires, err := h.store.NewCSRF(session)
	if err == nil {
		setCSRFCookie(w, token, expires)
	}
}

func (h *Handler) gateway(now time.Time) *httpv1.GatewayEndpoint {
	return &httpv1.GatewayEndpoint{
		GatewayId: h.config.GatewayID, WebsocketUrl: h.config.GatewayURL,
		Region: "local", Priority: 0, ExpiresAtMs: now.Add(time.Minute).UnixMilli(),
	}
}

func (h *Handler) writeProto(w http.ResponseWriter, status int, message proto.Message) {
	body, err := proto.Marshal(message)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, httpv1.HttpErrorCode_INTERNAL_ERROR, requestID(w))
		return
	}
	w.Header().Set("Content-Type", protobufMediaType)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (h *Handler) writeError(w http.ResponseWriter, status int, code httpv1.HttpErrorCode, correlationID string) {
	body, _ := proto.Marshal(&httpv1.HttpError{Code: code, CorrelationId: correlationID})
	w.Header().Set("Content-Type", protobufMediaType)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func sessionView(session *Session) *httpv1.SessionView {
	return &httpv1.SessionView{
		PlayerId: session.PlayerID, AccountName: session.AccountName,
		CreatedAtMs: session.CreatedAt.UnixMilli(), IdleExpiresAtMs: session.IdleExpiresAt.UnixMilli(),
		AbsoluteExpiresAtMs: session.AbsoluteExpiresAt.UnixMilli(),
	}
}

func setCSRFCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: CSRFCookieName, Value: token, Path: "/", SameSite: http.SameSiteStrictMode, Expires: expires,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookieName, Value: "", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}

func clearCSRFCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CSRFCookieName, Value: "", Path: "/", SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

func requestID(w http.ResponseWriter) string { return w.Header().Get("X-Request-ID") }

func acceptsProtobuf(value string) bool {
	if value == "" {
		return true
	}
	for _, part := range strings.Split(value, ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err == nil && (mediaType == protobufMediaType || mediaType == "*/*") && params["q"] != "0" {
			return true
		}
	}
	return false
}

func newUUID() string {
	raw, err := randomTokenBytes(16)
	if err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	return hex.EncodeToString(raw[0:4]) + "-" + hex.EncodeToString(raw[4:6]) + "-" +
		hex.EncodeToString(raw[6:8]) + "-" + hex.EncodeToString(raw[8:10]) + "-" +
		hex.EncodeToString(raw[10:16])
}

func randomTokenBytes(size int) ([]byte, error) {
	value := make([]byte, size)
	_, err := rand.Read(value)
	return value, err
}

func subtleString(a, b string) int {
	if len(a) != len(b) {
		return 0
	}
	var diff byte
	for i := range len(a) {
		diff |= a[i] ^ b[i]
	}
	if diff == 0 {
		return 1
	}
	return 0
}

func validateDevelopmentURL(rawURL, scheme string) error {
	prefix := scheme + "://"
	if !strings.HasPrefix(rawURL, prefix) {
		return fmt.Errorf("must use %s", scheme)
	}
	hostPort := strings.SplitN(strings.TrimPrefix(rawURL, prefix), "/", 2)[0]
	host := hostPort
	if parsedHost, _, err := net.SplitHostPort(hostPort); err == nil {
		host = parsedHost
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("must advertise loopback only")
	}
	return nil
}
