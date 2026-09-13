package game

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type session struct {
	PlayerID string
	Expires  time.Time
}
type connection struct {
	Token  string
	Socket *websocket.Conn
}

type Server struct {
	db          *sql.DB
	mu          sync.Mutex
	sessions    map[string]session
	connections map[string]connection
}

func NewServer(db *sql.DB) *Server {
	return &Server{db: db, sessions: map[string]session{}, connections: map[string]connection{}}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { s.reply(w, 200, Response{Code: "OK"}) })
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		cfg := GameConfig()
		s.reply(w, 200, Response{Code: "OK", Config: &cfg})
	})
	mux.HandleFunc("POST /api/register", s.register)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/mailbox", s.mailboxHTTP)
	mux.HandleFunc("POST /api/mailbox", s.mailboxHTTP)
	mux.HandleFunc("POST /api/friends/add", s.addFriendHTTP)
	mux.HandleFunc("GET /api/friends", s.listFriendsHTTP)
	mux.HandleFunc("GET /api/friends/{id}/farm", s.friendFarmHTTP)
	mux.HandleFunc("POST /api/friends/{id}/steal", s.stealHTTP)
	mux.HandleFunc("POST /api/friends/{id}/mail", s.friendMailHTTP)
	mux.HandleFunc("GET /ws", s.websocket)
	return mux
}

// mailboxHTTP 给没有实现邮箱界面的测试客户端提供简单查询。
func (s *Server) mailboxHTTP(w http.ResponseWriter, r *http.Request) {
	playerID, ok := s.requirePlayer(w, r)
	if !ok {
		return
	}
	mails, err := listMails(r.Context(), s.db, playerID)
	if err != nil {
		s.failHTTP(w, err)
		return
	}
	s.reply(w, http.StatusOK, Response{Code: "OK", Mails: mails})
}

func decodeJSON(r *http.Request, target any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return ErrInvalid
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4096)).Decode(target); err != nil {
		return ErrInvalid
	}
	return nil
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if err := decodeJSON(r, &c); err != nil || validateCredentials(c.Username, c.Password) != nil {
		s.failHTTP(w, ErrInvalid)
		return
	}
	id, err := createPlayer(r.Context(), s.db, c.Username, c.Password)
	if err != nil {
		s.failHTTP(w, err)
		return
	}
	s.reply(w, http.StatusCreated, Response{Code: "OK", PlayerID: id})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if err := decodeJSON(r, &c); err != nil || validateCredentials(c.Username, c.Password) != nil {
		s.failHTTP(w, ErrInvalid)
		return
	}
	a, err := findAccount(r.Context(), s.db, c.Username)
	if err != nil || a.Password != encodePassword(c.Password) {
		s.failHTTP(w, ErrCredentials)
		return
	}
	token, err := randomHex(16)
	if err != nil {
		s.failHTTP(w, err)
		return
	}
	s.mu.Lock()
	s.sessions[token] = session{PlayerID: a.ID, Expires: time.Now().Add(24 * time.Hour)}
	s.mu.Unlock()
	s.reply(w, http.StatusOK, Response{Code: "OK", Token: token, PlayerID: a.ID})
}

func (s *Server) identity(token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.sessions[token]
	if !ok || time.Now().After(value.Expires) {
		delete(s.sessions, token)
		return "", ErrUnauthenticated
	}
	return value.PlayerID, nil
}

func bearerToken(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func (s *Server) requirePlayer(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, err := s.identity(bearerToken(r))
	if err != nil {
		s.failHTTP(w, err)
		return "", false
	}
	return id, true
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	id, err := s.identity(token)
	if err != nil {
		s.failHTTP(w, err)
		return
	}
	s.mu.Lock()
	delete(s.sessions, token)
	c, ok := s.connections[id]
	if ok && c.Token == token {
		delete(s.connections, id)
	}
	s.mu.Unlock()
	if ok && c.Token == token {
		_ = c.Socket.CloseNow()
	}
	s.reply(w, 200, Response{Code: "OK"})
}

func (s *Server) Close() {
	s.mu.Lock()
	connections := s.connections
	s.connections = map[string]connection{}
	s.sessions = map[string]session{}
	s.mu.Unlock()
	for _, c := range connections {
		_ = c.Socket.CloseNow()
	}
}

func (s *Server) reply(w http.ResponseWriter, status int, response Response) {
	response.Type = "response"
	response.ServerTimeMS = time.Now().UnixMilli()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func errorResponse(err error) Response {
	messages := map[error]string{
		ErrInvalid: "参数格式不正确", ErrDuplicate: "账号已存在", ErrCredentials: "账号或密码错误",
		ErrUnauthenticated: "请重新登录", ErrCoins: "金币不足", ErrItems: "物品不足",
		ErrPlot: "地块状态不允许此操作", ErrNotMature: "作物尚未成熟", ErrTask: "请先完成任务",
		ErrMailNotFound: "邮件不存在", ErrFriendNotFound: "好友不存在", ErrAlreadyFriends: "已经是好友",
	}
	for code, message := range messages {
		if errors.Is(err, code) {
			return Response{Code: code.Error(), Message: message}
		}
	}
	return Response{Code: "SERVICE_UNAVAILABLE", Message: "服务暂时不可用"}
}

func (s *Server) failHTTP(w http.ResponseWriter, err error) {
	response := errorResponse(err)
	status := http.StatusBadRequest
	if response.Code == "INVALID_CREDENTIALS" || response.Code == "UNAUTHENTICATED" {
		status = http.StatusUnauthorized
	}
	if response.Code == "ACCOUNT_EXISTS" || response.Code == "ALREADY_FRIENDS" {
		status = http.StatusConflict
	}
	if response.Code == "SERVICE_UNAVAILABLE" {
		status = http.StatusServiceUnavailable
	}
	s.reply(w, status, response)
}

func operationContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 5*time.Second)
}
