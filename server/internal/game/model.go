// Package game 实现单服务器农场后端。
package game

import "errors"

var (
	ErrInvalid         = errors.New("INVALID_ARGUMENT")
	ErrDuplicate       = errors.New("ACCOUNT_EXISTS")
	ErrUnauthenticated = errors.New("UNAUTHENTICATED")
	ErrCredentials     = errors.New("INVALID_CREDENTIALS")
	ErrCoins           = errors.New("INSUFFICIENT_COINS")
	ErrItems           = errors.New("INSUFFICIENT_ITEMS")
	ErrPlot            = errors.New("PLOT_STATE_CONFLICT")
	ErrNotMature       = errors.New("CROP_NOT_MATURE")
	ErrTask            = errors.New("CHAPTER_NOT_CLAIMABLE")
	ErrMailNotFound    = errors.New("MAIL_NOT_FOUND")
	ErrFriendNotFound  = errors.New("FRIEND_NOT_FOUND")
	ErrAlreadyFriends  = errors.New("ALREADY_FRIENDS")
)

const WelcomeMailTitle = "欢迎来到经典农场"
const WelcomeMailContent = "欢迎来到经典农场！快去种下你的第一颗胡萝卜吧。"

type Account struct{ ID, Username, Password string }

type Crop struct {
	ID            int    `json:"crop_id"`
	Name          string `json:"crop_name"`
	SeedPrice     int    `json:"seed_price"`
	SalePrice     int    `json:"sale_price"`
	MatureSeconds int    `json:"mature_seconds"`
	Yield         int    `json:"yield"`
}

type InventoryItem struct {
	CropID    int    `json:"crop_id"`
	CropName  string `json:"crop_name"`
	SeedCount int    `json:"seed_count"`
	CropCount int    `json:"crop_count"`
}

type Plot struct {
	ID          int    `json:"plot_id"`
	CropID      int    `json:"crop_id,omitempty"`
	CropName    string `json:"crop_name,omitempty"`
	Status      string `json:"status"`
	PlantedAtMS int64  `json:"planted_at_ms"`
	MatureAtMS  int64  `json:"mature_at_ms"`
	Fertilized  bool   `json:"fertilized"`
}

type Task struct {
	Action  string `json:"action"`
	Label   string `json:"label"`
	Current int    `json:"current"`
	Target  int    `json:"target"`
}

type Mail struct {
	ID             string `json:"mail_id"`
	SenderID       string `json:"sender_id,omitempty"`
	SenderUsername string `json:"sender_username,omitempty"`
	Title          string `json:"title"`
	Content        string `json:"content"`
	IsRead         bool   `json:"is_read"`
	CreatedAtMS    int64  `json:"created_at_ms"`
}

type Friend struct {
	PlayerID string `json:"player_id"`
	Username string `json:"username"`
}

// State 保留旧 Qt 字段，并增加六种作物的仓库和商店数据。
type State struct {
	PlayerID   string          `json:"player_id"`
	Version    uint64          `json:"state_version,string"`
	Coins      int             `json:"coins"`
	Seeds      int             `json:"seeds"`
	Fertilizer int             `json:"fertilizer"`
	Crops      int             `json:"crops"`
	Plots      []Plot          `json:"plots"`
	Chapter    int             `json:"chapter"`
	Tasks      []Task          `json:"tasks"`
	Inventory  []InventoryItem `json:"inventory,omitempty"`
	Shop       []Crop          `json:"shop,omitempty"`
}

type Args struct {
	PlotID   int    `json:"plot_id,omitempty"`
	CropID   int    `json:"crop_id,omitempty"`
	Quantity int    `json:"quantity,omitempty"`
	Token    string `json:"token,omitempty"`
	MailID   string `json:"mail_id,omitempty"`
}

type Command struct {
	RequestID string `json:"request_id"`
	Action    string `json:"action"`
	Data      Args   `json:"data"`
}

type Config struct {
	SeedPrice         int `json:"seed_price"`
	FertilizerPrice   int `json:"fertilizer_price"`
	CropPrice         int `json:"crop_price"`
	GrowthSeconds     int `json:"growth_seconds"`
	FertilizerSeconds int `json:"fertilizer_seconds"`
	Yield             int `json:"yield"`
	Capacity          int `json:"capacity"`
}

type Response struct {
	Type         string   `json:"type"`
	RequestID    string   `json:"request_id,omitempty"`
	Action       string   `json:"action,omitempty"`
	Code         string   `json:"code"`
	Message      string   `json:"message,omitempty"`
	ServerTimeMS int64    `json:"server_time_ms"`
	Snapshot     *State   `json:"snapshot,omitempty"`
	Token        string   `json:"token,omitempty"`
	PlayerID     string   `json:"player_id,omitempty"`
	FriendID     string   `json:"friend_id,omitempty"`
	Config       *Config  `json:"config,omitempty"`
	Mails        []Mail   `json:"mails,omitempty"`
	Friends      []Friend `json:"friends,omitempty"`
	Plots        []Plot   `json:"plots,omitempty"`
}

// GameConfig 返回旧 Qt 需要的胡萝卜配置。
func GameConfig() Config {
	return Config{SeedPrice: 2, FertilizerPrice: 2, CropPrice: 5, GrowthSeconds: 30, FertilizerSeconds: 10, Yield: 3, Capacity: 200}
}
