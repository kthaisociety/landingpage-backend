package handlers

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This driver only consumes scripted responses in memory. It has no network
// implementation, DSN, environment loading, or credentials. It exercises real
// GORM SQL generation and transaction calls without a PostgreSQL server; it
// does not claim to verify PostgreSQL execution or row-lock behavior.
type tqSQLStep struct {
	kind     string
	contains []string
	columns  []string
	rows     [][]driver.Value
	err      error
	affected int64
	check    func(string, []driver.NamedValue)
	after    func()
}

type tqSQLScript struct {
	t     *testing.T
	mu    sync.Mutex
	steps []tqSQLStep
}

func newTeamQuestionsSQL(t *testing.T) (*gorm.DB, *tqSQLScript) {
	t.Helper()
	script := &tqSQLScript{t: t}
	pool := sql.OpenDB(&tqSQLConnector{script: script})
	t.Cleanup(func() {
		require.NoError(t, pool.Close())
		script.mu.Lock()
		defer script.mu.Unlock()
		require.Empty(t, script.steps, "all expected SQL operations must be consumed")
	})
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{
		DisableAutomaticPing: true,
		Logger:               logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	return db, script
}

func (s *tqSQLScript) add(steps ...tqSQLStep) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, steps...)
}

func (s *tqSQLScript) take(kind, query string, args []driver.NamedValue) tqSQLStep {
	s.t.Helper()
	s.mu.Lock()
	if len(s.steps) == 0 {
		s.mu.Unlock()
		s.t.Errorf("unexpected %s: %s", kind, query)
		return tqSQLStep{err: fmt.Errorf("unexpected SQL operation: %s", kind)}
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	s.mu.Unlock()
	if step.kind != kind {
		s.t.Errorf("SQL operation = %s, want %s: %s", kind, step.kind, query)
		return tqSQLStep{err: fmt.Errorf("unexpected SQL operation order")}
	}
	for _, fragment := range step.contains {
		if !strings.Contains(query, fragment) {
			s.t.Errorf("SQL %q does not contain %q", query, fragment)
		}
	}
	if step.check != nil {
		step.check(query, args)
	}
	if step.after != nil {
		step.after()
	}
	return step
}

type tqSQLConnector struct{ script *tqSQLScript }

func (c *tqSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &tqSQLConn{script: c.script}, nil
}

func (c *tqSQLConnector) Driver() driver.Driver { return tqSQLDriver{} }

type tqSQLDriver struct{}

func (tqSQLDriver) Open(string) (driver.Conn, error) {
	return nil, fmt.Errorf("use the in-memory test connector")
}

type tqSQLConn struct{ script *tqSQLScript }

func (c *tqSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("prepared statements are not supported by the test driver")
}

func (c *tqSQLConn) Close() error { return nil }

func (c *tqSQLConn) Begin() (driver.Tx, error) {
	step := c.script.take("begin", "", nil)
	if step.err != nil {
		return nil, step.err
	}
	return &tqSQLTx{script: c.script}, nil
}

func (c *tqSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}

func (c *tqSQLConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step := c.script.take("query", query, args)
	if step.err != nil {
		return nil, step.err
	}
	return &tqSQLRows{columns: step.columns, rows: step.rows}, nil
}

func (c *tqSQLConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	step := c.script.take("exec", query, args)
	return driver.RowsAffected(step.affected), step.err
}

type tqSQLTx struct{ script *tqSQLScript }

func (tx *tqSQLTx) Commit() error {
	return tx.script.take("commit", "", nil).err
}

func (tx *tqSQLTx) Rollback() error {
	return tx.script.take("rollback", "", nil).err
}

type tqSQLRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (r *tqSQLRows) Columns() []string { return r.columns }
func (r *tqSQLRows) Close() error      { return nil }

func (r *tqSQLRows) Next(dest []driver.Value) error {
	if r.index == len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.index])
	r.index++
	return nil
}
