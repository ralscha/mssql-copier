package copier

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	_ "github.com/denisenkom/go-mssqldb"
	"gopkg.in/yaml.v3"
)

const defaultConfigPath = "mssql-copier.yml"

var (
	confirmationInput  io.Reader = os.Stdin
	confirmationOutput io.Writer = os.Stderr
)

type config struct {
	ConfigPath     string
	SourceDSN      string
	TargetDSN      string
	ExportDDLFile  string
	ExportDataFile string
	ExportDataRows int
	ReportMDFile   string
	Workers        int
	BatchSize      int
	Verbose        bool
	Plan           bool
	DropExisting   bool
	IncludeSchemas []string
	ExcludeSchemas []string
	IncludeTables  []string
	ExcludeTables  []string
	FakeData       map[string]string
	LLM            llmConfig
	Docker         dockerTargetConfig
}

type copier struct {
	cfg        config
	faker      *gofakeit.Faker
	dataFaker  *dataFaker
	sourceDB   *sql.DB
	targetDB   *sql.DB
	tables     []tableMeta
	aliasTypes []aliasTypeMeta
	tableTypes []tableTypeMeta
	sequences  []sequenceMeta
	views      []viewMeta
	functions  []functionMeta
	procedures []procedureMeta
	triggers   []triggerMeta
	synonyms   []synonymMeta
	report     copyReport
}

type yamlConfig struct {
	SourceDSN      string            `yaml:"source"`
	TargetDSN      string            `yaml:"target"`
	ReportMDFile   *string           `yaml:"report-md"`
	Workers        *int              `yaml:"workers"`
	BatchSize      *int              `yaml:"batch-size"`
	Verbose        *bool             `yaml:"verbose"`
	Plan           *bool             `yaml:"plan"`
	DropExisting   *bool             `yaml:"drop-existing"`
	IncludeSchemas []string          `yaml:"include-schemas"`
	ExcludeSchemas []string          `yaml:"exclude-schemas"`
	IncludeTables  []string          `yaml:"include-tables"`
	ExcludeTables  []string          `yaml:"exclude-tables"`
	FakeData       map[string]string `yaml:"fake-data"`
	LLM            *yamlLLMConfig    `yaml:"llm"`
	Docker         *yamlDockerConfig `yaml:"docker"`
	ExportDDLFile  *string           `yaml:"export-ddl"`
	ExportDataFile *string           `yaml:"export-data"`
	ExportDataRows *int              `yaml:"export-data-rows"`
}

func Main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if err := runMain(); err != nil {
		log.Fatal(err)
	}
}

func runMain() error {
	return runTUI(parseFlags())
}

func executeConfig(cfg config) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if cfg.Docker.Enabled {
		if err := cfg.Docker.ensurePassword(); err != nil {
			return fmt.Errorf("generate docker SA password: %w", err)
		}
		dsn, err := setupDockerTarget(cfg.Docker)
		if err != nil {
			return fmt.Errorf("setup docker target: %w", err)
		}
		cfg.TargetDSN = dsn
	}
	if err := confirmTargetPermission(cfg.TargetDSN, cfg.requiresTarget()); err != nil {
		return err
	}
	dataFaker, err := newDataFaker(cfg.FakeData)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sourceDB, err := openDB(cfg.SourceDSN, cfg.Workers+2)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer closeAndLog(sourceDB, "source database")

	var targetDB *sql.DB
	if cfg.requiresTarget() {
		if err := ensureTargetDatabase(cfg.TargetDSN, cfg.Workers+2); err != nil {
			return fmt.Errorf("ensure target database: %w", err)
		}
		targetDB, err = openDB(cfg.TargetDSN, cfg.Workers+2)
		if err != nil {
			return fmt.Errorf("open target: %w", err)
		}
		defer closeAndLog(targetDB, "target database")
	}

	c := &copier{
		cfg:       cfg,
		faker:     gofakeit.New(0),
		dataFaker: dataFaker,
		sourceDB:  sourceDB,
		targetDB:  targetDB,
	}

	if err := c.run(ctx); err != nil {
		return fmt.Errorf("copy failed: %w", err)
	}

	return nil
}

func parseFlags() config {
	defaultWorkers := max(2, runtime.NumCPU())
	defaultBatchSize := 5000
	configureUsage(flag.CommandLine, os.Args[0])

	configPath := defaultConfigPath

	flag.StringVar(&configPath, "config", defaultConfigPath, "path to YAML config file")

	flag.Parse()

	explicit := map[string]bool{}
	flag.Visit(func(f *flag.Flag) {
		explicit[f.Name] = true
	})

	cfg := config{
		Workers:   defaultWorkers,
		BatchSize: defaultBatchSize,
		Verbose:   true,
	}

	yamlCfg, loaded, err := loadYAMLConfig(configPath, explicit["config"])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		flag.Usage()
		os.Exit(2)
	}
	if loaded {
		yamlCfg.applyTo(&cfg)
	}

	cfg.ConfigPath = strings.TrimSpace(configPath)
	if cfg.ConfigPath == "" {
		cfg.ConfigPath = defaultConfigPath
	}
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.BatchSize < 1 {
		cfg.BatchSize = 5000
	}
	return cfg
}

func loadYAMLConfig(path string, required bool) (yamlConfig, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		if required {
			return yamlConfig{}, false, fmt.Errorf("--config cannot be empty")
		}
		return yamlConfig{}, false, nil
	}

	// #nosec G304 -- reading a user-selected local config path is intentional behavior.
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !required && path == defaultConfigPath {
			return yamlConfig{}, false, nil
		}
		return yamlConfig{}, false, fmt.Errorf("read config file %q: %w", path, err)
	}

	var cfg yamlConfig
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return yamlConfig{}, false, fmt.Errorf("parse config file %q: %w", path, err)
	}
	return cfg, true, nil
}

func configureUsage(fs *flag.FlagSet, programName string) {
	fs.Usage = func() {
		out := fs.Output()
		if _, err := fmt.Fprintf(out, "Usage of %s:\n", programName); err != nil {
			return
		}
		fs.VisitAll(func(f *flag.Flag) {
			if out == nil {
				return
			}
			name, usage := flag.UnquoteUsage(f)
			if name == "" {
				name = flagTypeName(f)
			}

			if name == "bool" {
				if _, err := fmt.Fprintf(out, "  --%s\n", f.Name); err != nil {
					out = nil
					return
				}
			} else {
				if _, err := fmt.Fprintf(out, "  --%s %s\n", f.Name, name); err != nil {
					out = nil
					return
				}
			}
			if _, err := fmt.Fprintf(out, "      %s", usage); err != nil {
				out = nil
				return
			}
			if def := defaultValueString(f); def != "" {
				if _, err := fmt.Fprintf(out, " (default %s)", def); err != nil {
					out = nil
					return
				}
			}
			if _, err := fmt.Fprintln(out); err != nil {
				out = nil
			}
		})
	}
}

func flagTypeName(f *flag.Flag) string {
	if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
		return "bool"
	}
	getter, ok := f.Value.(flag.Getter)
	if !ok {
		return flagTypeNameFromString(f.DefValue)
	}
	switch getter.Get().(type) {
	case string:
		return "string"
	case int, int8, int16, int32, int64:
		return "int"
	case bool:
		return "bool"
	default:
		return flagTypeNameFromString(f.DefValue)
	}
}

func flagTypeNameFromString(value string) string {
	switch {
	case value == "":
		return "string"
	case strings.EqualFold(value, "true") || strings.EqualFold(value, "false"):
		return "bool"
	default:
		if _, err := fmt.Sscanf(value, "%d", new(int)); err == nil {
			return "int"
		}
		return "value"
	}
}

func defaultValueString(f *flag.Flag) string {
	if isZeroValue(f, f.DefValue) {
		return ""
	}
	if flagTypeName(f) == "string" {
		return fmt.Sprintf("%q", f.DefValue)
	}
	return f.DefValue
}

func isZeroValue(f *flag.Flag, value string) bool {
	if value == "" {
		return true
	}
	if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
		return value == "false"
	}
	return false
}

func (yamlCfg yamlConfig) applyTo(cfg *config) {
	cfg.SourceDSN = strings.TrimSpace(yamlCfg.SourceDSN)
	cfg.TargetDSN = strings.TrimSpace(yamlCfg.TargetDSN)
	if yamlCfg.Workers != nil {
		cfg.Workers = *yamlCfg.Workers
	}
	if yamlCfg.BatchSize != nil {
		cfg.BatchSize = *yamlCfg.BatchSize
	}
	if yamlCfg.ReportMDFile != nil {
		cfg.ReportMDFile = strings.TrimSpace(*yamlCfg.ReportMDFile)
	}
	if yamlCfg.Verbose != nil {
		cfg.Verbose = *yamlCfg.Verbose
	}
	if yamlCfg.Plan != nil {
		cfg.Plan = *yamlCfg.Plan
	}
	if yamlCfg.DropExisting != nil {
		cfg.DropExisting = *yamlCfg.DropExisting
	}
	cfg.IncludeSchemas = normalizeList(yamlCfg.IncludeSchemas)
	cfg.ExcludeSchemas = normalizeList(yamlCfg.ExcludeSchemas)
	cfg.IncludeTables = normalizeList(yamlCfg.IncludeTables)
	cfg.ExcludeTables = normalizeList(yamlCfg.ExcludeTables)
	cfg.FakeData = normalizeFakeData(yamlCfg.FakeData)
	cfg.LLM = normalizeLLMConfig(yamlCfg.LLM)
	cfg.Docker = normalizeDockerConfig(yamlCfg.Docker)
	if yamlCfg.ExportDDLFile != nil {
		cfg.ExportDDLFile = strings.TrimSpace(*yamlCfg.ExportDDLFile)
	}
	if yamlCfg.ExportDataFile != nil {
		cfg.ExportDataFile = strings.TrimSpace(*yamlCfg.ExportDataFile)
	}
	if yamlCfg.ExportDataRows != nil {
		cfg.ExportDataRows = *yamlCfg.ExportDataRows
	}
}

func normalizeList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if v := normalizeFilterName(value); v != "" {
			normalized = append(normalized, v)
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizeFakeData(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(values))
	for selector, functionName := range values {
		sel := strings.TrimSpace(selector)
		fn := strings.TrimSpace(functionName)
		if sel == "" || fn == "" {
			continue
		}
		normalized[sel] = fn
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func (cfg config) requiresTarget() bool {
	return !cfg.Plan && cfg.ExportDDLFile == "" && cfg.ExportDataFile == ""
}

func sameSourceAndTargetDatabase(sourceDSN, targetDSN string) bool {
	src := parseSQLServerDSNForm(sourceDSN)
	dst := parseSQLServerDSNForm(targetDSN)
	if src.Server == "" || dst.Server == "" {
		return false
	}
	const defaultPort = "1433"
	srcPort := src.Port
	if srcPort == "" {
		srcPort = defaultPort
	}
	dstPort := dst.Port
	if dstPort == "" {
		dstPort = defaultPort
	}
	return strings.EqualFold(src.Server, dst.Server) &&
		srcPort == dstPort &&
		strings.EqualFold(src.Database, dst.Database)
}

func (cfg config) validate() error {
	if cfg.requiresTarget() && sameSourceAndTargetDatabase(cfg.SourceDSN, cfg.TargetDSN) {
		return fmt.Errorf("source and target DSNs must not refer to the same database")
	}
	if cfg.ReportMDFile != "" && cfg.Plan {
		return fmt.Errorf("-report-md cannot be combined with -plan")
	}
	if cfg.ReportMDFile != "" && cfg.ExportDDLFile != "" {
		return fmt.Errorf("-report-md cannot be combined with -export-ddl")
	}
	if cfg.ReportMDFile != "" && cfg.ExportDataFile != "" {
		return fmt.Errorf("-report-md cannot be combined with -export-data")
	}
	if cfg.ExportDataRows < 0 {
		return fmt.Errorf("-export-data-rows must be greater than or equal to 0")
	}
	if cfg.ExportDataRows > 0 && cfg.ExportDataFile == "" {
		return fmt.Errorf("-export-data-rows requires -export-data")
	}
	if cfg.DropExisting && cfg.ExportDDLFile != "" {
		return fmt.Errorf("-drop-existing cannot be combined with -export-ddl")
	}
	if cfg.DropExisting && cfg.ExportDataFile != "" {
		return fmt.Errorf("-drop-existing cannot be combined with -export-data")
	}
	return nil
}

func confirmTargetPermission(targetDSN string, requiresTarget bool) error {
	if !requiresTarget {
		return nil
	}
	if isLocalTargetDSN(targetDSN) {
		return nil
	}

	host := targetHostLabel(targetDSN)
	if _, err := fmt.Fprintf(confirmationOutput, "Target host %q is not local. Type 'yes' to continue: ", host); err != nil {
		return fmt.Errorf("prompt for target confirmation: %w", err)
	}

	response, err := bufio.NewReader(confirmationInput).ReadString('\n')
	if err != nil && err != io.EOF {
		return fmt.Errorf("read target confirmation: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(response), "yes") {
		return nil
	}
	return fmt.Errorf("aborted before connecting to non-local target %q", host)
}

func isLocalTargetDSN(targetDSN string) bool {
	host := parseTargetHost(targetDSN)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func targetHostLabel(targetDSN string) string {
	host := parseTargetHost(targetDSN)
	if host == "" {
		return "unknown"
	}
	return host
}

func parseTargetHost(targetDSN string) string {
	targetDSN = strings.TrimSpace(targetDSN)
	if targetDSN == "" {
		return ""
	}
	if u, err := url.Parse(targetDSN); err == nil && u.Host != "" {
		return strings.TrimSpace(u.Hostname())
	}

	for _, part := range strings.FieldsFunc(targetDSN, func(r rune) bool {
		return r == ';' || r == '\n' || r == '\r'
	}) {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		if normalizedKey != "server" && normalizedKey != "data source" && normalizedKey != "addr" && normalizedKey != "address" && normalizedKey != "network address" {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.TrimPrefix(value, "tcp:")
		value = strings.TrimPrefix(value, "np:")
		value = strings.TrimPrefix(value, "lpc:")
		if value == "" {
			return ""
		}
		if strings.HasPrefix(value, "[") {
			if host, _, err := net.SplitHostPort(value); err == nil {
				return strings.TrimSpace(strings.Trim(host, "[]"))
			}
			return strings.TrimSpace(strings.Trim(value, "[]"))
		}
		if host, _, err := net.SplitHostPort(value); err == nil {
			return strings.TrimSpace(host)
		}
		if idx := strings.LastIndex(value, ","); idx >= 0 {
			if _, err := fmt.Sscanf(strings.TrimSpace(value[idx+1:]), "%d", new(int)); err == nil {
				value = value[:idx]
			}
		}
		if idx := strings.IndexAny(value, "\\/"); idx >= 0 {
			value = value[:idx]
		}
		return strings.TrimSpace(value)
	}

	return ""
}

func openDB(dsn string, maxConns int) (*sql.DB, error) {
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, err
	}
	db.SetConnMaxIdleTime(2 * time.Minute)
	db.SetConnMaxLifetime(0)
	db.SetMaxIdleConns(maxConns)
	db.SetMaxOpenConns(maxConns)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		closeAndLog(db, "database after failed ping")
		return nil, err
	}
	return db, nil
}

func ensureTargetDatabase(targetDSN string, maxConns int) error {
	databaseName := sqlServerDSNDatabaseName(targetDSN)
	if databaseName == "" || strings.EqualFold(databaseName, "master") {
		return nil
	}

	adminDSN, err := sqlServerDSNWithDatabase(targetDSN, "master")
	if err != nil {
		return fmt.Errorf("rewrite target dsn for master: %w", err)
	}

	adminDB, err := openDB(adminDSN, max(1, maxConns))
	if err != nil {
		return fmt.Errorf("open target admin connection: %w", err)
	}
	defer closeAndLog(adminDB, "target admin database")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var exists int
	if err := adminDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sys.databases WHERE name = @p1", databaseName).Scan(&exists); err != nil {
		return fmt.Errorf("check target database %q: %w", databaseName, err)
	}
	if exists > 0 {
		return nil
	}

	if _, err := adminDB.ExecContext(ctx, "IF DB_ID(@p1) IS NULL BEGIN DECLARE @sql nvarchar(max) = N'CREATE DATABASE ' + QUOTENAME(@p1); EXEC(@sql); END", databaseName); err != nil {
		return fmt.Errorf("create target database %q: %w", databaseName, err)
	}
	log.Printf("created target database %q", databaseName)
	return nil
}

func (c *copier) run(ctx context.Context) error {
	start := time.Now()
	log.Printf("discovering source metadata")
	tables, err := c.loadMetadata(ctx)
	if err != nil {
		return err
	}
	c.tables = tables
	log.Printf("discovered %d tables", len(c.tables))

	aliasTypes, err := c.loadAliasTypes(ctx)
	if err != nil {
		return err
	}
	c.aliasTypes = aliasTypes
	log.Printf("discovered %d alias types", len(c.aliasTypes))

	tableTypes, err := c.loadTableTypes(ctx)
	if err != nil {
		return err
	}
	c.tableTypes = tableTypes
	log.Printf("discovered %d table types", len(c.tableTypes))

	sequences, err := c.loadSequences(ctx)
	if err != nil {
		return err
	}
	c.sequences = sequences
	log.Printf("discovered %d sequences", len(c.sequences))

	views, err := c.loadViews(ctx)
	if err != nil {
		return err
	}
	c.views = views
	log.Printf("discovered %d views", len(c.views))

	functions, err := c.loadFunctions(ctx)
	if err != nil {
		return err
	}
	c.functions = functions
	log.Printf("discovered %d functions", len(c.functions))

	procedures, err := c.loadProcedures(ctx)
	if err != nil {
		return err
	}
	c.procedures = procedures

	triggers, err := c.loadTriggers(ctx)
	if err != nil {
		return err
	}
	c.triggers = triggers

	synonyms, err := c.loadSynonyms(ctx)
	if err != nil {
		return err
	}
	c.synonyms = synonyms
	log.Printf("discovered %d synonyms", len(c.synonyms))

	if err := c.resolveProcedureDependencies(ctx); err != nil {
		return err
	}
	log.Printf("discovered %d procedures", len(c.procedures))

	if err := c.resolveTriggerDependencies(ctx); err != nil {
		return err
	}
	log.Printf("discovered %d triggers", len(c.triggers))

	if c.cfg.Plan {
		c.printPlan()
		return nil
	}
	if c.cfg.ExportDDLFile != "" {
		if err := c.writeFlywayBaselineFile(); err != nil {
			return err
		}
		log.Printf("wrote DDL export file to %s", c.cfg.ExportDDLFile)
		if c.cfg.ExportDataFile == "" {
			return nil
		}
	}
	if c.cfg.ExportDataFile != "" {
		if err := c.writeDataExportFile(ctx); err != nil {
			return err
		}
		log.Printf("wrote data export file to %s", c.cfg.ExportDataFile)
		return nil
	}

	if err := c.createSchemas(ctx); err != nil {
		return err
	}
	if err := c.prepareTargetTables(ctx); err != nil {
		return err
	}
	if err := c.createAliasTypes(ctx); err != nil {
		return err
	}
	if err := c.createTableTypes(ctx); err != nil {
		return err
	}
	if err := c.createSequences(ctx); err != nil {
		return err
	}
	if err := c.createTables(ctx); err != nil {
		return err
	}
	if err := c.copyTableData(ctx); err != nil {
		return err
	}
	if err := c.createPostDataObjects(ctx); err != nil {
		return err
	}
	if err := c.createViews(ctx); err != nil {
		return err
	}
	if err := c.createFunctions(ctx); err != nil {
		return err
	}
	if err := c.createSynonyms(ctx); err != nil {
		return err
	}
	if err := c.createProcedures(ctx); err != nil {
		return err
	}
	if err := c.createTriggers(ctx); err != nil {
		return err
	}
	c.logSkippedIndexSummary()
	if err := c.writeMarkdownReport(time.Since(start)); err != nil {
		return err
	}

	log.Printf("completed in %s", time.Since(start).Round(time.Millisecond))
	return nil
}
