package game

// 本文件管理 WebSocket 生命周期、AUTH 身份绑定和 JSON 指令分发。

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"net/http"
	"strconv"
	"time"
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
	// 浏览器通过 Vite 同源代理连接；Qt 客户端可直接连接 8080。
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
	for {
		timeout := 60 * time.Second
		if playerID == "" {
			// 第一条消息必须在 5 秒内完成 AUTH，之后连接始终绑定这个玩家。
			timeout = 5 * time.Second
		}
		readCtx, cancel := context.WithTimeout(ctx, timeout)
		typ, body, err := c.Read(readCtx)
		cancel()
		if err != nil {
			return
		}
		var command Command
		if typ != websocket.MessageText || decodeJSON(body, &command) != nil || !requestPattern.MatchString(command.RequestID) {
			_ = writeWS(ctx, c, errorResponse(ErrInvalid))
			return
		}
		response := Response{Code: "OK", RequestID: command.RequestID, Action: command.Action}
		if playerID == "" {
			if command.Action != "AUTH" {
				err = ErrUnauthenticated
			} else if command.Data.Token == "" || command.Data.PlotID != 0 || command.Data.Quantity != 0 || command.Data.MailID != "" {
				err = ErrInvalid
			} else {
				// 根据 token 查询玩家身份。
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
			// 认证成功后保存连接。
			s.mu.Lock()
			old := s.connections[playerID]
			s.connections[playerID] = connection{token, c}
			s.mu.Unlock()
			if old.Socket != nil && old.Socket != c {
				// 每个玩家只保留最新连接，避免两个界面同时操作造成理解困难。
				_ = old.Socket.CloseNow()
			}
			response.PlayerID = playerID
		} else {
			if _, err = s.identity(token); err != nil {
				response = errorResponse(err)
				response.RequestID = command.RequestID
				_ = writeWS(ctx, c, response)
				return
			}
			operationCtx, operationCancel := context.WithTimeout(ctx, 5*time.Second)
			// playerID 只取自已认证连接，客户端不能指定要查询或修改的玩家。
			switch command.Action {
			case "GET_MAILBOX":
				if command.Data != (Args{}) {
					err = ErrInvalid
				} else {
					response.Mails, err = s.engine.Store.ListMails(operationCtx, playerID)
				}
			case "READ_MAIL":
				_, parseErr := strconv.ParseUint(command.Data.MailID, 10, 64)
				if parseErr != nil || command.Data.MailID == "0" || command.Data.PlotID != 0 || command.Data.Quantity != 0 || command.Data.Token != "" {
					err = ErrInvalid
				} else {
					response.Mails, err = s.engine.Store.MarkMailRead(operationCtx, playerID, command.Data.MailID)
				}
			default:
				var state State
				state, err = s.engine.Execute(operationCtx, playerID, command)
				if err == nil {
					response.Snapshot = &state
					cfg := GameConfig()
					response.Config = &cfg
				}
			}
			operationCancel()
			if err != nil {
				response = errorResponse(err)
				response.RequestID = command.RequestID
				response.Action = command.Action
			}
		}
		if err = writeWS(ctx, c, response); err != nil {
			return
		}
	}
}
