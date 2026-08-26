// Package dbx is the shared infrastructure helper for the ongrid database.
//
// Ongrid defaults to MySQL (via gorm.io/driver/mysql). SQLite remains
// available as an opt-in backend for single-user local tinkering. The data
// model itself is dialect-agnostic GORM; callers should not depend on any
// dialect-specific SQL.
//
// SQLite pragmas enabled at open time (when Dialect == "sqlite"):
//
//	journal_mode = WAL        // concurrent readers + single writer
//	busy_timeout = 5000 ms    // block briefly instead of SQLITE_BUSY
//	foreign_keys = ON         // SQLite ships with FKs disabled by default
//
// MySQL connections verify reachability with Ping() at Open time so config
// mistakes surface as a fail-fast error instead of lazily at first query.
package dbx

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/glebarez/sqlite"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/ongridio/ongrid/internal/pkg/config"
)

// Open opens the configured database backend. Dialect selects MySQL (default)
// or SQLite; an empty dialect is treated as MySQL for defensive defaults.
//
// The returned *gorm.DB uses a Warn-level logger so the normal query stream
// stays out of the application log. Callers that want query logs should wrap
// with db.Session(&gorm.Session{Logger: ...}) at call sites.
func Open(cfg config.DBConfig, log *slog.Logger) (*gorm.DB, error) {
	switch cfg.Dialect {
	case "", "mysql":
		return openMySQL(cfg.DSN, log)
	case "sqlite":
		return openSQLite(cfg.Path, log)
	default:
		return nil, fmt.Errorf("dbx: unsupported dialect %q", cfg.Dialect)
	}
}

// openMySQL opens a MySQL connection via gorm and verifies reachability
// with Ping(). The DSN password is never logged.
func openMySQL(dsn string, log *slog.Logger) (*gorm.DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("dbx: empty mysql DSN")
	}

	gdb, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("dbx: mysql open: %w", err)
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("dbx: mysql sql.DB handle: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("dbx: mysql ping failed: %w", err)
	}

	if log != nil {
		log.Info("mysql opened", "endpoint", redactDSN(dsn))
	}
	return gdb, nil
}

// openSQLite opens a SQLite database at path with WAL + busy_timeout +
// foreign_keys pragmas. Parent directories are created (0o755) if needed.
//
// Path may be:
//   - a plain filesystem path ("./data/ongrid.db", "/var/lib/ongrid/db")
//   - ":memory:" for an in-memory DB (tests)
func openSQLite(path string, log *slog.Logger) (*gorm.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("dbx: empty sqlite path")
	}

	if path != ":memory:" {
		dir := filepath.Dir(path)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("dbx: sqlite mkdir %q: %w", dir, err)
			}
		}
	}

	dsn := buildSQLiteDSN(path)

	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("dbx: sqlite open %q: %w", path, err)
	}

	if log != nil {
		log.Info("sqlite opened", "path", path, "journal_mode", "WAL", "foreign_keys", "on")
	}
	return gdb, nil
}

// buildSQLiteDSN appends pragma query params expected by modernc/glebarez sqlite.
func buildSQLiteDSN(path string) string {
	if path == ":memory:" {
		return path
	}
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(on)")
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + q.Encode()
}

// redactDSN strips the password from a go-sql-driver/mysql DSN for logging.
//
// The go-sql-driver DSN format is:
//
//	[user[:password]@][net[(addr)]]/dbname[?params]
//
// We drop everything between the first ':' after user and the final '@',
// preserving user@host:port/db?params so operators can still see what
// they're connecting to without leaking credentials.
func redactDSN(dsn string) string {
	at := strings.LastIndex(dsn, "@")
	if at < 0 {
		return dsn
	}
	userinfo := dsn[:at]
	rest := dsn[at:]
	if colon := strings.IndexByte(userinfo, ':'); colon >= 0 {
		userinfo = userinfo[:colon] + ":***"
	}
	return userinfo + rest
}
