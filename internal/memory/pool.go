package memory

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Pool wraps a Postgres connection pool.
type Pool struct {
	db *sql.DB
}

// NewPool opens a Postgres database at the given DSN and returns a Pool.
func NewPool(dsn string) (*Pool, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return &Pool{db: db}, nil
}

// DB returns the underlying *sql.DB for queries.
func (p *Pool) DB() *sql.DB {
	return p.db
}

// WriteTx executes fn within a transaction.
func (p *Pool) WriteTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

// Close closes the connection pool.
func (p *Pool) Close() error {
	return p.db.Close()
}
