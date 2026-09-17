package game

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
)

type addFriendRequest struct {
	Username string `json:"username"`
}
type stealRequest struct {
	PlotID int `json:"plot_id"`
}
type friendMailRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

func (s *Server) addFriendHTTP(w http.ResponseWriter, r *http.Request) {
	playerID, ok := s.requirePlayer(w, r)
	if !ok {
		return
	}
	var body addFriendRequest
	if decodeJSON(r, &body) != nil || body.Username == "" {
		s.failHTTP(w, ErrInvalid)
		return
	}
	friendID, err := addFriend(r.Context(), s.db, playerID, body.Username)
	if err != nil {
		s.failHTTP(w, err)
		return
	}
	s.reply(w, 200, Response{Code: "OK", FriendID: friendID})
}

func (s *Server) listFriendsHTTP(w http.ResponseWriter, r *http.Request) {
	playerID, ok := s.requirePlayer(w, r)
	if !ok {
		return
	}
	friends, err := listFriends(r.Context(), s.db, playerID)
	if err != nil {
		s.failHTTP(w, err)
		return
	}
	s.reply(w, 200, Response{Code: "OK", Friends: friends})
}

func (s *Server) friendFarmHTTP(w http.ResponseWriter, r *http.Request) {
	playerID, ok := s.requirePlayer(w, r)
	if !ok {
		return
	}
	friendID := r.PathValue("id")
	if !validID(friendID) {
		s.failHTTP(w, ErrInvalid)
		return
	}
	plots, err := loadFriendFarm(r.Context(), s.db, playerID, friendID)
	if err != nil {
		s.failHTTP(w, err)
		return
	}
	s.reply(w, 200, Response{Code: "OK", FriendID: friendID, Plots: plots})
}

func (s *Server) stealHTTP(w http.ResponseWriter, r *http.Request) {
	playerID, ok := s.requirePlayer(w, r)
	if !ok {
		return
	}
	friendID := r.PathValue("id")
	var body stealRequest
	if !validID(friendID) || decodeJSON(r, &body) != nil || body.PlotID < 1 || body.PlotID > 16 {
		s.failHTTP(w, ErrInvalid)
		return
	}
	if err := stealCrop(r.Context(), s.db, playerID, friendID, body.PlotID); err != nil {
		s.failHTTP(w, err)
		return
	}
	state, err := loadState(r.Context(), s.db, playerID)
	if err != nil {
		s.failHTTP(w, err)
		return
	}
	s.reply(w, 200, Response{Code: "OK", FriendID: friendID, Snapshot: &state})
}

func (s *Server) friendMailHTTP(w http.ResponseWriter, r *http.Request) {
	playerID, ok := s.requirePlayer(w, r)
	if !ok {
		return
	}
	friendID := r.PathValue("id")
	var body friendMailRequest
	if !validID(friendID) || decodeJSON(r, &body) != nil || len(body.Title) < 1 || len(body.Title) > 100 || len(body.Content) < 1 || len(body.Content) > 1000 {
		s.failHTTP(w, ErrInvalid)
		return
	}
	mail, err := sendFriendMail(r.Context(), s.db, playerID, friendID, body.Title, body.Content)
	if err != nil {
		s.failHTTP(w, err)
		return
	}
	// 只返回刚写入的这一封，不能把 B 的其他邮件暴露给 A。
	s.reply(w, 200, Response{Code: "OK", FriendID: friendID, Mails: []Mail{mail}})
}

func validID(id string) bool { _, err := strconv.ParseUint(id, 10, 64); return err == nil && id != "0" }

func addFriend(ctx context.Context, db *sql.DB, playerID, username string) (string, error) {
	// 一次插入两个方向，A 和 B 查询自己的好友列表时都很简单。
	var friendID string
	if err := db.QueryRowContext(ctx, `SELECT player_id FROM class_mid_accounts WHERE username=?`, username).Scan(&friendID); errors.Is(err, sql.ErrNoRows) {
		return "", ErrFriendNotFound
	} else if err != nil {
		return "", err
	}
	if friendID == playerID {
		return "", ErrInvalid
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	if _, err = tx.ExecContext(ctx, `INSERT INTO class_mid_friends (player_id,friend_id,created_at_ms) VALUES (?,?,?)`, playerID, friendID, now); err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return "", ErrAlreadyFriends
		}
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO class_mid_friends (player_id,friend_id,created_at_ms) VALUES (?,?,?)`, friendID, playerID, now); err != nil {
		return "", err
	}
	return friendID, tx.Commit()
}

func listFriends(ctx context.Context, db *sql.DB, playerID string) ([]Friend, error) {
	rows, err := db.QueryContext(ctx, `SELECT a.player_id,a.username FROM class_mid_friends f JOIN class_mid_accounts a ON a.player_id=f.friend_id WHERE f.player_id=? ORDER BY a.username`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var friends []Friend
	for rows.Next() {
		var f Friend
		if err = rows.Scan(&f.PlayerID, &f.Username); err != nil {
			return nil, err
		}
		friends = append(friends, f)
	}
	return friends, rows.Err()
}

func areFriends(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, playerID, friendID string) error {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM class_mid_friends WHERE player_id=? AND friend_id=?`, playerID, friendID).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return ErrFriendNotFound
	}
	return nil
}

func loadFriendFarm(ctx context.Context, db *sql.DB, playerID, friendID string) ([]Plot, error) {
	if err := areFriends(ctx, db, playerID, friendID); err != nil {
		return nil, err
	}
	return loadPlots(ctx, db, friendID)
}

func stealCrop(ctx context.Context, db *sql.DB, playerID, friendID string, plotID int) error {
	// 锁住好友的目标地块，保证成熟作物最多被一个请求偷走。
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = areFriends(ctx, tx, playerID, friendID); err != nil {
		return err
	}
	var cropID, yield int
	var status string
	var mature int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(p.crop_id,0),p.status,p.mature_at_ms,COALESCE(c.yield_count,0) FROM class_mid_plots p LEFT JOIN class_mid_crops c ON c.crop_id=p.crop_id WHERE p.player_id=? AND p.plot_no=? FOR UPDATE`, friendID, plotID).Scan(&cropID, &status, &mature, &yield); err != nil {
		return err
	}
	if status != "GROWING" {
		return ErrPlot
	}
	if time.Now().UnixMilli() < mature {
		return ErrNotMature
	}
	if _, err = tx.ExecContext(ctx, `UPDATE class_mid_inventory SET crop_count=crop_count+? WHERE player_id=? AND crop_id=?`, yield, playerID, cropID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE class_mid_players SET coins=coins+1,state_version=state_version+1 WHERE player_id=?`, playerID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE class_mid_players SET state_version=state_version+1 WHERE player_id=?`, friendID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE class_mid_plots SET status='NEED_CLEANUP' WHERE player_id=? AND plot_no=?`, friendID, plotID); err != nil {
		return err
	}
	return tx.Commit()
}

func sendFriendMail(ctx context.Context, db *sql.DB, playerID, friendID, title, content string) (Mail, error) {
	if err := areFriends(ctx, db, playerID, friendID); err != nil {
		return Mail{}, err
	}
	createdAt := time.Now().UnixMilli()
	result, err := db.ExecContext(ctx, `INSERT INTO class_mid_mails (sender_id,receiver_id,title,content,created_at_ms) VALUES (?,?,?,?,?)`, playerID, friendID, title, content, createdAt)
	if err != nil {
		return Mail{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Mail{}, err
	}
	var mail Mail
	err = db.QueryRowContext(ctx, `SELECT m.mail_id,m.sender_id,a.username,m.title,m.content,m.is_read,m.created_at_ms FROM class_mid_mails m JOIN class_mid_accounts a ON a.player_id=m.sender_id WHERE m.mail_id=? AND m.receiver_id=?`, id, friendID).Scan(&mail.ID, &mail.SenderID, &mail.SenderUsername, &mail.Title, &mail.Content, &mail.IsRead, &mail.CreatedAtMS)
	return mail, err
}
