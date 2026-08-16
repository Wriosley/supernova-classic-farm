package main

import (
        "bytes"
        "context"
        "fmt"
        "io"
        "net/http"
        "net/http/cookiejar"
        "net/url"
        "os"
        "time"

        "github.com/coder/websocket"
        httpv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/http"
        wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
        "google.golang.org/protobuf/proto"
)

func main() {
        loginURL := env("LOGIN_URL", "http://127.0.0.1:31238")
        gateWS := env("GATE_WS", "ws://127.0.0.1:32591/ws")
        origin := env("ORIGIN", "http://21.130.223.195:1616")
        account := env("ACCOUNT", "test1234")
        password := env("PASSWORD", "test1234")

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	csrf := &httpv1.CsrfResponse{}
	mustProto(client, http.MethodGet, loginURL+"/v1/auth/csrf", origin, "", nil, csrf)
	loginReq := &httpv1.LoginRequest{AccountName: account, Password: password}
	loginResp := &httpv1.LoginResponse{}
	var playerID uint64
	if err := doProto(client, http.MethodPost, loginURL+"/v1/auth/login", origin, csrf.CsrfToken, loginReq, loginResp); err != nil {
		fmt.Printf("login failed: %v\n", err)
		// try register
		csrf2 := &httpv1.CsrfResponse{}
		mustProto(client, http.MethodGet, loginURL+"/v1/auth/csrf", origin, "", nil, csrf2)
		reg := &httpv1.RegisterRequest{AccountName: account, Password: password}
		regResp := &httpv1.RegisterResponse{}
		if err := doProto(client, http.MethodPost, loginURL+"/v1/auth/register", origin, csrf2.CsrfToken, reg, regResp); err != nil {
			fatalf("register failed: %v", err)
		}
		playerID = regResp.GetSession().GetPlayerId()
		fmt.Printf("registered player_id=%d\n", playerID)
		csrf = csrf2
	} else {
		playerID = loginResp.GetSession().GetPlayerId()
	}
	fmt.Printf("login ok player_id=%d\n", playerID)

	csrfT := &httpv1.CsrfResponse{}
	mustProto(client, http.MethodGet, loginURL+"/v1/auth/csrf", origin, "", nil, csrfT)
	ticketResp := &httpv1.WsTicketResponse{}
	mustProto(client, http.MethodPost, loginURL+"/v1/ws-tickets", origin, csrfT.CsrfToken, &httpv1.WsTicketRequest{TicketRequestId: "11111111-1111-4111-8111-111111111111", GatewayId: env("GATEWAY_ID", "local-gateway")}, ticketResp)
	fmt.Printf("ticket len=%d\n", len(ticketResp.WsTicket))

        ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
        defer cancel()
        conn, _, err := websocket.Dial(ctx, gateWS, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{origin}}})
        if err != nil {
                fatalf("ws dial: %v", err)
        }
        defer conn.Close(websocket.StatusNormalClosure, "")
        conn.SetReadLimit(1 << 20)

        auth := &wsv1.WsEnvelope{
                ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST, Action: wsv1.Action_AUTH,
                RequestId: "auth-1", Payload: &wsv1.WsEnvelope_AuthRequest{AuthRequest: &wsv1.AuthRequest{WsTicket: ticketResp.WsTicket}},
        }
        writeWS(ctx, conn, auth)
        authResp := readWS(ctx, conn)
        fmt.Printf("auth error=%v player=%d\n", authResp.GetError(), authResp.GetAuthResponse().GetPlayerId())

	snap := &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST, Action: wsv1.Action_GET_PLAYER_SNAPSHOT,
		RequestId: "snap-1", TargetPlayerId: playerID,
		Payload: &wsv1.WsEnvelope_GetPlayerSnapshotRequest{GetPlayerSnapshotRequest: &wsv1.GetPlayerSnapshotRequest{}},
	}
        writeWS(ctx, conn, snap)
        snapResp := readWS(ctx, conn)
        if snapResp.GetError() != nil {
                fmt.Printf("SNAPSHOT FAIL code=%v retryable=%v\n", snapResp.GetError().GetCode(), snapResp.GetError().GetRetryable())
                os.Exit(2)
        }
        fmt.Printf("SNAPSHOT OK player=%d plots=%d coins=%d\n",
                snapResp.GetGetPlayerSnapshotResponse().GetSnapshot().GetPlayerId(),
                len(snapResp.GetGetPlayerSnapshotResponse().GetSnapshot().GetPlots()),
                snapResp.GetGetPlayerSnapshotResponse().GetSnapshot().GetCoinBalance())
}

func env(k, d string) string {
        if v := os.Getenv(k); v != "" {
                return v
        }
        return d
}
func fatalf(f string, a ...any) { fmt.Printf(f+"\n", a...); os.Exit(1) }
func mustProto(c *http.Client, method, rawURL, origin, csrf string, in, out proto.Message) {
        if err := doProto(c, method, rawURL, origin, csrf, in, out); err != nil {
                fatalf("%s %s: %v", method, rawURL, err)
        }
}
func doProto(c *http.Client, method, rawURL, origin, csrf string, in, out proto.Message) error {
        var body io.Reader
        if in != nil {
                b, err := proto.Marshal(in)
                if err != nil {
                        return err
                }
                body = bytes.NewReader(b)
        }
        req, err := http.NewRequest(method, rawURL, body)
        if err != nil {
                return err
        }
        req.Header.Set("Origin", origin)
        req.Header.Set("Content-Type", "application/x-protobuf")
        req.Header.Set("Accept", "application/x-protobuf")
        if csrf != "" {
                req.Header.Set("X-CSRF-Token", csrf)
        }
        // also set cookie jar host
        if u, err := url.Parse(rawURL); err == nil {
                _ = u
        }
        resp, err := c.Do(req)
        if err != nil {
                return err
        }
        defer resp.Body.Close()
        raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
        if resp.StatusCode >= 300 {
		he := &httpv1.HttpError{}
		_ = proto.Unmarshal(raw, he)
		return fmt.Errorf("status=%d code=%v debug=%q", resp.StatusCode, he.GetCode(), he.GetDebugMessage())
        }
        if out != nil {
                return proto.Unmarshal(raw, out)
        }
        return nil
}
func writeWS(ctx context.Context, c *websocket.Conn, msg *wsv1.WsEnvelope) {
        b, err := proto.Marshal(msg)
        if err != nil {
                fatalf("marshal: %v", err)
        }
        if err := c.Write(ctx, websocket.MessageBinary, b); err != nil {
                fatalf("ws write: %v", err)
        }
}
func readWS(ctx context.Context, c *websocket.Conn) *wsv1.WsEnvelope {
        _, data, err := c.Read(ctx)
        if err != nil {
                fatalf("ws read: %v", err)
        }
        msg := &wsv1.WsEnvelope{}
        if err := proto.Unmarshal(data, msg); err != nil {
                fatalf("ws unmarshal: %v", err)
        }
        return msg
}
