package config

import (
	"strings"
	"testing"
	"time"
)

func TestValidateRejectsUnsafeProductionConfiguration(t *testing.T) {
	cfg := Config{
		AppEnv:       "production",
		JWTSecret:    "dev-secret-change-me",
		MySQLDSN:     "root:root@tcp(localhost:3306)/dio_wapp",
		WeChatAppID:  "appid",
		WeChatSecret: "secret",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsafe production configuration to be rejected")
	}
}

func TestValidateRejectsProductionMockLogin(t *testing.T) {
	cfg := Config{
		AppEnv:               "production",
		HTTPAddr:             ":8888",
		JWTSecret:            "01234567890123456789012345678901",
		JWTTTL:               7 * 24 * time.Hour,
		MySQLDSN:             "dio_app:secret@tcp(mysql:3306)/dio_wapp",
		WeChatAppID:          "appid",
		WeChatSecret:         "secret",
		MockLoginEnabled:     true,
		ExchangeLockTTL:      5 * time.Second,
		ExchangeLockWait:     time.Second,
		OrderRedeemTTL:       24 * time.Hour,
		OrderExpiryScan:      time.Minute,
		MaintenanceScan:      time.Hour,
		IdempotencyRetention: 7 * 24 * time.Hour,
		DefaultAgencyName:    "事务所",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected production mock login to be rejected")
	}
}

func TestValidateAllowsHardenedProductionConfiguration(t *testing.T) {
	cfg := Config{
		AppEnv:               "production",
		HTTPAddr:             ":8888",
		MySQLDSN:             "dio_app:secret@tcp(mysql:3306)/dio_wapp",
		RedisEnabled:         true,
		RedisAddr:            "redis:6379",
		RedisPassword:        "redis-secret",
		ExchangeLockTTL:      5 * time.Second,
		ExchangeLockWait:     time.Second,
		OrderRedeemTTL:       24 * time.Hour,
		OrderExpiryScan:      time.Minute,
		MaintenanceScan:      time.Hour,
		IdempotencyRetention: 7 * 24 * time.Hour,
		JWTSecret:            "01234567890123456789012345678901",
		JWTTTL:               7 * 24 * time.Hour,
		WeChatAppID:          "appid",
		WeChatSecret:         "secret",
		DefaultAgencyName:    "事务所",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected hardened production config to pass: %v", err)
	}
}

func TestValidateAllowsExplicitLocalConfiguration(t *testing.T) {
	if err := (Config{AppEnv: "local", JWTSecret: "01234567890123456789012345678901"}).Validate(); err != nil {
		t.Fatalf("local configuration should remain available: %v", err)
	}
}

func TestValidateRejectsDefaultJWTSecretInLocalMode(t *testing.T) {
	if err := (Config{AppEnv: "local", JWTSecret: "dev-secret-change-me"}).Validate(); err == nil {
		t.Fatal("expected default local JWT secret to be rejected")
	}
}

func TestLoadRejectsMalformedEnvironmentValues(t *testing.T) {
	t.Setenv("JWT_TTL_SECONDS", "seven-days")
	t.Setenv("REDIS_ENABLED", "sometimes")
	t.Setenv("JWT_SECRET", "01234567890123456789012345678901")

	err := Load().Validate()
	if err == nil {
		t.Fatal("expected malformed environment values to fail validation")
	}
	if !strings.Contains(err.Error(), "JWT_TTL_SECONDS") || !strings.Contains(err.Error(), "REDIS_ENABLED") {
		t.Fatalf("validation error did not identify malformed variables: %v", err)
	}
}

func TestLoadRejectsIntegerBeforeDurationOverflow(t *testing.T) {
	t.Setenv("ORDER_REDEEM_TTL_HOURS", "9223372036854775807")
	t.Setenv("JWT_SECRET", "01234567890123456789012345678901")

	err := Load().Validate()
	if err == nil || !strings.Contains(err.Error(), "ORDER_REDEEM_TTL_HOURS") {
		t.Fatalf("expected oversized duration source to fail validation, got %v", err)
	}
}
