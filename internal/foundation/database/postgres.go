package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

func openPostgres(ctx context.Context, cfg Config) (*Database, error) {
	config, err := pgx.ParseConfig(cfg.DSN)
	if err != nil {
		// Parse errors may contain the original connection string and password.
		return nil, errors.New("invalid PostgreSQL database URL")
	}
	if config.ConnectTimeout == 0 {
		config.ConnectTimeout = cfg.BusyTimeout
	}
	config.RuntimeParams["application_name"] = "workbench"
	lockConnector := stdlib.GetConnector(*config)
	guard := &postgresGuard{}
	connector := postgresConnector{Connector: lockConnector, guard: guard}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(cfg.MaxConnections)
	db.SetMaxIdleConns(cfg.MaxConnections)
	database := &Database{db: db, driver: Postgres, lockDB: sql.OpenDB(lockConnector)}
	database.lockDB.SetMaxOpenConns(1)
	database.lockDB.SetMaxIdleConns(1)
	ready := false
	defer func() {
		if !ready {
			_ = database.Close()
		}
	}()
	if err := database.lockDB.PingContext(ctx); err != nil {
		return nil, postgresConnectionError(err)
	}
	database.lockConn, err = database.lockDB.Conn(ctx)
	if err != nil {
		return nil, postgresConnectionError(err)
	}
	var locked bool
	err = database.lockConn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(
		hashtextextended(current_database() || ':' || current_schema() || ':workbench', 0)
	)`).Scan(&locked)
	if err != nil {
		return nil, fmt.Errorf("lock PostgreSQL workspace: %w", err)
	}
	if !locked {
		return nil, errors.New("PostgreSQL workspace is already in use")
	}
	guard.conn = database.lockConn
	ready = true
	return database, nil
}

func postgresConnectionError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("connect PostgreSQL: %w", err)
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		return fmt.Errorf("connect PostgreSQL: SQLSTATE %s", pgError.Code)
	}
	return errors.New("could not connect to PostgreSQL; check the database URL and server availability")
}

// Adapt the generated queries at the driver boundary so *sql.DB, *sql.Tx and
// prepared statements all use the same SQL sources and transaction contracts.
type postgresConnector struct {
	driver.Connector
	guard *postgresGuard
}

func (c postgresConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := c.guard.check(ctx); err != nil {
		return nil, err
	}
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &postgresConn{Conn: conn.(*stdlib.Conn), guard: c.guard}, nil
}

var errPostgresLockLost = errors.New("PostgreSQL workspace lock connection was lost; restart workbench")

// A retained *sql.Conn never reconnects. Losing it permanently fences this
// instance, including existing pooled connections and prepared statements.
type postgresGuard struct {
	mu   sync.Mutex
	conn *sql.Conn
	lost bool
}

func (g *postgresGuard) check(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.lost || g.conn == nil {
		return errPostgresLockLost
	}
	// Request cancellation must not terminate the separate lock session.
	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultBusyTimeout)
	defer cancel()
	if err := g.conn.PingContext(probeCtx); err != nil {
		g.lost = true
		return errPostgresLockLost
	}
	return ctx.Err()
}

type postgresConn struct {
	*stdlib.Conn
	guard *postgresGuard
}

func (c *postgresConn) Ping(ctx context.Context) error {
	if err := c.guard.check(ctx); err != nil {
		return err
	}
	return c.Conn.Ping(ctx)
}

func (c *postgresConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *postgresConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if err := c.guard.check(ctx); err != nil {
		return nil, err
	}
	// SQLite's immediate transactions serialize read-modify-write operations.
	// PostgreSQL must reject conflicting snapshots instead of committing stale
	// task updates, event claims or notification decisions.
	if opts.Isolation == driver.IsolationLevel(sql.LevelDefault) {
		opts.Isolation = driver.IsolationLevel(sql.LevelSerializable)
	}
	tx, err := c.Conn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return postgresTx{Tx: tx, guard: c.guard, ctx: ctx}, nil
}

type postgresTx struct {
	driver.Tx
	guard *postgresGuard
	ctx   context.Context
}

func (t postgresTx) Commit() error {
	if err := t.guard.check(t.ctx); err != nil {
		_ = t.Tx.Rollback()
		return err
	}
	return t.Tx.Commit()
}

func (c *postgresConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *postgresConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if err := c.guard.check(ctx); err != nil {
		return nil, err
	}
	stmt, err := c.Conn.PrepareContext(ctx, rebindPostgres(query))
	if err != nil {
		return nil, err
	}
	return &postgresStmt{Stmt: stmt.(*stdlib.Stmt), guard: c.guard}, nil
}

func (c *postgresConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.guard.check(ctx); err != nil {
		return nil, err
	}
	return c.Conn.ExecContext(ctx, rebindPostgres(query), args)
}

func (c *postgresConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.guard.check(ctx); err != nil {
		return nil, err
	}
	return c.Conn.QueryContext(ctx, rebindPostgres(query), args)
}

type postgresStmt struct {
	*stdlib.Stmt
	guard *postgresGuard
}

func (s *postgresStmt) Exec(args []driver.Value) (driver.Result, error) {
	if err := s.guard.check(context.Background()); err != nil {
		return nil, err
	}
	return s.Stmt.Exec(args)
}

func (s *postgresStmt) Query(args []driver.Value) (driver.Rows, error) {
	if err := s.guard.check(context.Background()); err != nil {
		return nil, err
	}
	return s.Stmt.Query(args)
}

func (s *postgresStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if err := s.guard.check(ctx); err != nil {
		return nil, err
	}
	return s.Stmt.ExecContext(ctx, args)
}

func (s *postgresStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if err := s.guard.check(ctx); err != nil {
		return nil, err
	}
	return s.Stmt.QueryContext(ctx, args)
}

// SQLite's ?N keeps its argument number, including repeated sqlc.arg references.
// Bare ? follows SQLite's rule of using one beyond the highest number so far.
// Quoted strings, identifiers, dollar quotes and comments are copied verbatim.
func rebindPostgres(query string) string {
	if !strings.Contains(query, "?") {
		return query
	}
	var result strings.Builder
	result.Grow(len(query))
	index := 0
	for i := 0; i < len(query); {
		start := i
		switch {
		case query[i] == '\'' || query[i] == '"':
			quote := query[i]
			escaped := quote == '\'' && i > 0 && (query[i-1] == 'E' || query[i-1] == 'e') &&
				(i == 1 || !sqlIdentifierByte(query[i-2]))
			i++
			for i < len(query) {
				if escaped && query[i] == '\\' && i+1 < len(query) {
					i += 2
				} else if query[i] == quote {
					i++
					if i >= len(query) || query[i] != quote {
						break
					}
					i++
				} else {
					i++
				}
			}
		case strings.HasPrefix(query[i:], "--"):
			i += 2
			for i < len(query) && query[i] != '\n' {
				i++
			}
		case strings.HasPrefix(query[i:], "/*"):
			i += 2
			depth := 1
			for i < len(query) && depth > 0 {
				switch {
				case strings.HasPrefix(query[i:], "/*"):
					depth++
					i += 2
				case strings.HasPrefix(query[i:], "*/"):
					depth--
					i += 2
				default:
					i++
				}
			}
		case query[i] == '$':
			end := i + 1
			if end < len(query) && (query[end] == '_' || query[end] >= 'a' && query[end] <= 'z' || query[end] >= 'A' && query[end] <= 'Z') {
				for end < len(query) && sqlIdentifierByte(query[end]) {
					end++
				}
			}
			if end < len(query) && query[end] == '$' {
				tag := query[i : end+1]
				closing := strings.Index(query[end+1:], tag)
				if closing < 0 {
					i = len(query)
				} else {
					i = end + 1 + closing + len(tag)
				}
			} else {
				i++
			}
		case query[i] == '?':
			i++
			for i < len(query) && query[i] >= '0' && query[i] <= '9' {
				i++
			}
			if i > start+1 {
				number, _ := strconv.Atoi(query[start+1 : i])
				index = max(index, number)
				result.WriteByte('$')
				result.WriteString(query[start+1 : i])
			} else {
				index++
				result.WriteByte('$')
				result.WriteString(strconv.Itoa(index))
			}
			continue
		default:
			i++
		}
		result.WriteString(query[start:i])
	}
	return result.String()
}

func sqlIdentifierByte(char byte) bool {
	return char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9'
}
