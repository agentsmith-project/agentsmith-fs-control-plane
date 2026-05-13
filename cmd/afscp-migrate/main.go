package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/schemamigration"
	_ "github.com/lib/pq"
)

const (
	commandName    = "afscp-migrate"
	defaultTimeout = 60 * time.Second
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(newCommand(os.Stdout, os.Stderr).runContext(ctx, os.Args[1:]))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	return newCommand(stdout, stderr).run(args)
}

type migrationRunner interface {
	Apply(context.Context) (schemamigration.Result, error)
	Check(context.Context) (schemamigration.Result, error)
	Close() error
}

type command struct {
	stdout    io.Writer
	stderr    io.Writer
	lookupEnv func(string) (string, bool)
	newRunner func(context.Context, string) (migrationRunner, error)
}

func newCommand(stdout io.Writer, stderr io.Writer) command {
	return command{
		stdout:    stdout,
		stderr:    stderr,
		lookupEnv: os.LookupEnv,
		newRunner: func(ctx context.Context, dsn string) (migrationRunner, error) {
			return newPostgresRunner(ctx, dsn)
		},
	}
}

func (cmd command) run(args []string) int {
	return cmd.runContext(context.Background(), args)
}

func (cmd command) runContext(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet(commandName, flag.ContinueOnError)
	flags.SetOutput(cmd.stderr)

	showVersion := flags.Bool("version", false, "print version")
	apply := flags.Bool("apply", false, "apply pending schema migrations")
	check := flags.Bool("check", false, "verify all schema migrations and required runtime tables are ready")
	timeout := flags.Duration("timeout", defaultTimeout, "schema migration command timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(cmd.stderr, "%s: unexpected argument %q\n", commandName, audit.RedactString(flags.Arg(0)))
		return 2
	}

	if *showVersion {
		fmt.Fprintf(cmd.stdout, "%s %s\n", commandName, version)
		return 0
	}
	if !*apply && !*check {
		fmt.Fprintf(cmd.stderr, "%s: --apply or --check is required\n", commandName)
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintf(cmd.stderr, "%s: --timeout must be positive\n", commandName)
		return 2
	}

	dsn := cmd.postgresDSN()
	if dsn == "" {
		fmt.Fprintf(cmd.stderr, "%s: AFSCP_MIGRATION_POSTGRES_DSN, AFSCP_POSTGRES_DSN, or AFSCP_DATABASE_URL is required\n", commandName)
		return 2
	}

	runCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	runner, err := cmd.newRunner(runCtx, dsn)
	if err != nil {
		fmt.Fprintf(cmd.stderr, "%s: configure schema bootstrap: %s\n", commandName, safeError(err))
		return 2
	}
	defer func() {
		if err := runner.Close(); err != nil {
			fmt.Fprintf(cmd.stderr, "%s: close schema bootstrap runner: %s\n", commandName, safeError(err))
		}
	}()

	var result schemamigration.Result
	if *apply {
		result, err = runner.Apply(runCtx)
		if err != nil {
			encodeResult(cmd.stdout, result)
			fmt.Fprintf(cmd.stderr, "%s: apply schema migrations: %s\n", commandName, safeError(err))
			return 1
		}
	}
	if *check {
		result, err = runner.Check(runCtx)
		if err != nil {
			encodeResult(cmd.stdout, result)
			fmt.Fprintf(cmd.stderr, "%s: check schema readiness: %s\n", commandName, safeError(err))
			return 1
		}
	}
	if err := encodeResult(cmd.stdout, result); err != nil {
		fmt.Fprintf(cmd.stderr, "%s: encode schema bootstrap result: %s\n", commandName, safeError(err))
		return 1
	}
	if result.Status != "ready" {
		fmt.Fprintf(cmd.stderr, "%s: schema is not ready: pending_migrations=%d missing_required_tables=%d\n", commandName, len(result.PendingMigrations), len(result.MissingRequiredTables))
		return 1
	}
	return 0
}

func (cmd command) postgresDSN() string {
	for _, key := range []string{"AFSCP_MIGRATION_POSTGRES_DSN", "AFSCP_POSTGRES_DSN", "AFSCP_DATABASE_URL"} {
		if value, ok := cmd.lookupEnv(key); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type postgresRunner struct {
	db     *sql.DB
	conn   *sql.Conn
	runner schemamigration.Runner
}

func newPostgresRunner(ctx context.Context, dsn string) (*postgresRunner, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		closeErr := db.Close()
		return nil, errors.Join(err, closeErr)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		closeErr := db.Close()
		return nil, errors.Join(err, closeErr)
	}
	runner, err := schemamigration.NewRunner(sqlConnDB{conn: conn})
	if err != nil {
		closeErr := errors.Join(conn.Close(), db.Close())
		return nil, errors.Join(err, closeErr)
	}
	return &postgresRunner{db: db, conn: conn, runner: runner}, nil
}

type sqlConnDB struct {
	conn *sql.Conn
}

func (db sqlConnDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.conn.ExecContext(ctx, query, args...)
}

func (db sqlConnDB) QueryContext(ctx context.Context, query string, args ...any) (schemamigration.RowsScanner, error) {
	return db.conn.QueryContext(ctx, query, args...)
}

func (db sqlConnDB) QueryRowContext(ctx context.Context, query string, args ...any) schemamigration.RowScanner {
	return db.conn.QueryRowContext(ctx, query, args...)
}

func (runner *postgresRunner) Apply(ctx context.Context) (schemamigration.Result, error) {
	return runner.runner.Apply(ctx)
}

func (runner *postgresRunner) Check(ctx context.Context) (schemamigration.Result, error) {
	return runner.runner.Check(ctx)
}

func (runner *postgresRunner) Close() error {
	if runner == nil {
		return nil
	}
	return errors.Join(runner.conn.Close(), runner.db.Close())
}

func encodeResult(stdout io.Writer, result schemamigration.Result) error {
	if result.SchemaVersion == "" {
		result.SchemaVersion = schemamigration.ResultSchemaVersion
	}
	if result.Status == "" {
		result.Status = "not_ready"
	}
	return json.NewEncoder(stdout).Encode(result)
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	return audit.RedactString(err.Error())
}
