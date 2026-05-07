package copier

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"time"

	_ "github.com/denisenkom/go-mssqldb"
)

type config struct {
	SourceDSN      string
	TargetDSN      string
	ExportDDLFile  string
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

func Main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if err := runMain(); err != nil {
		log.Fatal(err)
	}
}

func runMain() error {
	cfg := parseFlags()
	if cfg.DropExisting && cfg.ExportDDLFile != "" {
		return fmt.Errorf("-drop-existing cannot be combined with -export-ddl")
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
	var cfg config
	flag.StringVar(&cfg.SourceDSN, "source", "", "source SQL Server DSN")
	flag.StringVar(&cfg.TargetDSN, "target", "", "target SQL Server DSN")
	flag.StringVar(&cfg.ExportDDLFile, "export-ddl", "", "write Liquibase-formatted DDL to the given path")
	flag.IntVar(&cfg.Workers, "workers", max(2, runtime.NumCPU()), "number of concurrent table copy workers")
	flag.IntVar(&cfg.BatchSize, "batch-size", 5000, "rows per bulk batch hint")
	flag.BoolVar(&cfg.Verbose, "verbose", true, "log per-table activity")
	flag.BoolVar(&cfg.Plan, "plan", false, "print the filtered execution plan without modifying the target")
	flag.BoolVar(&cfg.DropExisting, "drop-existing", false, "drop matching target tables before recreating them")
	var includeSchemas string
	var excludeSchemas string
	var includeTables string
	var excludeTables string
	flag.StringVar(&includeSchemas, "include-schemas", "", "comma-separated schema names or wildcard patterns to copy")
	flag.StringVar(&excludeSchemas, "exclude-schemas", "", "comma-separated schema names or wildcard patterns to skip")
	flag.StringVar(&includeTables, "include-tables", "", "comma-separated table names, schema.table names, or wildcard patterns to copy")
	flag.StringVar(&excludeTables, "exclude-tables", "", "comma-separated table names, schema.table names, or wildcard patterns to skip")
	flag.Parse()

	if cfg.SourceDSN == "" || (cfg.requiresTarget() && cfg.TargetDSN == "") {
		if cfg.Plan || cfg.ExportDDLFile != "" {
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
	cfg.IncludeSchemas = parseList(includeSchemas)
	cfg.ExcludeSchemas = parseList(excludeSchemas)
	cfg.IncludeTables = parseList(includeTables)
	cfg.ExcludeTables = parseList(excludeTables)
	return cfg
}

func (cfg config) requiresTarget() bool {
	return !cfg.Plan && cfg.ExportDDLFile == ""
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
