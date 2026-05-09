package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportgateway"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store/postgres"

	_ "github.com/lib/pq"
)

const (
	commandName = "afscp-export-gateway"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	return runWithDeps(context.Background(), args, stdout, stderr, commandDeps{
		loadConfig: config.Load,
		serve:      serveGateway,
	})
}

type commandDeps struct {
	loadConfig func(config.Source) (config.Config, error)
	serve      func(context.Context, exportgateway.ServerConfig) error
}

func runWithDeps(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, deps commandDeps) int {
	flags := flag.NewFlagSet(commandName, flag.ContinueOnError)
	flags.SetOutput(stderr)

	showVersion := flags.Bool("version", false, "print version")
	dryRun := flags.Bool("dry-run", false, "validate gateway configuration")
	serve := flags.Bool("serve", false, "serve WebDAV export gateway")
	listenAddr := flags.String("listen-addr", "", "gateway listen address")
	postgresDSN := flags.String("postgres-dsn", "", "gateway postgres dsn")
	volumeRoots := flags.String("volume-roots", "", "comma-separated vol_id=/abs/root mappings")
	prefix := flags.String("prefix", "", "gateway URL prefix")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "%s: unexpected argument %q\n", commandName, redacted(flags.Arg(0)))
		return 2
	}

	if *showVersion {
		fmt.Fprintf(stdout, "%s %s\n", commandName, version)
		return 0
	}
	if !*dryRun && !*serve {
		return 0
	}

	if deps.loadConfig == nil {
		deps.loadConfig = config.Load
	}
	cfg, err := deps.loadConfig(config.EnvSource{})
	if err != nil {
		fmt.Fprintf(stderr, "%s: invalid config: %s\n", commandName, redacted(err.Error()))
		return 1
	}
	gatewayCfg := cfg.ExportGateway
	if *listenAddr != "" {
		gatewayCfg.ListenAddr = strings.TrimSpace(*listenAddr)
	}
	if *postgresDSN != "" {
		gatewayCfg.PostgresDSN = strings.TrimSpace(*postgresDSN)
	}
	if *prefix != "" {
		gatewayCfg.Prefix = strings.TrimSpace(*prefix)
	}
	if *volumeRoots != "" {
		roots, err := config.ParseExportGatewayVolumeRoots(*volumeRoots)
		if err != nil {
			fmt.Fprintf(stderr, "%s: invalid config: %s\n", commandName, redacted(err.Error()))
			return 1
		}
		gatewayCfg.VolumeRoots = roots
	}
	if err := config.ValidateExportGatewayConfig(gatewayCfg); err != nil {
		fmt.Fprintf(stderr, "%s: invalid config: %s\n", commandName, redacted(err.Error()))
		return 1
	}
	if !cfg.Capabilities.WebDAV.Available() {
		fmt.Fprintf(stderr, "%s: WebDAV capability is disabled or not ready\n", commandName)
		return 1
	}
	if *dryRun {
		fmt.Fprintf(stdout, "%s dry-run ok\n", commandName)
		return 0
	}
	if deps.serve == nil {
		deps.serve = serveGateway
	}
	serverCfg := exportgateway.ServerConfig{
		ListenAddr:  gatewayCfg.ListenAddr,
		PostgresDSN: gatewayCfg.PostgresDSN,
		VolumeRoots: gatewayCfg.VolumeRoots,
		Prefix:      gatewayCfg.Prefix,
	}
	if err := deps.serve(ctx, serverCfg); err != nil {
		fmt.Fprintf(stderr, "%s: serve failed: %s\n", commandName, redacted(err.Error()))
		return 1
	}

	return 0
}

func serveGateway(ctx context.Context, cfg exportgateway.ServerConfig) error {
	db, err := sql.Open("postgres", cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer db.Close()

	store := postgres.New(db)
	handler, err := exportgateway.NewHandler(exportgateway.Config{
		Store:       store,
		AuditSink:   auditOutboxSink{store: store},
		VolumeRoots: cfg.VolumeRoots,
		Prefix:      cfg.Prefix,
	})
	if err != nil {
		return err
	}

	server := &http.Server{Addr: cfg.ListenAddr, Handler: handler}
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	err = server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

type auditAppendStore interface {
	AppendAuditEvent(context.Context, audit.Event) error
}

type auditOutboxSink struct {
	store auditAppendStore
}

func (sink auditOutboxSink) Emit(ctx context.Context, event audit.Event) error {
	if sink.store == nil {
		return nil
	}
	return sink.store.AppendAuditEvent(ctx, event)
}

func redacted(value string) string {
	redacted, _ := observability.RedactString(value)
	return redacted
}
