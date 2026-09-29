package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/service"

	"gorm.io/gorm"
)

const migrationLockName = "dio_wapp_schema_migrations"

type SchemaMigration struct {
	Version   uint64    `gorm:"primaryKey"`
	Name      string    `gorm:"size:128;not null"`
	AppliedAt time.Time `gorm:"not null"`
}

type migration struct {
	version uint64
	name    string
	apply   func(*gorm.DB) error
}

var migrations = []migration{
	{version: 1, name: "initial_schema", apply: migrateInitialSchema},
	{version: 2, name: "query_indexes", apply: migrateQueryIndexes},
	{version: 3, name: "backfill_agency_point_accounts", apply: migrateAgencyPointAccounts},
	{version: 4, name: "maintenance_indexes", apply: migrateMaintenanceIndexes},
	{version: 5, name: "idempotency_compaction", apply: func(db *gorm.DB) error {
		return db.AutoMigrate(&model.IdempotencyRecord{})
	}},
	{version: 6, name: "exchange_recovery_reference", apply: func(db *gorm.DB) error {
		return db.AutoMigrate(&model.IdempotencyRecord{})
	}},
}

func Run(ctx context.Context, database *gorm.DB) (err error) {
	if database == nil {
		return errors.New("migration database is nil")
	}
	if err := validatePlan(migrations); err != nil {
		return err
	}
	release, err := acquireLock(ctx, database)
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := release(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()

	db := database.WithContext(ctx)
	if err := db.AutoMigrate(&SchemaMigration{}); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}
	for _, item := range migrations {
		var applied SchemaMigration
		err := db.First(&applied, "version = ?", item.version).Error
		if err == nil {
			if applied.Name != item.name {
				return fmt.Errorf("migration %d name changed from %q to %q", item.version, applied.Name, item.name)
			}
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("read migration %d: %w", item.version, err)
		}
		if err := item.apply(db); err != nil {
			return fmt.Errorf("apply migration %d %s: %w", item.version, item.name, err)
		}
		if err := db.Create(&SchemaMigration{
			Version:   item.version,
			Name:      item.name,
			AppliedAt: time.Now(),
		}).Error; err != nil {
			return fmt.Errorf("record migration %d: %w", item.version, err)
		}
	}
	return nil
}

func validatePlan(items []migration) error {
	var previous uint64
	for _, item := range items {
		if item.version == 0 || item.version <= previous {
			return fmt.Errorf("migration versions must be strictly increasing: %d after %d", item.version, previous)
		}
		if item.name == "" || item.apply == nil {
			return fmt.Errorf("migration %d must have a name and apply function", item.version)
		}
		previous = item.version
	}
	return nil
}

func migrateInitialSchema(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.User{},
		&model.AgencyPointAccount{},
		&model.Agency{},
		&model.IdolGroup{},
		&model.StaffMember{},
		&model.StaffMemberAuditLog{},
		&model.AdminMember{},
		&model.IdentityQRToken{},
		&model.PointLedger{},
		&model.PointGrantRequest{},
		&model.StaffPointDailyUsage{},
		&model.PointCorrectionRequest{},
		&model.Goods{},
		&model.StockMovement{},
		&model.GoodsChangeRequest{},
		&model.ExchangeOrder{},
		&model.IdempotencyRecord{},
	)
}

func migrateQueryIndexes(db *gorm.DB) error {
	if db.Migrator().HasIndex(&model.PointGrantRequest{}, "idx_point_grant_review") {
		if err := db.Migrator().DropIndex(&model.PointGrantRequest{}, "idx_point_grant_review"); err != nil {
			return fmt.Errorf("replace index idx_point_grant_review: %w", err)
		}
	}
	if err := db.Migrator().CreateIndex(&model.PointGrantRequest{}, "idx_point_grant_review"); err != nil {
		return fmt.Errorf("create index idx_point_grant_review: %w", err)
	}

	indexSets := []struct {
		model   any
		indexes []string
	}{
		{&model.PointCorrectionRequest{}, []string{"idx_point_correction_group_review", "idx_point_correction_submitter_list"}},
		{&model.GoodsChangeRequest{}, []string{"idx_goods_request_staff_list", "idx_goods_request_group_review", "idx_goods_request_admin_review"}},
		{&model.ExchangeOrder{}, []string{"idx_exchange_expiry_scan", "idx_exchange_user_expiry"}},
	}
	for _, set := range indexSets {
		for _, index := range set.indexes {
			if db.Migrator().HasIndex(set.model, index) {
				continue
			}
			if err := db.Migrator().CreateIndex(set.model, index); err != nil {
				return fmt.Errorf("create index %s: %w", index, err)
			}
		}
	}
	return nil
}

func migrateAgencyPointAccounts(db *gorm.DB) error {
	return service.BackfillAgencyPointAccounts(db.Statement.Context, db)
}

func migrateMaintenanceIndexes(db *gorm.DB) error {
	indexSets := []struct {
		model any
		index string
	}{
		{&model.IdentityQRToken{}, "idx_identity_qr_expiry"},
		{&model.IdempotencyRecord{}, "idx_idempotency_cleanup"},
	}
	for _, item := range indexSets {
		if db.Migrator().HasIndex(item.model, item.index) {
			continue
		}
		if err := db.Migrator().CreateIndex(item.model, item.index); err != nil {
			return fmt.Errorf("create index %s: %w", item.index, err)
		}
	}
	return nil
}

func acquireLock(ctx context.Context, database *gorm.DB) (func() error, error) {
	if database.Dialector.Name() != "mysql" {
		return func() error { return nil }, nil
	}
	sqlDB, err := database.DB()
	if err != nil {
		return nil, fmt.Errorf("get migration database connection: %w", err)
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("reserve migration connection: %w", err)
	}
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", migrationLockName, 60).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("acquire migration lock: %w", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		_ = conn.Close()
		return nil, errors.New("timed out waiting for migration lock")
	}
	return func() error {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released sql.NullInt64
		releaseErr := conn.QueryRowContext(releaseCtx, "SELECT RELEASE_LOCK(?)", migrationLockName).Scan(&released)
		closeErr := conn.Close()
		if releaseErr != nil {
			return fmt.Errorf("release migration lock: %w", releaseErr)
		}
		if !released.Valid || released.Int64 != 1 {
			return errors.New("migration lock was not owned when released")
		}
		if closeErr != nil {
			return fmt.Errorf("close migration connection: %w", closeErr)
		}
		return nil
	}, nil
}
