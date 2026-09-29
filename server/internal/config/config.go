package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppEnv               string
	HTTPAddr             string
	MySQLDSN             string
	RedisEnabled         bool
	RedisAddr            string
	RedisPassword        string
	RedisDB              int
	ExchangeLockTTL      time.Duration
	ExchangeLockWait     time.Duration
	OrderRedeemTTL       time.Duration
	OrderExpiryScan      time.Duration
	MaintenanceScan      time.Duration
	IdempotencyRetention time.Duration
	JWTSecret            string
	JWTTTL               time.Duration
	WeChatAppID          string
	WeChatSecret         string
	MockLoginEnabled     bool
	AdminOpenIDs         map[string]struct{}
	StaffOpenIDs         map[string]struct{}
	TeamLeaderOpenIDs    map[string]struct{}
	DefaultAgencyName    string
	loadErr              error
}

func (c Config) Validate() error {
	if c.loadErr != nil {
		return c.loadErr
	}
	if len(c.JWTSecret) < 32 || c.JWTSecret == "dev-secret-change-me" {
		return fmt.Errorf("JWT_SECRET must be a random value of at least 32 characters")
	}
	if c.AppEnv == "local" {
		return nil
	}
	if c.AppEnv != "staging" && c.AppEnv != "production" {
		return fmt.Errorf("APP_ENV must be local, staging, or production")
	}
	if strings.TrimSpace(c.HTTPAddr) == "" {
		return fmt.Errorf("HTTP_ADDR is required outside local mode")
	}
	if strings.TrimSpace(c.MySQLDSN) == "" {
		return fmt.Errorf("MYSQL_DSN is required outside local mode")
	}
	if c.JWTTTL < 5*time.Minute || c.JWTTTL > 30*24*time.Hour {
		return fmt.Errorf("JWT_TTL_SECONDS must be between 300 and 2592000 outside local mode")
	}
	if c.WeChatAppID == "" || c.WeChatSecret == "" {
		return fmt.Errorf("WECHAT_APPID and WECHAT_SECRET are required outside local mode")
	}
	if c.MockLoginEnabled {
		return fmt.Errorf("MOCK_LOGIN_ENABLED must be false outside local mode")
	}
	if strings.HasPrefix(strings.ToLower(c.MySQLDSN), "root:") {
		return fmt.Errorf("MYSQL_DSN must use a non-root database account outside local mode")
	}
	if c.RedisEnabled && (strings.TrimSpace(c.RedisAddr) == "" || strings.TrimSpace(c.RedisPassword) == "") {
		return fmt.Errorf("REDIS_ADDR and REDIS_PASSWORD are required when Redis is enabled outside local mode")
	}
	if c.ExchangeLockTTL <= 0 || c.ExchangeLockTTL > time.Minute || c.ExchangeLockWait < 0 || c.ExchangeLockWait > c.ExchangeLockTTL {
		return fmt.Errorf("exchange lock durations are invalid")
	}
	if c.OrderRedeemTTL < time.Hour || c.OrderRedeemTTL > 30*24*time.Hour {
		return fmt.Errorf("ORDER_REDEEM_TTL_HOURS must be between 1 and 720")
	}
	if c.OrderExpiryScan < 10*time.Second || c.OrderExpiryScan > time.Hour {
		return fmt.Errorf("ORDER_EXPIRY_SCAN_SECONDS must be between 10 and 3600")
	}
	if c.MaintenanceScan < 5*time.Minute || c.MaintenanceScan > 24*time.Hour {
		return fmt.Errorf("MAINTENANCE_SCAN_SECONDS must be between 300 and 86400")
	}
	if c.IdempotencyRetention < 24*time.Hour || c.IdempotencyRetention > 90*24*time.Hour {
		return fmt.Errorf("IDEMPOTENCY_RETENTION_HOURS must be between 24 and 2160")
	}
	if strings.TrimSpace(c.DefaultAgencyName) == "" || len([]rune(c.DefaultAgencyName)) > 128 {
		return fmt.Errorf("DEFAULT_AGENCY_NAME is required and must not exceed 128 characters")
	}
	return nil
}

func Load() Config {
	loadDotEnv(".env")
	var parseErrors []string

	config := Config{
		AppEnv:               env("APP_ENV", "local"),
		HTTPAddr:             env("HTTP_ADDR", ":8888"),
		MySQLDSN:             env("MYSQL_DSN", "root:root@tcp(127.0.0.1:3306)/dio_wapp?charset=utf8mb4&parseTime=True&loc=Local"),
		RedisEnabled:         envBool("REDIS_ENABLED", true, &parseErrors),
		RedisAddr:            env("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword:        env("REDIS_PASSWORD", ""),
		RedisDB:              envInt("REDIS_DB", 0, &parseErrors),
		ExchangeLockTTL:      time.Duration(envInt("EXCHANGE_LOCK_TTL_MS", 5000, &parseErrors)) * time.Millisecond,
		ExchangeLockWait:     time.Duration(envInt("EXCHANGE_LOCK_WAIT_MS", 800, &parseErrors)) * time.Millisecond,
		OrderRedeemTTL:       time.Duration(envInt("ORDER_REDEEM_TTL_HOURS", 24, &parseErrors)) * time.Hour,
		OrderExpiryScan:      time.Duration(envInt("ORDER_EXPIRY_SCAN_SECONDS", 60, &parseErrors)) * time.Second,
		MaintenanceScan:      time.Duration(envInt("MAINTENANCE_SCAN_SECONDS", 3600, &parseErrors)) * time.Second,
		IdempotencyRetention: time.Duration(envInt("IDEMPOTENCY_RETENTION_HOURS", 168, &parseErrors)) * time.Hour,
		JWTSecret:            env("JWT_SECRET", "dev-secret-change-me"),
		JWTTTL:               time.Duration(envInt("JWT_TTL_SECONDS", 604800, &parseErrors)) * time.Second,
		WeChatAppID:          env("WECHAT_APPID", ""),
		WeChatSecret:         env("WECHAT_SECRET", ""),
		MockLoginEnabled:     envBool("MOCK_LOGIN_ENABLED", false, &parseErrors),
		AdminOpenIDs:         parseSet(env("ADMIN_OPENIDS", "")),
		StaffOpenIDs:         parseSet(env("STAFF_OPENIDS", "")),
		TeamLeaderOpenIDs:    parseSet(env("TEAM_LEADER_OPENIDS", "")),
		DefaultAgencyName:    env("DEFAULT_AGENCY_NAME", "默认事务所"),
	}
	if len(parseErrors) > 0 {
		config.loadErr = fmt.Errorf("invalid environment configuration: %s", strings.Join(parseErrors, ", "))
	}
	return config
}

func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

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
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		_ = os.Setenv(key, value)
	}
}

func env(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int, parseErrors *[]string) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < -1_000_000_000 || parsed > 1_000_000_000 {
		*parseErrors = append(*parseErrors, key+" must be an integer between -1000000000 and 1000000000")
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool, parseErrors *[]string) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		*parseErrors = append(*parseErrors, key+" must be true or false")
		return fallback
	}
	return parsed
}

func parseSet(raw string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result[item] = struct{}{}
		}
	}
	return result
}
