package service

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"
	driver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func mysqlTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_MYSQL_DSN")
	local := dsn == "" && os.Getenv("DIO_TEST_MYSQL_LOCAL") == "1"
	if local {
		file, err := os.Open("../../.env")
		if err != nil {
			t.Fatal("cannot read local test connection configuration")
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			key, value, ok := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
			if ok && strings.TrimSpace(key) == "MYSQL_DSN" {
				dsn = strings.Trim(strings.TrimSpace(value), "\"'")
			}
		}
		if scanner.Err() != nil || dsn == "" {
			t.Fatal("local MYSQL_DSN unavailable")
		}
	}
	if dsn == "" {
		t.Skip("set TEST_MYSQL_DSN to run isolated MySQL concurrency tests")
	}
	config, err := driver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test MySQL connection configuration")
	}
	if local && config.Addr != "127.0.0.1:3306" && config.Addr != "localhost:3306" {
		t.Fatal("local tests require a loopback MySQL connection")
	}
	config.DBName = ""
	config.ParseTime = true
	config.Timeout = 5 * time.Second
	config.ReadTimeout = 15 * time.Second
	config.WriteTimeout = 15 * time.Second
	admin, err := gorm.Open(mysql.Open(config.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot connect to test MySQL; check local service and test credentials")
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminSQL.Close() })
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	name := "dio_it_" + hex.EncodeToString(suffix[:])
	if err := admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4").Error; err != nil {
		t.Fatal("test account must have permission to create isolated test schemas")
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP DATABASE `" + name + "`").Error; err != nil {
			t.Errorf("failed to remove temporary schema %s: %v", name, err)
		}
	})
	config.DBName = name
	db, err := gorm.Open(mysql.Open(config.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot connect to isolated test schema")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(16)
	t.Cleanup(func() { _ = sqlDB.Close() })
	migrateCatalogForTest(t, db)
	return db
}

func concurrently(count int, work func(int) error) []error {
	start := make(chan struct{})
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; errs[i] = work(i) }(i)
	}
	close(start)
	wg.Wait()
	return errs
}

func assertRows(t *testing.T, db *gorm.DB, value any, want int64) {
	t.Helper()
	var count int64
	if err := db.Model(value).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%T count = %d, want %d", value, count, want)
	}
}

func TestMySQLLastStockConcurrency(t *testing.T) {
	db := mysqlTestDB(t)
	_, goods := auditFixture(t, db)
	if err := db.Model(&goods).Update("stock", 1).Error; err != nil {
		t.Fatal(err)
	}
	users := make([]model.User, 8)
	for i := range users {
		users[i] = model.User{OpenID: fmt.Sprintf("buyer-%d", i), PointsBalance: 100}
		mustCreate(t, db, &users[i])
		mustCreate(t, db, &model.AgencyPointAccount{AgencyID: goods.AgencyID, UserID: users[i].ID, Balance: 100})
	}
	shop := NewShopService(db)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	errs := concurrently(len(users), func(i int) error {
		_, err := shop.ExchangeGoods(ctx, users[i].ID, goods.ID, fmt.Sprintf("last-stock-%d", i), 10)
		return err
	})
	success := 0
	for _, err := range errs {
		if err == nil {
			success++
			continue
		}
		var appErr *xerr.Error
		if !errors.As(err, &appErr) || appErr.Code != "goods_sold_out" {
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("successful purchases = %d", success)
	}
	assertRows(t, db, &model.ExchangeOrder{}, 1)
	assertRows(t, db.Where("type = ?", PointLedgerExchangeCost), &model.PointLedger{}, 1)
	assertRows(t, db.Where("type = ?", model.StockMovementExchange), &model.StockMovement{}, 1)
	if err := db.First(&goods, goods.ID).Error; err != nil {
		t.Fatal(err)
	}
	var total int64
	if err := db.Model(&model.User{}).Where("open_id LIKE ?", "buyer-%").Select("SUM(points_balance)").Scan(&total).Error; err != nil {
		t.Fatal(err)
	}
	if goods.Stock != 0 || total != 790 {
		t.Fatalf("stock=%d balance sum=%d", goods.Stock, total)
	}
}

func TestMySQLDuplicateExchangeConcurrency(t *testing.T) {
	db := mysqlTestDB(t)
	user, goods := auditFixture(t, db)
	shop := NewShopService(db)
	results := make([]ExchangeResult, 8)
	errs := concurrently(8, func(i int) error {
		var err error
		results[i], err = shop.ExchangeGoods(context.Background(), user.ID, goods.ID, "same-exchange-request", 10)
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
		if results[i].Order.ID != results[0].Order.ID {
			t.Fatal("duplicate order")
		}
	}
	assertRows(t, db, &model.ExchangeOrder{}, 1)
	assertRows(t, db.Where("type = ?", PointLedgerExchangeCost), &model.PointLedger{}, 1)
	report := NewAdminReportService(db, &AuthService{db: db})
	for _, kind := range []string{"accounts", "orders", "stock"} {
		page, err := report.Reconcile(context.Background(), user.ID, goods.AgencyID, kind, 0, 20)
		if err != nil || len(page.Issues) != 0 {
			t.Fatalf("MySQL snapshot reconciliation %s: %+v %v", kind, page, err)
		}
	}
}

func TestMySQLCancelRedeemConcurrency(t *testing.T) {
	db := mysqlTestDB(t)
	user, goods := auditFixture(t, db)
	staff := model.User{OpenID: "redeem-staff"}
	mustCreate(t, db, &staff)
	mustCreate(t, db, &model.StaffMember{AgencyID: goods.AgencyID, GroupID: goods.GroupID, UserID: staff.ID, Role: model.StaffRoleStaff})
	shop := NewShopService(db)
	ctx := context.Background()
	for attempt := 0; attempt < 6; attempt++ {
		// Reset fixture balances and stock so every race starts from the same state.
		if err := db.Model(&goods).Update("stock", 2).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.AgencyPointAccount{}).Where("user_id = ?", user.ID).Update("balance", 100).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&user).Update("points_balance", 100).Error; err != nil {
			t.Fatal(err)
		}
		order, err := shop.ExchangeGoods(ctx, user.ID, goods.ID, fmt.Sprintf("race-order-%d", attempt), 10)
		if err != nil {
			t.Fatal(err)
		}
		qr, err := shop.CreateRedeemQRCode(ctx, user.ID, order.Order.ID)
		if err != nil {
			t.Fatal(err)
		}
		errs := concurrently(2, func(i int) error {
			if i == 0 {
				_, err := shop.CancelOrder(ctx, user.ID, order.Order.ID, fmt.Sprintf("cancel-race-%d", attempt))
				return err
			}
			_, err := shop.RedeemOrderByStaff(ctx, staff.ID, qr.Token, fmt.Sprintf("redeem-race-%d", attempt))
			return err
		})
		for _, err := range errs {
			if err == nil {
				continue
			}
			var appErr *xerr.Error
			if !errors.As(err, &appErr) || (appErr.Code != "order_already_redeemed" && appErr.Code != "order_not_pending") {
				t.Fatalf("database concurrency error: %v", err)
			}
		}
		var stored model.ExchangeOrder
		if err := db.First(&stored, order.Order.ID).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.First(&goods, goods.ID).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.First(&user, user.ID).Error; err != nil {
			t.Fatal(err)
		}
		refunds := int64(0)
		if stored.Status == model.ExchangeOrderStatusCanceled {
			refunds = 1
			if goods.Stock != 2 || user.PointsBalance != 100 {
				t.Fatal("cancel did not restore exactly once")
			}
		} else if stored.Status == model.ExchangeOrderStatusRedeemed {
			if goods.Stock != 1 || user.PointsBalance != 90 {
				t.Fatal("redeemed order was refunded")
			}
		} else {
			t.Fatalf("invalid final status: %s", stored.Status)
		}
		assertRows(t, db.Where("reference_id = ? AND type = ?", stored.ID, PointLedgerOrderRefund), &model.PointLedger{}, refunds)
		assertRows(t, db.Where("reference_id = ? AND type = ?", stored.ID, model.StockMovementOrderCancel), &model.StockMovement{}, refunds)
	}
}

func TestMySQLConcurrentGrantApproval(t *testing.T) {
	db := mysqlTestDB(t)
	user, goods := auditFixture(t, db)
	staff := model.User{OpenID: "grant-staff"}
	mustCreate(t, db, &staff)
	mustCreate(t, db, &model.StaffMember{AgencyID: goods.AgencyID, GroupID: goods.GroupID, UserID: staff.ID})
	leaders := make([]model.User, 2)
	for i := range leaders {
		leaders[i] = model.User{OpenID: fmt.Sprintf("leader-%d", i)}
		mustCreate(t, db, &leaders[i])
		mustCreate(t, db, &model.StaffMember{AgencyID: goods.AgencyID, GroupID: goods.GroupID, UserID: leaders[i].ID, Role: model.StaffRoleTeamLeader})
	}
	grant := model.PointGrantRequest{AgencyID: goods.AgencyID, GroupID: goods.GroupID, UserID: user.ID, SubmittedBy: staff.ID, GrantMode: model.PointGrantModeManual, Points: 10000, RequestStatus: model.ApprovalStatusPending, ClientRequestID: "concurrent-grant"}
	mustCreate(t, db, &grant)
	points := &PointsService{db: db, authService: &AuthService{db: db}}
	errs := concurrently(2, func(i int) error {
		_, err := points.ReviewPointGrant(context.Background(), leaders[i].ID, grant.ID, true, "checked")
		return err
	})
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := db.First(&user, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if user.PointsBalance != 10100 {
		t.Fatalf("incorrect balance: %d", user.PointsBalance)
	}
	assertRows(t, db.Where("type = ?", PointLedgerStaffAdd), &model.PointLedger{}, 1)
}
