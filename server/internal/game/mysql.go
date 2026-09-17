package game

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
)

// InitTables 只补齐表和固定配置，不删除已有数据。
func InitTables(ctx context.Context, db *sql.DB) error {
	// 启动时只负责“表不存在则创建”；需要丢弃旧数据时运行 reset_class_mid.sql。
	queries := []string{
		`CREATE TABLE IF NOT EXISTS class_mid_accounts (player_id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, username VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE, password VARCHAR(64) NOT NULL) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS class_mid_players (player_id BIGINT UNSIGNED PRIMARY KEY, coins INT NOT NULL DEFAULT 50, fertilizer INT NOT NULL DEFAULT 2, chapter INT NOT NULL DEFAULT 1, state_version BIGINT UNSIGNED NOT NULL DEFAULT 1, FOREIGN KEY (player_id) REFERENCES class_mid_accounts(player_id) ON DELETE CASCADE) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS class_mid_crops (crop_id INT PRIMARY KEY, crop_name VARCHAR(30) NOT NULL, seed_price INT NOT NULL, sale_price INT NOT NULL, mature_seconds INT NOT NULL, yield_count INT NOT NULL) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS class_mid_inventory (player_id BIGINT UNSIGNED NOT NULL, crop_id INT NOT NULL, seed_count INT NOT NULL DEFAULT 0, crop_count INT NOT NULL DEFAULT 0, PRIMARY KEY (player_id,crop_id), FOREIGN KEY (player_id) REFERENCES class_mid_players(player_id) ON DELETE CASCADE, FOREIGN KEY (crop_id) REFERENCES class_mid_crops(crop_id)) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS class_mid_plots (player_id BIGINT UNSIGNED NOT NULL, plot_no INT NOT NULL, crop_id INT NULL, status VARCHAR(20) NOT NULL DEFAULT 'EMPTY', planted_at_ms BIGINT NOT NULL DEFAULT 0, mature_at_ms BIGINT NOT NULL DEFAULT 0, fertilized BOOLEAN NOT NULL DEFAULT FALSE, PRIMARY KEY (player_id,plot_no), FOREIGN KEY (player_id) REFERENCES class_mid_players(player_id) ON DELETE CASCADE, FOREIGN KEY (crop_id) REFERENCES class_mid_crops(crop_id)) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS class_mid_tasks (task_id INT PRIMARY KEY, action VARCHAR(40) NOT NULL UNIQUE, label VARCHAR(100) NOT NULL, target_count INT NOT NULL, reward_coins INT NOT NULL DEFAULT 0) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS class_mid_player_tasks (player_id BIGINT UNSIGNED NOT NULL, task_id INT NOT NULL, current_count INT NOT NULL DEFAULT 0, is_claimed BOOLEAN NOT NULL DEFAULT FALSE, PRIMARY KEY (player_id,task_id), FOREIGN KEY (player_id) REFERENCES class_mid_players(player_id) ON DELETE CASCADE, FOREIGN KEY (task_id) REFERENCES class_mid_tasks(task_id)) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS class_mid_mails (mail_id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, sender_id BIGINT UNSIGNED NULL, receiver_id BIGINT UNSIGNED NOT NULL, title VARCHAR(100) NOT NULL, content TEXT NOT NULL, is_read BOOLEAN NOT NULL DEFAULT FALSE, created_at_ms BIGINT NOT NULL, INDEX idx_class_mid_mails_receiver (receiver_id,mail_id), FOREIGN KEY (sender_id) REFERENCES class_mid_players(player_id) ON DELETE SET NULL, FOREIGN KEY (receiver_id) REFERENCES class_mid_players(player_id) ON DELETE CASCADE) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS class_mid_friends (player_id BIGINT UNSIGNED NOT NULL, friend_id BIGINT UNSIGNED NOT NULL, created_at_ms BIGINT NOT NULL, PRIMARY KEY (player_id,friend_id), FOREIGN KEY (player_id) REFERENCES class_mid_players(player_id) ON DELETE CASCADE, FOREIGN KEY (friend_id) REFERENCES class_mid_players(player_id) ON DELETE CASCADE) ENGINE=InnoDB`,
	}
	for _, query := range queries {
		if _, err := db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	_, err := db.ExecContext(ctx, `INSERT INTO class_mid_crops (crop_id,crop_name,seed_price,sale_price,mature_seconds,yield_count) VALUES
		(1,'胡萝卜',2,5,30,3),(2,'玉米',3,7,35,3),(3,'土豆',4,9,40,4),(4,'番茄',5,11,45,4),(5,'草莓',6,14,50,5),(6,'南瓜',8,18,60,5)
		ON DUPLICATE KEY UPDATE crop_name=VALUES(crop_name),seed_price=VALUES(seed_price),sale_price=VALUES(sale_price),mature_seconds=VALUES(mature_seconds),yield_count=VALUES(yield_count)`)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO class_mid_tasks (task_id,action,label,target_count,reward_coins) VALUES
		(1,'BUY_SEEDS','购买3颗种子',3,0),(2,'PLANT','种植1次',1,0),(3,'APPLY_FERTILIZER','施肥1次',1,0),(4,'HARVEST','收获1次',1,0),(5,'SELL_CROP','出售1份作物',1,10)
		ON DUPLICATE KEY UPDATE action=VALUES(action),label=VALUES(label),target_count=VALUES(target_count),reward_coins=VALUES(reward_coins)`)
	return err
}

// createPlayer 在一个事务中创建账号、玩家、16块地、仓库、任务和欢迎邮件。
func createPlayer(ctx context.Context, db *sql.DB, username, password string) (string, error) {
	// 注册涉及多张表，统一放进事务，避免只创建一半的玩家数据。
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO class_mid_accounts (username,password) VALUES (?,?)`, username, encodePassword(password))
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return "", ErrDuplicate
		}
		return "", err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO class_mid_players (player_id) VALUES (?)`, id); err != nil {
		return "", err
	}
	for plot := 1; plot <= 16; plot++ {
		if _, err = tx.ExecContext(ctx, `INSERT INTO class_mid_plots (player_id,plot_no) VALUES (?,?)`, id, plot); err != nil {
			return "", err
		}
	}
	for crop := 1; crop <= 6; crop++ {
		if _, err = tx.ExecContext(ctx, `INSERT INTO class_mid_inventory (player_id,crop_id) VALUES (?,?)`, id, crop); err != nil {
			return "", err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO class_mid_player_tasks (player_id,task_id) SELECT ?,task_id FROM class_mid_tasks`, id); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO class_mid_mails (sender_id,receiver_id,title,content,created_at_ms) VALUES (NULL,?,?,?,?)`, id, WelcomeMailTitle, WelcomeMailContent, time.Now().UnixMilli()); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return strconv.FormatInt(id, 10), nil
}

func findAccount(ctx context.Context, db *sql.DB, username string) (Account, error) {
	var a Account
	err := db.QueryRowContext(ctx, `SELECT player_id,username,password FROM class_mid_accounts WHERE username=?`, username).Scan(&a.ID, &a.Username, &a.Password)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrCredentials
	}
	return a, err
}

func loadState(ctx context.Context, db *sql.DB, playerID string) (State, error) {
	// 快照由关系表实时拼装，客户端不用分别请求金币、仓库和农田。
	var state State
	state.PlayerID = playerID
	if err := db.QueryRowContext(ctx, `SELECT coins,fertilizer,chapter,state_version FROM class_mid_players WHERE player_id=?`, playerID).Scan(&state.Coins, &state.Fertilizer, &state.Chapter, &state.Version); err != nil {
		return state, err
	}

	rows, err := db.QueryContext(ctx, `SELECT i.crop_id,c.crop_name,i.seed_count,i.crop_count,c.seed_price,c.sale_price,c.mature_seconds,c.yield_count FROM class_mid_inventory i JOIN class_mid_crops c ON c.crop_id=i.crop_id WHERE i.player_id=? ORDER BY i.crop_id`, playerID)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		var item InventoryItem
		var crop Crop
		if err = rows.Scan(&item.CropID, &item.CropName, &item.SeedCount, &item.CropCount, &crop.SeedPrice, &crop.SalePrice, &crop.MatureSeconds, &crop.Yield); err != nil {
			rows.Close()
			return state, err
		}
		crop.ID, crop.Name = item.CropID, item.CropName
		state.Inventory = append(state.Inventory, item)
		state.Shop = append(state.Shop, crop)
		if item.CropID == 1 {
			state.Seeds, state.Crops = item.SeedCount, item.CropCount
		}
	}
	if err = rows.Close(); err != nil {
		return state, err
	}

	state.Plots, err = loadPlots(ctx, db, playerID)
	if err != nil {
		return state, err
	}
	rows, err = db.QueryContext(ctx, `SELECT t.action,t.label,p.current_count,t.target_count FROM class_mid_player_tasks p JOIN class_mid_tasks t ON t.task_id=p.task_id WHERE p.player_id=? ORDER BY t.task_id`, playerID)
	if err != nil {
		return state, err
	}
	defer rows.Close()
	for rows.Next() {
		var task Task
		if err = rows.Scan(&task.Action, &task.Label, &task.Current, &task.Target); err != nil {
			return state, err
		}
		state.Tasks = append(state.Tasks, task)
	}
	return state, rows.Err()
}

func loadPlots(ctx context.Context, db *sql.DB, playerID string) ([]Plot, error) {
	rows, err := db.QueryContext(ctx, `SELECT p.plot_no,COALESCE(p.crop_id,0),COALESCE(c.crop_name,''),p.status,p.planted_at_ms,p.mature_at_ms,p.fertilized FROM class_mid_plots p LEFT JOIN class_mid_crops c ON c.crop_id=p.crop_id WHERE p.player_id=? ORDER BY p.plot_no`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var plots []Plot
	now := time.Now().UnixMilli()
	for rows.Next() {
		var p Plot
		if err = rows.Scan(&p.ID, &p.CropID, &p.CropName, &p.Status, &p.PlantedAtMS, &p.MatureAtMS, &p.Fertilized); err != nil {
			return nil, err
		}
		if p.Status == "GROWING" && now >= p.MatureAtMS {
			p.Status = "MATURE"
		}
		plots = append(plots, p)
	}
	return plots, rows.Err()
}

func listMails(ctx context.Context, db *sql.DB, playerID string) ([]Mail, error) {
	rows, err := db.QueryContext(ctx, `SELECT m.mail_id,COALESCE(m.sender_id,0),COALESCE(a.username,''),m.title,m.content,m.is_read,m.created_at_ms FROM class_mid_mails m LEFT JOIN class_mid_accounts a ON a.player_id=m.sender_id WHERE m.receiver_id=? ORDER BY m.mail_id DESC`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var mails []Mail
	for rows.Next() {
		var m Mail
		if err = rows.Scan(&m.ID, &m.SenderID, &m.SenderUsername, &m.Title, &m.Content, &m.IsRead, &m.CreatedAtMS); err != nil {
			return nil, err
		}
		if m.SenderID == "0" {
			m.SenderID = ""
		}
		mails = append(mails, m)
	}
	return mails, rows.Err()
}

func markMailRead(ctx context.Context, db *sql.DB, playerID, mailID string) ([]Mail, error) {
	result, err := db.ExecContext(ctx, `UPDATE class_mid_mails SET is_read=TRUE WHERE mail_id=? AND receiver_id=?`, mailID, playerID)
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count == 0 {
		var exists int
		if db.QueryRowContext(ctx, `SELECT COUNT(*) FROM class_mid_mails WHERE mail_id=? AND receiver_id=?`, mailID, playerID).Scan(&exists) != nil || exists == 0 {
			return nil, ErrMailNotFound
		}
	}
	return listMails(ctx, db, playerID)
}
