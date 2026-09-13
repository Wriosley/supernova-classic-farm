package game

import (
	"context"
	"database/sql"
	"time"
)

func executeGame(ctx context.Context, db *sql.DB, playerID string, command Command) (State, error) {
	// 查询命令直接读取；会改变数据的命令统一使用 MySQL 事务。
	if !requestPattern.MatchString(command.RequestID) || command.Data.Token != "" || command.Data.MailID != "" {
		return State{}, ErrInvalid
	}
	if command.Action == "PING" || command.Action == "GET_PLAYER_SNAPSHOT" || command.Action == "GET_SHOP" {
		return loadState(ctx, db, playerID)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return State{}, err
	}
	defer tx.Rollback()
	cropID := command.Data.CropID
	if cropID == 0 {
		cropID = 1
	}

	switch command.Action {
	case "BUY_SEEDS":
		err = buySeeds(ctx, tx, playerID, cropID, command.Data.Quantity)
	case "BUY_FERTILIZER":
		err = buyFertilizer(ctx, tx, playerID, command.Data.Quantity)
	case "PLANT":
		err = plant(ctx, tx, playerID, command.Data.PlotID, cropID)
	case "APPLY_FERTILIZER":
		err = fertilize(ctx, tx, playerID, command.Data.PlotID)
	case "HARVEST":
		err = harvest(ctx, tx, playerID, command.Data.PlotID)
	case "CLEAN_PLOT":
		err = cleanPlot(ctx, tx, playerID, command.Data.PlotID)
	case "SELL_CROP":
		err = sellCrop(ctx, tx, playerID, cropID, command.Data.Quantity)
	case "CLAIM_CHAPTER_REWARD":
		err = claimReward(ctx, tx, playerID)
	default:
		err = ErrInvalid
	}
	if err != nil {
		return State{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE class_mid_players SET state_version=state_version+1 WHERE player_id=?`, playerID); err != nil {
		return State{}, err
	}
	if err = tx.Commit(); err != nil {
		return State{}, err
	}
	return loadState(ctx, db, playerID)
}

func buySeeds(ctx context.Context, tx *sql.Tx, playerID string, cropID, quantity int) error {
	if cropID < 1 || cropID > 6 || quantity < 1 || quantity > 100 {
		return ErrInvalid
	}
	var price int
	if err := tx.QueryRowContext(ctx, `SELECT seed_price FROM class_mid_crops WHERE crop_id=?`, cropID).Scan(&price); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE class_mid_players SET coins=coins-? WHERE player_id=? AND coins>=?`, price*quantity, playerID, price*quantity)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrCoins
	}
	if _, err = tx.ExecContext(ctx, `UPDATE class_mid_inventory SET seed_count=seed_count+? WHERE player_id=? AND crop_id=?`, quantity, playerID, cropID); err != nil {
		return err
	}
	return addTaskProgress(ctx, tx, playerID, "BUY_SEEDS", quantity)
}

func buyFertilizer(ctx context.Context, tx *sql.Tx, playerID string, quantity int) error {
	if quantity < 1 || quantity > 100 {
		return ErrInvalid
	}
	cost := quantity * 2
	result, err := tx.ExecContext(ctx, `UPDATE class_mid_players SET coins=coins-?,fertilizer=fertilizer+? WHERE player_id=? AND coins>=?`, cost, quantity, playerID, cost)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrCoins
	}
	return nil
}

func plant(ctx context.Context, tx *sql.Tx, playerID string, plotID, cropID int) error {
	if plotID < 1 || plotID > 16 || cropID < 1 || cropID > 6 {
		return ErrInvalid
	}
	var status string
	// FOR UPDATE 锁住这一块地，防止两个请求同时种植。
	if err := tx.QueryRowContext(ctx, `SELECT status FROM class_mid_plots WHERE player_id=? AND plot_no=? FOR UPDATE`, playerID, plotID).Scan(&status); err != nil {
		return err
	}
	if status != "EMPTY" {
		return ErrPlot
	}
	var seeds, seconds int
	if err := tx.QueryRowContext(ctx, `SELECT seed_count FROM class_mid_inventory WHERE player_id=? AND crop_id=? FOR UPDATE`, playerID, cropID).Scan(&seeds); err != nil {
		return err
	}
	if seeds < 1 {
		return ErrItems
	}
	if err := tx.QueryRowContext(ctx, `SELECT mature_seconds FROM class_mid_crops WHERE crop_id=?`, cropID).Scan(&seconds); err != nil {
		return err
	}
	now := time.Now()
	if _, err := tx.ExecContext(ctx, `UPDATE class_mid_inventory SET seed_count=seed_count-1 WHERE player_id=? AND crop_id=?`, playerID, cropID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE class_mid_plots SET crop_id=?,status='GROWING',planted_at_ms=?,mature_at_ms=?,fertilized=FALSE WHERE player_id=? AND plot_no=?`, cropID, now.UnixMilli(), now.Add(time.Duration(seconds)*time.Second).UnixMilli(), playerID, plotID); err != nil {
		return err
	}
	return addTaskProgress(ctx, tx, playerID, "PLANT", 1)
}

func fertilize(ctx context.Context, tx *sql.Tx, playerID string, plotID int) error {
	if plotID < 1 || plotID > 16 {
		return ErrInvalid
	}
	var status string
	var mature int64
	var used bool
	if err := tx.QueryRowContext(ctx, `SELECT status,mature_at_ms,fertilized FROM class_mid_plots WHERE player_id=? AND plot_no=? FOR UPDATE`, playerID, plotID).Scan(&status, &mature, &used); err != nil {
		return err
	}
	if status != "GROWING" || used || time.Now().UnixMilli() >= mature {
		return ErrPlot
	}
	result, err := tx.ExecContext(ctx, `UPDATE class_mid_players SET fertilizer=fertilizer-1 WHERE player_id=? AND fertilizer>0`, playerID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrItems
	}
	mature -= 10000
	if mature < time.Now().UnixMilli() {
		mature = time.Now().UnixMilli()
	}
	if _, err = tx.ExecContext(ctx, `UPDATE class_mid_plots SET mature_at_ms=?,fertilized=TRUE WHERE player_id=? AND plot_no=?`, mature, playerID, plotID); err != nil {
		return err
	}
	return addTaskProgress(ctx, tx, playerID, "APPLY_FERTILIZER", 1)
}

func harvest(ctx context.Context, tx *sql.Tx, playerID string, plotID int) error {
	if plotID < 1 || plotID > 16 {
		return ErrInvalid
	}
	var cropID int
	var status string
	var mature int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(crop_id,0),status,mature_at_ms FROM class_mid_plots WHERE player_id=? AND plot_no=? FOR UPDATE`, playerID, plotID).Scan(&cropID, &status, &mature); err != nil {
		return err
	}
	if status != "GROWING" {
		return ErrPlot
	}
	if time.Now().UnixMilli() < mature {
		return ErrNotMature
	}
	var yield int
	if err := tx.QueryRowContext(ctx, `SELECT yield_count FROM class_mid_crops WHERE crop_id=?`, cropID).Scan(&yield); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE class_mid_inventory SET crop_count=crop_count+? WHERE player_id=? AND crop_id=?`, yield, playerID, cropID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE class_mid_plots SET status='NEED_CLEANUP' WHERE player_id=? AND plot_no=?`, playerID, plotID); err != nil {
		return err
	}
	return addTaskProgress(ctx, tx, playerID, "HARVEST", 1)
}

func cleanPlot(ctx context.Context, tx *sql.Tx, playerID string, plotID int) error {
	if plotID < 1 || plotID > 16 {
		return ErrInvalid
	}
	result, err := tx.ExecContext(ctx, `UPDATE class_mid_plots SET crop_id=NULL,status='EMPTY',planted_at_ms=0,mature_at_ms=0,fertilized=FALSE WHERE player_id=? AND plot_no=? AND status='NEED_CLEANUP'`, playerID, plotID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrPlot
	}
	return nil
}

func sellCrop(ctx context.Context, tx *sql.Tx, playerID string, cropID, quantity int) error {
	if cropID < 1 || cropID > 6 || quantity < 1 || quantity > 200 {
		return ErrInvalid
	}
	var price int
	if err := tx.QueryRowContext(ctx, `SELECT sale_price FROM class_mid_crops WHERE crop_id=?`, cropID).Scan(&price); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE class_mid_inventory SET crop_count=crop_count-? WHERE player_id=? AND crop_id=? AND crop_count>=?`, quantity, playerID, cropID, quantity)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrItems
	}
	if _, err = tx.ExecContext(ctx, `UPDATE class_mid_players SET coins=coins+? WHERE player_id=?`, price*quantity, playerID); err != nil {
		return err
	}
	return addTaskProgress(ctx, tx, playerID, "SELL_CROP", quantity)
}

func claimReward(ctx context.Context, tx *sql.Tx, playerID string) error {
	var unfinished int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM class_mid_player_tasks p JOIN class_mid_tasks t ON t.task_id=p.task_id WHERE p.player_id=? AND p.current_count<t.target_count`, playerID).Scan(&unfinished); err != nil {
		return err
	}
	if unfinished > 0 {
		return ErrTask
	}
	if _, err := tx.ExecContext(ctx, `UPDATE class_mid_players SET coins=coins+10,chapter=chapter+1 WHERE player_id=?`, playerID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE class_mid_inventory SET seed_count=seed_count+3 WHERE player_id=? AND crop_id=1`, playerID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE class_mid_player_tasks SET current_count=0,is_claimed=FALSE WHERE player_id=?`, playerID)
	return err
}

func addTaskProgress(ctx context.Context, tx *sql.Tx, playerID, action string, amount int) error {
	_, err := tx.ExecContext(ctx, `UPDATE class_mid_player_tasks p JOIN class_mid_tasks t ON t.task_id=p.task_id SET p.current_count=LEAST(t.target_count,p.current_count+?) WHERE p.player_id=? AND t.action=?`, amount, playerID, action)
	return err
}
