// Package db opens the SQLite database and applies its migrations.
package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

func Connect(path string) (*sql.DB, error) {
	// Several goroutines write at once; wait for the lock rather than fail, and
	// take it when a transaction begins so a read-then-write one cannot deadlock.
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	if _, err := db.Exec(`PRAGMA journal_mode = WAL;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}

	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}
