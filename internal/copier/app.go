package copier

import (
	"bytes"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"time"

	_ "github.com/denisenkom/go-mssqldb"
	"gopkg.in/yaml.v3"
)

const defaultConfigPath = "mssql-copier.yml"

type config struct {
	SourceDSN      string
	TargetDSN      string
	ExportDDLFile  string
	ExportDataFile string
	Workers        int
	BatchSize      int
	Verbose        bool
	Plan           bool
	DropExisting   bool
	IncludeSchemas []string
	ExcludeSchemas []string
	IncludeTables  []string
	ExcludeTables  []string
}

type copier struct {
	cfg        config
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
}

type yamlConfig struct {
	SourceDSN      string   `yaml:"source"`
	TargetDSN      string   `yaml:"target"`
	Workers        *int     `yaml:"workers"`
	BatchSize      *int     `yaml:"batch-size"`
	Verbose        *bool    `yaml:"verbose"`
	Plan           *bool    `yaml:"plan"`
	DropExisting   *bool    `yaml:"drop-existing"`
	IncludeSchemas []string `yaml:"include-schemas"`
	ExcludeSchemas []string `yaml:"exclude-schemas"`
	IncludeTables  []string `yaml:"include-tables"`
	ExcludeTables  []string `yaml:"exclude-tables"`
	ExportDDLFile  *string  `yaml:"export-ddl"`
	ExportDataFile *string  `yaml:"export-data"`
}

func Main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if err := runMain(); err != nil {
		log.Fatal(err)
	}
}

func runMain() error {
	cfg := parseFlags()
	if err := cfg.validate(); err != nil {
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
		targetDB, err = openDB(cfg.TargetDSN, cfg.Workers+2)
		if err != nil {
			return fmt.Errorf("open target: %w", err)
		}
		defer closeAndLog(targetDB, "target database")
	}

	c := &copier{
		cfg:      cfg,
		sourceDB: sourceDB,
		targetDB: targetDB,
	}

	if err := c.run(ctx); err != nil {
		return fmt.Errorf("copy failed: %w", err)
	}

	return nil
}

func parseFlags() config {
	defaultWorkers := max(2, runtime.NumCPU())
	defaultBatchSize := 5000

	var sourceDSN string
	var targetDSN string
	var exportDDLFile string
	var exportDataFile string
	workers := defaultWorkers
	batchSize := defaultBatchSize
	verbose := true
	var plan bool
	var dropExisting bool
	configPath := defaultConfigPath

	flag.StringVar(&configPath, "config", defaultConfigPath, "path to YAML config file")
	flag.StringVar(&sourceDSN, "source", "", "source SQL Server DSN")
	flag.StringVar(&targetDSN, "target", "", "target SQL Server DSN")
	flag.StringVar(&exportDDLFile, "export-ddl", "", "write Liquibase-formatted DDL to the given path")
	flag.StringVar(&exportDataFile, "export-data", "", "write plain SQL data inserts to the given path")
	flag.IntVar(&workers, "workers", defaultWorkers, "number of concurrent table copy workers")
	flag.IntVar(&batchSize, "batch-size", defaultBatchSize, "rows per bulk batch hint")
	flag.BoolVar(&verbose, "verbose", true, "log per-table activity")
	flag.BoolVar(&plan, "plan", false, "print the filtered execution plan without modifying the target")
	flag.BoolVar(&dropExisting, "drop-existing", false, "drop matching target tables before recreating them")
	var includeSchemas string
	var excludeSchemas string
	var includeTables string
	var excludeTables string
	flag.StringVar(&includeSchemas, "include-schemas", "", "comma-separated schema names or wildcard patterns to copy")
	flag.StringVar(&excludeSchemas, "exclude-schemas", "", "comma-separated schema names or wildcard patterns to skip")
	flag.StringVar(&includeTables, "include-tables", "", "comma-separated table names, schema.table names, or wildcard patterns to copy")
	flag.StringVar(&excludeTables, "exclude-tables", "", "comma-separated table names, schema.table names, or wildcard patterns to skip")
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

	if explicit["source"] {
		cfg.SourceDSN = strings.TrimSpace(sourceDSN)
	}
	if explicit["target"] {
		cfg.TargetDSN = strings.TrimSpace(targetDSN)
	}
	if explicit["workers"] {
		cfg.Workers = workers
	}
	if explicit["batch-size"] {
		cfg.BatchSize = batchSize
	}
	if explicit["verbose"] {
		cfg.Verbose = verbose
	}
	if explicit["plan"] {
		cfg.Plan = plan
	}
	if explicit["drop-existing"] {
		cfg.DropExisting = dropExisting
	}
	if explicit["include-schemas"] {
		cfg.IncludeSchemas = parseList(includeSchemas)
	}
	if explicit["exclude-schemas"] {
		cfg.ExcludeSchemas = parseList(excludeSchemas)
	}
	if explicit["include-tables"] {
		cfg.IncludeTables = parseList(includeTables)
	}
	if explicit["exclude-tables"] {
		cfg.ExcludeTables = parseList(excludeTables)
	}

	cfg.ExportDDLFile = strings.TrimSpace(exportDDLFile)
	cfg.ExportDataFile = strings.TrimSpace(exportDataFile)

	if cfg.SourceDSN == "" || (cfg.requiresTarget() && cfg.TargetDSN == "") {
		if cfg.Plan || cfg.ExportDDLFile != "" || cfg.ExportDataFile != "" {
			fmt.Fprintln(os.Stderr, "source DSN is required")
		} else {
			fmt.Fprintln(os.Stderr, "source and target DSNs are required")
		}
		flag.Usage()
		os.Exit(2)
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
			return yamlConfig{}, false, fmt.Errorf("-config cannot be empty")
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
	if cfg.ExportDDLFile != nil {
		return yamlConfig{}, false, fmt.Errorf("config file %q cannot set export-ddl; use the -export-ddl flag", path)
	}
	if cfg.ExportDataFile != nil {
		return yamlConfig{}, false, fmt.Errorf("config file %q cannot set export-data; use the -export-data flag", path)
	}

	return cfg, true, nil
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

func (cfg config) requiresTarget() bool {
	return !cfg.Plan && cfg.ExportDDLFile == "" && cfg.ExportDataFile == ""
}

func (cfg config) validate() error {
	if cfg.ExportDDLFile != "" && cfg.ExportDataFile != "" {
		return fmt.Errorf("-export-ddl cannot be combined with -export-data")
	}
	if cfg.DropExisting && cfg.ExportDDLFile != "" {
		return fmt.Errorf("-drop-existing cannot be combined with -export-ddl")
	}
	if cfg.DropExisting && cfg.ExportDataFile != "" {
		return fmt.Errorf("-drop-existing cannot be combined with -export-data")
	}
	return nil
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
		if err := c.writeLiquibaseInitialQueryFile(); err != nil {
			return err
		}
		log.Printf("wrote DDL export file to %s", c.cfg.ExportDDLFile)
		return nil
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

	log.Printf("completed in %s", time.Since(start).Round(time.Millisecond))
	return nil
}
