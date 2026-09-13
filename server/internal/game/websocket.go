package game

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
)

func writeWS(ctx context.Context, c *websocket.Conn, response Response) error {
	response.Type = "response"
	response.ServerTimeMS = time.Now().UnixMilli()
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.Write(writeCtx, websocket.MessageText, body)
}

func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(4096)
	ctx := r.Context()
	var playerID, token string
	defer func() {
		s.mu.Lock()
		if current, ok := s.connections[playerID]; ok && current.Socket == c {
			delete(s.connections, playerID)
		}
		s.mu.Unlock()
	}()

	// 每个连接有自己的读取循环，同一连接内的命令按到达顺序执行。
	for {
		timeout := 60 * time.Second
		if playerID == "" {
			timeout = 5 * time.Second
		}
		readCtx, cancel := context.WithTimeout(ctx, timeout)
		typ, body, err := c.Read(readCtx)
		cancel()
		if err != nil {
			return
		}

		var command Command
		if typ != websocket.MessageText || json.Unmarshal(body, &command) != nil || !requestPattern.MatchString(command.RequestID) {
			_ = writeWS(ctx, c, errorResponse(ErrInvalid))
			return
		}
		response := Response{Code: "OK", RequestID: command.RequestID, Action: command.Action}

		if playerID == "" {
			if command.Action != "AUTH" || command.Data.Token == "" {
				err = ErrUnauthenticated
			} else {
				token = command.Data.Token
				playerID, err = s.identity(token)
			}
			if err != nil {
				response = errorResponse(err)
				response.RequestID = command.RequestID
				response.Action = command.Action
				_ = writeWS(ctx, c, response)
				return
			}
			s.mu.Lock()
			old := s.connections[playerID]
			s.connections[playerID] = connection{Token: token, Socket: c}
			s.mu.Unlock()
			if old.Socket != nil && old.Socket != c {
				_ = old.Socket.CloseNow()
			}
			response.PlayerID = playerID
		} else {
			if _, err = s.identity(token); err != nil {
				response = errorResponse(err)
				response.RequestID = command.RequestID
				response.Action = command.Action
				_ = writeWS(ctx, c, response)
				return
			}
			opCtx, opCancel := operationContext(ctx)
			switch command.Action {
			case "GET_MAILBOX":
				response.Mails, err = listMails(opCtx, s.db, playerID)
			case "READ_MAIL":
				_, parseErr := strconv.ParseUint(command.Data.MailID, 10, 64)
				if parseErr != nil || command.Data.MailID == "0" {
					err = ErrInvalid
				} else {
					response.Mails, err = markMailRead(opCtx, s.db, playerID, command.Data.MailID)
				}
			default:
				var state State
				state, err = executeGame(opCtx, s.db, playerID, command)
				if err == nil {
					response.Snapshot = &state
					cfg := GameConfig()
					response.Config = &cfg
				}
			}
			opCancel()
			if err != nil {
				response = errorResponse(err)
				response.RequestID = command.RequestID
				response.Action = command.Action
			}
		}
		if writeWS(ctx, c, response) != nil {
			return
		}
	}
}
