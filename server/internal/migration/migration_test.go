package migration

import (
	"context"
	"strings"
	"testing"
	"time"

	"dio_wapp/server/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCompactionMigrationPreservesExistingResponses(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`CREATE TABLE idempotency_records (
		id integer PRIMARY KEY, actor_id integer NOT NULL, operation varchar(32) NOT NULL,
		request_id varchar(64) NOT NULL, request_hash varchar(64) NOT NULL,
		response_json text NOT NULL, created_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("INSERT INTO idempotency_records (id, actor_id, operation, request_id, request_hash, response_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		1, 1, "exchange", "existing_request", "hash", `{"order_id":42}`, time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&SchemaMigration{}); err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations[:4] {
		if err := database.Create(&SchemaMigration{Version: item.version, Name: item.name, AppliedAt: time.Now()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := Run(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var record model.IdempotencyRecord
	if err := database.First(&record, 1).Error; err != nil {
		t.Fatal(err)
	}
	if record.Compacted || record.ResponseJSON != `{"order_id":42}` || record.RequestID != "existing_request" {
		t.Fatalf("migration changed existing response: %+v", record)
	}
	if !database.Migrator().HasIndex(&model.IdempotencyRecord{}, "idx_idempotency_compaction") {
		t.Fatal("missing compaction index")
	}
}

func TestRunAppliesInitialSchemaOnce(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := Run(context.Background(), database); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if err := Run(context.Background(), database); err != nil {
		t.Fatalf("rerun migrations: %v", err)
	}

	for _, table := range []string{"schema_migrations", "users", "goods", "exchange_orders", "point_ledgers"} {
		if !database.Migrator().HasTable(table) {
			t.Fatalf("expected table %s", table)
		}
	}
	var records []SchemaMigration
	if err := database.Find(&records).Error; err != nil {
		t.Fatalf("list migration records: %v", err)
	}
	if len(records) != 6 || records[0].Version != 1 || records[0].Name != "initial_schema" || records[1].Version != 2 || records[1].Name != "query_indexes" || records[2].Version != 3 || records[2].Name != "backfill_agency_point_accounts" || records[3].Version != 4 || records[3].Name != "maintenance_indexes" || records[4].Version != 5 || records[4].Name != "idempotency_compaction" || records[5].Name != "exchange_recovery_reference" {
		t.Fatalf("unexpected migration records: %+v", records)
	}

	for _, check := range []struct {
		model any
		name  string
	}{
		{&model.PointGrantRequest{}, "idx_point_grant_review"},
		{&model.PointCorrectionRequest{}, "idx_point_correction_group_review"},
		{&model.PointCorrectionRequest{}, "idx_point_correction_submitter_list"},
		{&model.GoodsChangeRequest{}, "idx_goods_request_staff_list"},
		{&model.GoodsChangeRequest{}, "idx_goods_request_group_review"},
		{&model.GoodsChangeRequest{}, "idx_goods_request_admin_review"},
		{&model.ExchangeOrder{}, "idx_exchange_expiry_scan"},
		{&model.ExchangeOrder{}, "idx_exchange_user_expiry"},
		{&model.IdentityQRToken{}, "idx_identity_qr_expiry"},
		{&model.IdempotencyRecord{}, "idx_idempotency_cleanup"},
		{&model.IdempotencyRecord{}, "idx_idempotency_compaction"},
	} {
		if !database.Migrator().HasIndex(check.model, check.name) {
			t.Errorf("expected index %s", check.name)
		}
	}
}

func TestExchangeRecoveryMigrationFromVersionFive(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`CREATE TABLE idempotency_records (
		id integer PRIMARY KEY, actor_id integer NOT NULL, operation varchar(32) NOT NULL,
		request_id varchar(64) NOT NULL, request_hash varchar(64) NOT NULL,
		response_json text NOT NULL, compacted numeric NOT NULL DEFAULT 0, created_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("INSERT INTO idempotency_records (id, actor_id, operation, request_id, request_hash, response_json, compacted, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)", 1, 1, "exchange", "old-request", "hash", "", true, time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&SchemaMigration{}); err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations[:5] {
		if err := database.Create(&SchemaMigration{Version: item.version, Name: item.name, AppliedAt: time.Now()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := Run(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var record model.IdempotencyRecord
	if err := database.First(&record, 1).Error; err != nil {
		t.Fatal(err)
	}
	if record.ResourceID != 0 || !record.Compacted || record.RequestID != "old-request" {
		t.Fatalf("migration rewrote archival evidence: %+v", record)
	}
}

func TestRunBackfillsAgencyPointAccountsOnlyOnce(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := Run(context.Background(), database); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if err := database.Delete(&SchemaMigration{}, 3).Error; err != nil {
		t.Fatalf("remove data migration record: %v", err)
	}
	agency := model.Agency{Name: "迁移事务所", Status: model.MemberStatusNormal}
	user := model.User{OpenID: "migration-user", PointsBalance: 60, Status: model.UserStatusNormal}
	if err := database.Create(&agency).Error; err != nil {
		t.Fatalf("create agency: %v", err)
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := database.Create(&model.PointLedger{
		AgencyID: agency.ID, UserID: user.ID, BeforePoints: 0, DeltaPoints: 60, AfterPoints: 60,
		Type: "migration_test", OperatorID: user.ID,
	}).Error; err != nil {
		t.Fatalf("create ledger: %v", err)
	}

	if err := Run(context.Background(), database); err != nil {
		t.Fatalf("apply data migration: %v", err)
	}
	if err := Run(context.Background(), database); err != nil {
		t.Fatalf("rerun data migration: %v", err)
	}
	var accounts []model.AgencyPointAccount
	if err := database.Find(&accounts).Error; err != nil {
		t.Fatalf("list agency accounts: %v", err)
	}
	if len(accounts) != 1 || accounts[0].AgencyID != agency.ID || accounts[0].UserID != user.ID || accounts[0].Balance != 60 {
		t.Fatalf("unexpected agency accounts: %+v", accounts)
	}
}

func TestRunRejectsChangedMigrationIdentity(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := Run(context.Background(), database); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if err := database.Model(&SchemaMigration{}).Where("version = ?", 1).Update("name", "changed").Error; err != nil {
		t.Fatalf("change migration identity: %v", err)
	}
	err = Run(context.Background(), database)
	if err == nil || !strings.Contains(err.Error(), "name changed") {
		t.Fatalf("expected migration identity error, got %v", err)
	}
}

func TestRunUpgradesLegacyQueryIndexes(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := Run(context.Background(), database); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if err := database.Migrator().DropIndex(&model.PointGrantRequest{}, "idx_point_grant_review"); err != nil {
		t.Fatalf("drop current review index: %v", err)
	}
	if err := database.Exec("CREATE INDEX idx_point_grant_review ON point_grant_requests (agency_id, group_id, request_status)").Error; err != nil {
		t.Fatalf("create legacy review index: %v", err)
	}
	if err := database.Migrator().DropIndex(&model.ExchangeOrder{}, "idx_exchange_expiry_scan"); err != nil {
		t.Fatalf("drop expiry index: %v", err)
	}
	if err := database.Delete(&SchemaMigration{}, 2).Error; err != nil {
		t.Fatalf("remove second migration record: %v", err)
	}

	if err := Run(context.Background(), database); err != nil {
		t.Fatalf("upgrade legacy indexes: %v", err)
	}
	if !database.Migrator().HasIndex(&model.ExchangeOrder{}, "idx_exchange_expiry_scan") {
		t.Fatal("expected missing expiry index to be recreated")
	}
	var columns []struct {
		Name string
	}
	if err := database.Raw("PRAGMA index_info('idx_point_grant_review')").Scan(&columns).Error; err != nil {
		t.Fatalf("inspect upgraded review index: %v", err)
	}
	want := []string{"agency_id", "group_id", "request_status", "id"}
	if len(columns) != len(want) {
		t.Fatalf("unexpected upgraded review index columns: %+v", columns)
	}
	for index := range want {
		if columns[index].Name != want[index] {
			t.Fatalf("unexpected upgraded review index columns: %+v", columns)
		}
	}
}

func TestValidatePlanRejectsDuplicateOrIncompleteMigrations(t *testing.T) {
	apply := func(*gorm.DB) error { return nil }
	if err := validatePlan([]migration{{version: 1, name: "one", apply: apply}, {version: 1, name: "duplicate", apply: apply}}); err == nil {
		t.Fatal("expected duplicate migration version to fail")
	}
	if err := validatePlan([]migration{{version: 1, name: "", apply: apply}}); err == nil {
		t.Fatal("expected unnamed migration to fail")
	}
}
