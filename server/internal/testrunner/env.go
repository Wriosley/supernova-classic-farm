package testrunner

import (
	"bufio"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"database/sql"

	_ "github.com/go-sql-driver/mysql"
)

type MySQLConfig struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string
	DSN      string
}

func (c MySQLConfig) Configured() bool {
	return strings.TrimSpace(c.DSN) != "" || (strings.TrimSpace(c.User) != "" && strings.TrimSpace(c.Password) != "" && strings.TrimSpace(c.Database) != "")
}

func (c MySQLConfig) BuildDSN() (string, error) {
	if strings.TrimSpace(c.DSN) != "" {
		return c.DSN, nil
	}
	if !c.Configured() {
		return "", fmt.Errorf("mysql is not configured")
	}
	host := c.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := c.Port
	if port == 0 {
		port = 3306
	}
	escaped := url.QueryEscape(c.Password)
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=true&loc=Local",
		c.User, escaped, host, port, c.Database), nil
}

func LoadDotEnv(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer file.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		values[key] = value
	}
	return values, scanner.Err()
}

func LoadMySQLConfig(repoRoot string) (MySQLConfig, error) {
	dotenv, err := LoadDotEnv(filepath.Join(repoRoot, ".env"))
	if err != nil {
		return MySQLConfig{}, err
	}
	cfg := MySQLConfig{
		Host:     firstNonEmpty(os.Getenv("MYSQL_HOST"), dotenv["MYSQL_HOST"], "127.0.0.1"),
		Database: firstNonEmpty(os.Getenv("MYSQL_DATABASE"), dotenv["MYSQL_DATABASE"], "classicfarm"),
		User:     firstNonEmpty(os.Getenv("MYSQL_USER"), dotenv["MYSQL_USER"], "classicfarm"),
		Password: firstNonEmpty(os.Getenv("MYSQL_PASSWORD"), dotenv["MYSQL_PASSWORD"]),
		DSN:      firstNonEmpty(os.Getenv("MYSQL_DSN"), dotenv["MYSQL_DSN"]),
	}
	if cfg.Password == "请在本地填写" {
		cfg.Password = ""
	}
	portRaw := firstNonEmpty(os.Getenv("MYSQL_PORT"), dotenv["MYSQL_PORT"], "3306")
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		return MySQLConfig{}, fmt.Errorf("invalid MYSQL_PORT: %w", err)
	}
	cfg.Port = port
	return cfg, nil
}

func PingMySQL(cfg MySQLConfig) error {
	dsn, err := cfg.BuildDSN()
	if err != nil {
		return err
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return sanitizeError(err, cfg)
	}
	defer db.Close()
	db.SetConnMaxLifetime(5 * time.Second)
	if err := db.Ping(); err != nil {
		return sanitizeError(err, cfg)
	}
	return nil
}

func PortFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sanitizeError(err error, cfg MySQLConfig) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", RedactSecrets(err.Error(), cfg))
}
