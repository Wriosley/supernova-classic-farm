package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"google.golang.org/protobuf/proto"
)

const maxEnvelopeBytes = 64 << 10

type runtimeHandler struct {
	runtime *player.Runtime
}

func newCommandHandler(runtime *player.Runtime) http.Handler {
	return &runtimeHandler{runtime: runtime}
}

func (h *runtimeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "LOOPBACK_ONLY")
		return
	}

	callerPlayerID, err := parseRequiredUintHeader(r, "X-Caller-Player-ID")
	if err != nil || callerPlayerID == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_CALLER_PLAYER_ID")
		return
	}
	ownerEpoch, err := parseRequiredUintHeader(r, "X-Owner-Epoch")
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_OWNER_EPOCH")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxEnvelopeBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "ENVELOPE_TOO_LARGE")
		return
	}
	request := &wsv1.WsEnvelope{}
	if len(body) == 0 || proto.Unmarshal(body, request) != nil {
		writeError(w, http.StatusBadRequest, "MALFORMED_PROTOBUF")
		return
	}

	response, err := h.runtime.Handle(r.Context(), callerPlayerID, ownerEpoch, request)
	switch {
	case errors.Is(err, player.ErrNotOwner):
		writeError(w, http.StatusConflict, "NOT_OWNER")
		return
	case errors.Is(err, player.ErrForbiddenTarget):
		writeError(w, http.StatusForbidden, "FORBIDDEN_TARGET")
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, "INVALID_COMMAND")
		return
	}

	encoded, err := proto.Marshal(response)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ENCODE_FAILED")
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

func parseRequiredUintHeader(r *http.Request, name string) (uint64, error) {
	value := r.Header.Get(name)
	if value == "" {
		return 0, errors.New("missing header")
	}
	return strconv.ParseUint(value, 10, 64)
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code string `json:"code"`
	}{Code: code})
}
