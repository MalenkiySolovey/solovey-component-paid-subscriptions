package store

import (
	"path/filepath"
	"testing"

	paid "github.com/MalenkiySolovey/solovey-component-paid-subscriptions"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openPaidTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "payment.db")+"?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return db
}

func newPaidDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := openPaidTestDB(t)
	if err := paid.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func fixtureTransaction(db *gorm.DB, work func(*gorm.DB) error) error {
	return db.Transaction(work)
}
