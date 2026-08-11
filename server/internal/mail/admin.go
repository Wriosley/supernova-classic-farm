package mail

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/internalnet"
)

const adminTokenEnv = "MAIL_ADMIN_TOKEN"

type adminAttachment struct {
	ItemID   uint32 `json:"item_id"`
	Quantity uint32 `json:"quantity"`
}

type createPublicMailRequest struct {
	Title             string            `json:"title"`
	Content           string            `json:"content"`
	SenderDisplayName string            `json:"sender_display_name"`
	PublishedAtMS     int64             `json:"published_at_ms"`
	SourceEventID     string            `json:"source_event_id"`
	Attachments       []adminAttachment `json:"attachments"`
}

type createPrivateMailRequest struct {
	RecipientPlayerID uint64            `json:"recipient_player_id"`
	Title             string            `json:"title"`
	Content           string            `json:"content"`
	SenderDisplayName string            `json:"sender_display_name"`
	SourceEventID     string            `json:"source_event_id"`
	Attachments       []adminAttachment `json:"attachments"`
}

type createMailResponse struct {
	MailID string `json:"mail_id"`
}

// AdminHandler serves intranet admin mail create endpoints.
type AdminHandler struct {
	service *Service
	token   []byte
}

func NewAdminHandler(service *Service, token string) (*AdminHandler, error) {
	token = strings.TrimSpace(token)
	if service == nil {
		return nil, errors.New("mail admin service is required")
	}
	if token == "" {
		return nil, errors.New("MAIL_ADMIN_TOKEN is required")
	}
	return &AdminHandler{service: service, token: []byte(token)}, nil
}

func LoadAdminTokenFromEnv() (string, error) {
	token := strings.TrimSpace(os.Getenv(adminTokenEnv))
	if token == "" {
		return "", errors.New("MAIL_ADMIN_TOKEN is required")
	}
	return token, nil
}

func (h *AdminHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /internal/v1/admin/mails/public", h.handleCreatePublic)
	mux.HandleFunc("POST /internal/v1/admin/mails/private", h.handleCreatePrivate)
}

func (h *AdminHandler) handleCreatePublic(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	var body createPublicMailRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid json")
		return
	}
	mailID, err := h.service.CreatePublicMail(r.Context(), CreatePublicMailInput{
		Title:             body.Title,
		Content:           body.Content,
		SenderDisplayName: body.SenderDisplayName,
		PublishedAtMS:     body.PublishedAtMS,
		SourceEventID:     body.SourceEventID,
		Attachments:       toProtoAttachments(body.Attachments),
	})
	if err != nil {
		writeCreateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, createMailResponse{MailID: mailID})
}

func (h *AdminHandler) handleCreatePrivate(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	var body createPrivateMailRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid json")
		return
	}
	mailID, err := h.service.CreatePrivateMail(r.Context(), CreatePrivateMailInput{
		RecipientPlayerID: body.RecipientPlayerID,
		Title:             body.Title,
		Content:           body.Content,
		SenderDisplayName: body.SenderDisplayName,
		SourceEventID:     body.SourceEventID,
		Attachments:       toProtoAttachments(body.Attachments),
	})
	if err != nil {
		writeCreateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, createMailResponse{MailID: mailID})
}

func (h *AdminHandler) authorize(w http.ResponseWriter, r *http.Request) bool {
	if !internalnet.RemoteAllowed(r.RemoteAddr) {
		writeAdminError(w, http.StatusForbidden, "forbidden")
		return false
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		writeAdminError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	provided := []byte(strings.TrimSpace(strings.TrimPrefix(header, prefix)))
	if subtle.ConstantTimeCompare(provided, h.token) != 1 {
		writeAdminError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	return true
}

func toProtoAttachments(in []adminAttachment) []*tcaplusv1.MailAttachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]*tcaplusv1.MailAttachment, 0, len(in))
	for _, attachment := range in {
		out = append(out, &tcaplusv1.MailAttachment{
			ItemId: attachment.ItemID, Quantity: attachment.Quantity,
		})
	}
	return out
}

func writeCreateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrAlreadyExists):
		writeAdminError(w, http.StatusConflict, "source_event_id already used")
	default:
		writeAdminError(w, http.StatusBadRequest, "invalid mail")
	}
}

func writeAdminError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
