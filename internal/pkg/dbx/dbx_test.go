package dbx

import (
	"testing"

	"github.com/ongridio/ongrid/internal/pkg/config"
	"gorm.io/gorm"
)

// The MySQL path is exercised manually via `docker compose up`; tests here
// stick to the SQLite in-memory backend because CI has no docker.

func TestOpen_SQLiteInMemory(t *testing.T) {
	cfg := config.DBConfig{Dialect: "sqlite", Path: ":memory:"}
	db, err := Open(cfg, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if db == nil {
		t.Fatal("Open returned nil *gorm.DB")
	}
	// Sanity: a trivial SELECT should work.
	var one int
	if err := db.Raw("SELECT 1").Scan(&one).Error; err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if one != 1 {
		t.Errorf("SELECT 1 = %d, want 1", one)
	}
}

func TestOpen_DefaultsToMySQL(t *testing.T) {
	// Empty Dialect should route to the MySQL branch. We only assert that
	// we hit the MySQL ping failure (no server available in tests) and not
	// the "unsupported dialect" path; the error message is the contract.
	cfg := config.DBConfig{DSN: "ongrid:ongrid@tcp(127.0.0.1:1)/ongrid"}
	_, err := Open(cfg, nil)
	if err == nil {
		t.Fatal("expected error (no mysql reachable), got nil")
	}
	// We do NOT want to see "unsupported dialect" — that would mean the
	// defensive default routing is broken.
	if got := err.Error(); got == "" {
		t.Fatalf("empty error")
	}
	if contains(err.Error(), "unsupported dialect") {
		t.Errorf("empty Dialect should default to mysql, got %q", err.Error())
	}
}

func TestOpen_UnsupportedDialect(t *testing.T) {
	cfg := config.DBConfig{Dialect: "postgres"}
	_, err := Open(cfg, nil)
	if err == nil {
		t.Fatal("expected error for unsupported dialect")
	}
	if !contains(err.Error(), "unsupported dialect") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "unsupported dialect")
	}
}

// fakeModel is a tiny model used to exercise RunMigrations end-to-end
// against the SQLite :memory: backend.
type fakeModel struct {
	ID   uint64 `gorm:"primaryKey"`
	Name string
}

func (fakeModel) TableName() string { return "fake_models" }

func fakeMigrator(db *gorm.DB) error {
	return db.AutoMigrate(&fakeModel{})
}

func TestRunMigrations_AppliesAndLogs(t *testing.T) {
	db, err := Open(config.DBConfig{Dialect: "sqlite", Path: ":memory:"}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := RunMigrations(db, nil, fakeMigrator); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Confirm the fake_models table exists in sqlite_master.
	var name string
	if err := db.Raw(
		`SELECT name FROM sqlite_master WHERE type='table' AND name=?`,
		"fake_models",
	).Scan(&name).Error; err != nil {
		t.Fatalf("lookup fake_models: %v", err)
	}
	if name != "fake_models" {
		t.Errorf("table fake_models missing (got %q)", name)
	}

	// Running again should also succeed (AutoMigrate is idempotent).
	if err := RunMigrations(db, nil, fakeMigrator); err != nil {
		t.Fatalf("RunMigrations (second run): %v", err)
	}
}

func TestRunMigrations_NilDBRejected(t *testing.T) {
	if err := RunMigrations(nil, nil, fakeMigrator); err == nil {
		t.Fatal("expected error for nil db")
	}
}

func TestRunMigrations_NilMigratorRejected(t *testing.T) {
	db, err := Open(config.DBConfig{Dialect: "sqlite", Path: ":memory:"}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := RunMigrations(db, nil, nil); err == nil {
		t.Fatal("expected error for nil migrator")
	}
}

func TestRedactDSN(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{
			in:   "ongrid:ongrid@tcp(127.0.0.1:3306)/ongrid?parseTime=true",
			want: "ongrid:***@tcp(127.0.0.1:3306)/ongrid?parseTime=true",
		},
		{
			in:   "root:hunter2@tcp(mysql:3306)/db",
			want: "root:***@tcp(mysql:3306)/db",
		},
		{
			in:   "noauth@tcp(host:3306)/db",
			want: "noauth@tcp(host:3306)/db",
		},
		{
			in:   "/no/at/sign",
			want: "/no/at/sign",
		},
	}
	for _, c := range cases {
		if got := redactDSN(c.in); got != c.want {
			t.Errorf("redactDSN(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// contains is a tiny helper to avoid importing "strings" just for one call.
func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	n := len(sub)
	if n == 0 {
		return 0
	}
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return i
		}
	}
	return -1
}
