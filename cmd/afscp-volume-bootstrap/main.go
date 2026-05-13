package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store/postgres"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/volumebootstrap"
	_ "github.com/lib/pq"
)

const (
	commandName    = "afscp-volume-bootstrap"
	defaultTimeout = 60 * time.Second
	defaultOwner   = "afscp-volume-bootstrap"
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

type bootstrapRunner interface {
	Ensure(context.Context, volumebootstrap.VolumeSpec) (volumebootstrap.Result, error)
	Check(context.Context, volumebootstrap.VolumeSpec) (volumebootstrap.Result, error)
	Close() error
}

type command struct {
	stdout    io.Writer
	stderr    io.Writer
	lookupEnv func(string) (string, bool)
	newRunner func(context.Context, string, volumebootstrap.Config) (bootstrapRunner, error)
}

func newCommand(stdout io.Writer, stderr io.Writer) command {
	return command{
		stdout:    stdout,
		stderr:    stderr,
		lookupEnv: os.LookupEnv,
		newRunner: func(ctx context.Context, dsn string, cfg volumebootstrap.Config) (bootstrapRunner, error) {
			return newPostgresRunner(ctx, dsn, cfg)
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
	ensure := flags.Bool("ensure", false, "ensure the configured default volume exists and is active")
	check := flags.Bool("check", false, "check the configured default volume is active without writing")
	timeout := flags.Duration("timeout", defaultTimeout, "volume bootstrap command timeout")
	leaseDuration := flags.Duration("lease-duration", 5*time.Minute, "volume ensure operation lease duration")
	postgresDSNFlag := flags.String("postgres-dsn", "", "postgres dsn")
	volumeSpecFlag := flags.String("volume-spec", "", "explicit volume spec JSON")
	volumeIDFlag := flags.String("volume-id", "", "default volume id")
	backendFlag := flags.String("backend", "", "default volume backend")
	isolationClassFlag := flags.String("isolation-class", "", "default volume isolation class")
	statusFlag := flags.String("status", "", "default volume status")
	capabilitiesFlag := flags.String("capabilities-json", "", "default volume capabilities JSON object")
	ownerFlag := flags.String("owner", "", "operation lease owner")
	callerServiceFlag := flags.String("caller-service", "", "operation caller service")
	actorTypeFlag := flags.String("actor-type", "", "authorized actor type")
	actorIDFlag := flags.String("actor-id", "", "authorized actor id")
	idempotencyKeyFlag := flags.String("idempotency-key", "", "stable idempotency key for the default volume ensure operation")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(cmd.stderr, "%s: unexpected argument %q\n", commandName, safeError(errors.New(flags.Arg(0))))
		return 2
	}
	if *showVersion {
		fmt.Fprintf(cmd.stdout, "%s %s\n", commandName, version)
		return 0
	}
	if !*ensure && !*check {
		fmt.Fprintf(cmd.stderr, "%s: --ensure or --check is required\n", commandName)
		return 2
	}
	action := volumebootstrap.ActionCheck
	if *ensure {
		action = volumebootstrap.ActionEnsure
	}
	if *timeout <= 0 {
		return cmd.invalidConfig(action, "", "invalid_timeout", "--timeout must be positive")
	}
	if *leaseDuration <= 0 {
		return cmd.invalidConfig(action, "", "invalid_lease_duration", "--lease-duration must be positive")
	}
	dsn := firstNonBlank(*postgresDSNFlag, cmd.envFirst("AFSCP_VOLUME_BOOTSTRAP_POSTGRES_DSN", "AFSCP_POSTGRES_DSN", "AFSCP_DATABASE_URL"))
	if dsn == "" {
		fmt.Fprintf(cmd.stderr, "%s: AFSCP_VOLUME_BOOTSTRAP_POSTGRES_DSN, AFSCP_POSTGRES_DSN, or AFSCP_DATABASE_URL is required\n", commandName)
		return cmd.invalidConfig(action, "", "missing_postgres_dsn", "postgres dsn is required")
	}
	spec, err := cmd.volumeSpec(*volumeSpecFlag, *volumeIDFlag, *backendFlag, *isolationClassFlag, *statusFlag, *capabilitiesFlag)
	if err != nil {
		code := "invalid_volume_spec"
		if strings.TrimSpace(spec.VolumeID) == "" {
			code = "missing_volume_id"
		}
		fmt.Fprintf(cmd.stderr, "%s: invalid volume bootstrap spec: %s\n", commandName, safeError(err))
		return cmd.invalidConfig(action, spec.VolumeID, code, "volume bootstrap spec is invalid")
	}

	runCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	cfg := volumebootstrap.Config{
		Owner:           firstNonBlank(*ownerFlag, cmd.envFirst("AFSCP_VOLUME_BOOTSTRAP_OWNER"), defaultOwner),
		CallerService:   firstNonBlank(*callerServiceFlag, cmd.envFirst("AFSCP_VOLUME_BOOTSTRAP_CALLER_SERVICE"), defaultOwner),
		AuthorizedActor: operations.Actor{Type: firstNonBlank(*actorTypeFlag, cmd.envFirst("AFSCP_VOLUME_BOOTSTRAP_ACTOR_TYPE"), "system"), ID: firstNonBlank(*actorIDFlag, cmd.envFirst("AFSCP_VOLUME_BOOTSTRAP_ACTOR_ID"), defaultOwner)},
		IdempotencyKey:  firstNonBlank(*idempotencyKeyFlag, cmd.envFirst("AFSCP_VOLUME_BOOTSTRAP_IDEMPOTENCY_KEY")),
		LeaseDuration:   *leaseDuration,
		Clock:           func() time.Time { return time.Now().UTC() },
		OperationID:     func() string { return randomID("op_") },
		AuditEventID:    func() string { return randomID("evt_") },
	}
	runner, err := cmd.newRunner(runCtx, dsn, cfg)
	if err != nil {
		fmt.Fprintf(cmd.stderr, "%s: configure volume bootstrap: %s\n", commandName, safeError(err))
		return cmd.invalidConfig(action, spec.VolumeID, "configure_runner_failed", "volume bootstrap runner could not be configured")
	}
	defer func() {
		if err := runner.Close(); err != nil {
			fmt.Fprintf(cmd.stderr, "%s: close volume bootstrap runner: %s\n", commandName, safeError(err))
		}
	}()

	var result volumebootstrap.Result
	if *ensure {
		result, err = runner.Ensure(runCtx, spec)
	} else {
		result, err = runner.Check(runCtx, spec)
	}
	if encodeErr := encodeResult(cmd.stdout, result); encodeErr != nil {
		fmt.Fprintf(cmd.stderr, "%s: encode volume bootstrap result: %s\n", commandName, safeError(encodeErr))
		return 1
	}
	if err != nil || result.Status != volumebootstrap.ResultStatusReady {
		if err != nil {
			fmt.Fprintf(cmd.stderr, "%s: volume bootstrap %s failed: %s\n", commandName, action, safeError(err))
		}
		return 1
	}
	return 0
}

func (cmd command) volumeSpec(rawFlag, volumeIDFlag, backendFlag, isolationClassFlag, statusFlag, capabilitiesFlag string) (volumebootstrap.VolumeSpec, error) {
	var spec volumebootstrap.VolumeSpec
	raw := firstNonBlank(rawFlag, cmd.envFirst("AFSCP_VOLUME_BOOTSTRAP_SPEC", "AFSCP_DEFAULT_VOLUME_SPEC"))
	if raw != "" {
		parsed, err := parseVolumeSpec(raw)
		if err != nil {
			return spec, err
		}
		spec = parsed
	}
	if value := firstNonBlank(volumeIDFlag, cmd.envFirst("AFSCP_VOLUME_BOOTSTRAP_VOLUME_ID", "AFSCP_DEFAULT_VOLUME_ID")); value != "" {
		spec.VolumeID = value
	}
	if value := firstNonBlank(backendFlag, cmd.envFirst("AFSCP_VOLUME_BOOTSTRAP_BACKEND", "AFSCP_DEFAULT_VOLUME_BACKEND")); value != "" {
		spec.Backend = resources.VolumeBackend(value)
	}
	if value := firstNonBlank(isolationClassFlag, cmd.envFirst("AFSCP_VOLUME_BOOTSTRAP_ISOLATION_CLASS", "AFSCP_DEFAULT_VOLUME_ISOLATION_CLASS")); value != "" {
		spec.IsolationClass = resources.VolumeIsolationClass(value)
	}
	if value := firstNonBlank(statusFlag, cmd.envFirst("AFSCP_VOLUME_BOOTSTRAP_STATUS", "AFSCP_DEFAULT_VOLUME_STATUS")); value != "" {
		spec.Status = resources.VolumeStatus(value)
	}
	if value := firstNonBlank(capabilitiesFlag, cmd.envFirst("AFSCP_VOLUME_BOOTSTRAP_CAPABILITIES_JSON", "AFSCP_DEFAULT_VOLUME_CAPABILITIES_JSON")); value != "" {
		capabilities, err := parseCapabilities(value)
		if err != nil {
			return spec, err
		}
		spec.Capabilities = capabilities
	}
	spec.VolumeID = strings.TrimSpace(spec.VolumeID)
	if err := volumebootstrap.ValidateSpec(spec); err != nil {
		return spec, err
	}
	return spec, nil
}

func parseVolumeSpec(raw string) (volumebootstrap.VolumeSpec, error) {
	var body struct {
		VolumeID       string         `json:"volume_id"`
		Backend        string         `json:"backend"`
		IsolationClass string         `json:"isolation_class"`
		Status         string         `json:"status"`
		Capabilities   map[string]any `json:"capabilities"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return volumebootstrap.VolumeSpec{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return volumebootstrap.VolumeSpec{}, errors.New("multiple json values")
	} else if !errors.Is(err, io.EOF) {
		return volumebootstrap.VolumeSpec{}, err
	}
	return volumebootstrap.VolumeSpec{
		VolumeID:       strings.TrimSpace(body.VolumeID),
		Backend:        resources.VolumeBackend(strings.TrimSpace(body.Backend)),
		IsolationClass: resources.VolumeIsolationClass(strings.TrimSpace(body.IsolationClass)),
		Status:         resources.VolumeStatus(strings.TrimSpace(body.Status)),
		Capabilities:   body.Capabilities,
	}, nil
}

func parseCapabilities(raw string) (map[string]any, error) {
	var capabilities map[string]any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&capabilities); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return nil, errors.New("multiple json values")
	} else if !errors.Is(err, io.EOF) {
		return nil, err
	}
	if capabilities == nil {
		return nil, errors.New("capabilities must be a JSON object")
	}
	return capabilities, nil
}

func (cmd command) envFirst(keys ...string) string {
	for _, key := range keys {
		if value, ok := cmd.lookupEnv(key); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (cmd command) invalidConfig(action volumebootstrap.Action, volumeID, code, message string) int {
	_ = encodeResult(cmd.stdout, volumebootstrap.Result{
		SchemaVersion: volumebootstrap.ResultSchemaVersion,
		Action:        action,
		Status:        volumebootstrap.ResultStatusInvalidConfig,
		VolumeID:      strings.TrimSpace(volumeID),
		CheckedAt:     time.Now().UTC().Format(time.RFC3339),
		Findings:      []volumebootstrap.Finding{{Code: volumebootstrap.FindingCode(code), Message: message, Severity: "critical"}},
	})
	return 2
}

type postgresRunner struct {
	db     *sql.DB
	runner *volumebootstrap.Runner
}

func newPostgresRunner(ctx context.Context, dsn string, cfg volumebootstrap.Config) (*postgresRunner, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		closeErr := db.Close()
		return nil, errors.Join(err, closeErr)
	}
	cfg.Store = postgres.New(db)
	runner, err := volumebootstrap.NewRunner(cfg)
	if err != nil {
		closeErr := db.Close()
		return nil, errors.Join(err, closeErr)
	}
	return &postgresRunner{db: db, runner: runner}, nil
}

func (runner *postgresRunner) Ensure(ctx context.Context, spec volumebootstrap.VolumeSpec) (volumebootstrap.Result, error) {
	return runner.runner.Ensure(ctx, spec)
}

func (runner *postgresRunner) Check(ctx context.Context, spec volumebootstrap.VolumeSpec) (volumebootstrap.Result, error) {
	return runner.runner.Check(ctx, spec)
}

func (runner *postgresRunner) Close() error {
	if runner == nil || runner.db == nil {
		return nil
	}
	return runner.db.Close()
}

func encodeResult(stdout io.Writer, result volumebootstrap.Result) error {
	if result.SchemaVersion == "" {
		result.SchemaVersion = volumebootstrap.ResultSchemaVersion
	}
	if result.Status == "" {
		result.Status = volumebootstrap.ResultStatusNotReady
	}
	return json.NewEncoder(stdout).Encode(result)
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	return audit.RedactString(err.Error())
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func randomID(prefix string) string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return prefix + hex.EncodeToString(b[:])
}
