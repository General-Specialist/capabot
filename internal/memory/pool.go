package memory

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
)

// Pool implements a single-writer / multi-reader connection pool for SQLite.
// All connections enforce WAL mode and synchronous=NORMAL.
type Pool struct {
	writer  *sql.DB
	readers *sql.DB
	mu      sync.Mutex // serializes write transactions
}

// NewPool opens a SQLite database at the given path and returns a Pool with
// WAL mode enabled. The writer connection has max 1 open connection;
// readers allow up to maxReaders concurrent connections.
func NewPool(dbPath string, maxReaders int) (*Pool, error) {
	writer, err := openDB(dbPath, 1)
	if err != nil {
		return nil, fmt.Errorf("opening writer connection: %w", err)
	}

	readers, err := openDB(dbPath, maxReaders)
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("opening reader connections: %w", err)
	}

	p := &Pool{
		writer:  writer,
		readers: readers,
	}

	if err := p.configurePragmas(); err != nil {
		p.Close()
		return nil, fmt.Errorf("configuring pragmas: %w", err)
	}

	return p, nil
}

// ReadDB returns the reader connection pool for SELECT queries.
func (p *Pool) ReadDB() *sql.DB {
	return p.readers
}

// WriteDB returns the writer connection for mutations.
// Callers should use WriteTx for transactional writes.
func (p *Pool) WriteDB() *sql.DB {
	return p.writer
}

// WriteTx executes fn within a serialized write transaction.
// Only one write transaction can run at a time.
func (p *Pool) WriteTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	tx, err := p.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning write transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

// Close closes both writer and reader connections.
func (p *Pool) Close() error {
	var errs []error
	if err := p.writer.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing writer: %w", err))
	}
	if err := p.readers.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing readers: %w", err))
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func openDB(path string, maxConns int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(maxConns)
	return db, nil
}

func (p *Pool) configurePragmas() error {
	for _, db := range []*sql.DB{p.writer, p.readers} {
		pragmas := []string{
			"PRAGMA journal_mode=WAL",
			"PRAGMA synchronous=NORMAL",
			"PRAGMA foreign_keys=ON",
			"PRAGMA busy_timeout=5000",
		}
		for _, pragma := range pragmas {
			if _, err := db.Exec(pragma); err != nil {
				return fmt.Errorf("executing %s: %w", pragma, err)
			}
		}
	}
	return nil
}
