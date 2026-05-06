package copier

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestCopierIntegration(t *testing.T) {
	if os.Getenv("COPY_MSSQL_RUN_INTEGRATION") == "" {
		t.Skip("set COPY_MSSQL_RUN_INTEGRATION=1 to run the integration test")
	}
	ctx := context.Background()

	sourceContainer, sourceDSN := startSQLServerContainer(ctx, t, "copy-mssql-source")
	defer terminateContainer(ctx, t, sourceContainer)
	targetContainer, targetDSN := startSQLServerContainer(ctx, t, "copy-mssql-target")
	defer terminateContainer(ctx, t, targetContainer)

	sourceDB, err := openTestDB(ctx, sourceDSN, 4)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer sourceDB.Close()

	targetDB, err := openTestDB(ctx, targetDSN, 4)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer targetDB.Close()

	schemaName := fmt.Sprintf("sales_it_%d", time.Now().UnixNano())
	tableFQTN := quoteIdent(schemaName) + ".[sample]"
	excludedTableFQTN := quoteIdent(schemaName) + ".[audit_2026]"

	cleanupSource := fmt.Sprintf("IF OBJECT_ID(N'%s', 'U') IS NOT NULL DROP TABLE %s; IF OBJECT_ID(N'%s', 'U') IS NOT NULL DROP TABLE %s; IF SCHEMA_ID(N'%s') IS NOT NULL EXEC(N'DROP SCHEMA %s');", escapeSQLString(excludedTableFQTN), excludedTableFQTN, escapeSQLString(tableFQTN), tableFQTN, escapeSQLString(schemaName), quoteIdent(schemaName))
	cleanupTarget := cleanupSource
	defer sourceDB.ExecContext(ctx, cleanupSource)
	defer targetDB.ExecContext(ctx, cleanupTarget)

	seedStatements := []string{
		fmt.Sprintf("CREATE SCHEMA %s;", quoteIdent(schemaName)),
		fmt.Sprintf("CREATE TABLE %s ([id] int NOT NULL, [name] nvarchar(50) NOT NULL, CONSTRAINT [PK_sample] PRIMARY KEY CLUSTERED ([id] ASC));", tableFQTN),
		fmt.Sprintf("CREATE TABLE %s ([id] int NOT NULL, [message] nvarchar(50) NOT NULL, CONSTRAINT [PK_audit_2026] PRIMARY KEY CLUSTERED ([id] ASC));", excludedTableFQTN),
		fmt.Sprintf("INSERT INTO %s ([id], [name]) VALUES (1, N'alpha'), (2, N'beta');", tableFQTN),
		fmt.Sprintf("INSERT INTO %s ([id], [message]) VALUES (99, N'should be skipped');", excludedTableFQTN),
	}
	for _, statement := range seedStatements {
		if _, err := sourceDB.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed source: %v", err)
		}
	}

	c := &copier{
		cfg: config{
			SourceDSN:      sourceDSN,
			TargetDSN:      targetDSN,
			Workers:        2,
			BatchSize:      1000,
			Verbose:        false,
			DropExisting:   true,
			IncludeSchemas: []string{"sales*"},
			ExcludeTables:  []string{"*.audit_%"},
		},
		sourceDB: sourceDB,
		targetDB: targetDB,
	}

	if err := c.run(ctx); err != nil {
		t.Fatalf("run copier: %v", err)
	}

	var count int
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", tableFQTN)).Scan(&count); err != nil {
		t.Fatalf("count target rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 copied rows, got %d", count)
	}

	var value string
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("SELECT [name] FROM %s WHERE [id] = 2", tableFQTN)).Scan(&value); err != nil {
		t.Fatalf("read copied row: %v", err)
	}
	if value != "beta" {
		t.Fatalf("expected copied value beta, got %q", value)
	}

	var excludedCount int
	err = targetDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sys.tables t JOIN sys.schemas s ON s.schema_id = t.schema_id WHERE s.name = @p1 AND t.name = @p2", schemaName, "audit_2026").Scan(&excludedCount)
	if err != nil {
		t.Fatalf("check excluded table presence: %v", err)
	}
	if excludedCount != 0 {
		t.Fatalf("expected wildcard-excluded table not to be copied, found %d matching target tables", excludedCount)
	}
}

func TestCopierIntegrationViews(t *testing.T) {
	if os.Getenv("COPY_MSSQL_RUN_INTEGRATION") == "" {
		t.Skip("set COPY_MSSQL_RUN_INTEGRATION=1 to run the integration test")
	}
	ctx := context.Background()

	sourceContainer, sourceDSN := startSQLServerContainer(ctx, t, "copy-mssql-source-views")
	defer terminateContainer(ctx, t, sourceContainer)
	targetContainer, targetDSN := startSQLServerContainer(ctx, t, "copy-mssql-target-views")
	defer terminateContainer(ctx, t, targetContainer)

	sourceDB, err := openTestDB(ctx, sourceDSN, 4)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer sourceDB.Close()

	targetDB, err := openTestDB(ctx, targetDSN, 4)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer targetDB.Close()

	tableSchema := fmt.Sprintf("sales_view_it_%d", time.Now().UnixNano())
	viewSchema := fmt.Sprintf("report_view_it_%d", time.Now().UnixNano())
	tableFQTN := quoteIdent(tableSchema) + ".[orders]"
	baseViewFQTN := quoteIdent(viewSchema) + ".[base_orders]"
	childViewFQTN := quoteIdent(viewSchema) + ".[order_names]"

	cleanup := fmt.Sprintf(
		"IF OBJECT_ID(N'%s', 'V') IS NOT NULL DROP VIEW %s; "+
			"IF OBJECT_ID(N'%s', 'V') IS NOT NULL DROP VIEW %s; "+
			"IF OBJECT_ID(N'%s', 'U') IS NOT NULL DROP TABLE %s; "+
			"IF SCHEMA_ID(N'%s') IS NOT NULL EXEC(N'DROP SCHEMA %s'); "+
			"IF SCHEMA_ID(N'%s') IS NOT NULL EXEC(N'DROP SCHEMA %s');",
		escapeSQLString(childViewFQTN), childViewFQTN,
		escapeSQLString(baseViewFQTN), baseViewFQTN,
		escapeSQLString(tableFQTN), tableFQTN,
		escapeSQLString(viewSchema), quoteIdent(viewSchema),
		escapeSQLString(tableSchema), quoteIdent(tableSchema),
	)
	defer sourceDB.ExecContext(ctx, cleanup)
	defer targetDB.ExecContext(ctx, cleanup)

	seedStatements := []string{
		fmt.Sprintf("CREATE SCHEMA %s;", quoteIdent(tableSchema)),
		fmt.Sprintf("CREATE SCHEMA %s;", quoteIdent(viewSchema)),
		fmt.Sprintf("CREATE TABLE %s ([id] int NOT NULL, [name] nvarchar(50) NOT NULL, [amount] int NOT NULL, CONSTRAINT [PK_orders] PRIMARY KEY CLUSTERED ([id] ASC));", tableFQTN),
		fmt.Sprintf("INSERT INTO %s ([id], [name], [amount]) VALUES (1, N'alpha', 10), (2, N'beta', 20);", tableFQTN),
		fmt.Sprintf("CREATE VIEW %s AS SELECT [id], [name], [amount] FROM %s WHERE [amount] >= 10;", baseViewFQTN, tableFQTN),
		fmt.Sprintf("CREATE VIEW %s AS SELECT [id], [name] FROM %s;", childViewFQTN, baseViewFQTN),
	}
	for _, statement := range seedStatements {
		if _, err := sourceDB.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed source: %v", err)
		}
	}

	c := &copier{
		cfg: config{
			SourceDSN:      sourceDSN,
			TargetDSN:      targetDSN,
			Workers:        2,
			BatchSize:      1000,
			Verbose:        false,
			DropExisting:   true,
			IncludeSchemas: []string{tableSchema, viewSchema},
		},
		sourceDB: sourceDB,
		targetDB: targetDB,
	}

	if err := c.run(ctx); err != nil {
		t.Fatalf("run copier first pass: %v", err)
	}

	var schemaCount int
	if err := targetDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sys.schemas WHERE name = @p1", viewSchema).Scan(&schemaCount); err != nil {
		t.Fatalf("check target view schema: %v", err)
	}
	if schemaCount != 1 {
		t.Fatalf("expected target schema %s to exist, got count %d", viewSchema, schemaCount)
	}

	var childCount int
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", childViewFQTN)).Scan(&childCount); err != nil {
		t.Fatalf("query child view: %v", err)
	}
	if childCount != 2 {
		t.Fatalf("expected 2 rows from copied child view, got %d", childCount)
	}

	if _, err := sourceDB.ExecContext(ctx, fmt.Sprintf("ALTER VIEW %s AS SELECT [id], [name] FROM %s WHERE [amount] >= 20;", baseViewFQTN, tableFQTN)); err != nil {
		t.Fatalf("alter source base view: %v", err)
	}

	if err := c.run(ctx); err != nil {
		t.Fatalf("run copier second pass: %v", err)
	}

	var filteredCount int
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", childViewFQTN)).Scan(&filteredCount); err != nil {
		t.Fatalf("query child view after rerun: %v", err)
	}
	if filteredCount != 1 {
		t.Fatalf("expected rerun to update dependent view output to 1 row, got %d", filteredCount)
	}

	var remainingName string
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("SELECT TOP 1 [name] FROM %s", childViewFQTN)).Scan(&remainingName); err != nil {
		t.Fatalf("read remaining child view row: %v", err)
	}
	if remainingName != "beta" {
		t.Fatalf("expected updated child view to return beta, got %q", remainingName)
	}
}

func TestCopierIntegrationSequencesAndProcedures(t *testing.T) {
	if os.Getenv("COPY_MSSQL_RUN_INTEGRATION") == "" {
		t.Skip("set COPY_MSSQL_RUN_INTEGRATION=1 to run the integration test")
	}
	ctx := context.Background()

	sourceContainer, sourceDSN := startSQLServerContainer(ctx, t, "copy-mssql-source-objects")
	defer terminateContainer(ctx, t, sourceContainer)
	targetContainer, targetDSN := startSQLServerContainer(ctx, t, "copy-mssql-target-objects")
	defer terminateContainer(ctx, t, targetContainer)

	sourceDB, err := openTestDB(ctx, sourceDSN, 4)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer sourceDB.Close()

	targetDB, err := openTestDB(ctx, targetDSN, 4)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer targetDB.Close()

	dataSchema := fmt.Sprintf("sales_obj_it_%d", time.Now().UnixNano())
	seqSchema := fmt.Sprintf("seq_obj_it_%d", time.Now().UnixNano())
	procSchema := fmt.Sprintf("proc_obj_it_%d", time.Now().UnixNano())
	tableFQTN := quoteIdent(dataSchema) + ".[orders]"
	sequenceFQTN := quoteIdent(seqSchema) + ".[order_seq]"
	procedureFQTN := quoteIdent(procSchema) + ".[count_orders]"

	cleanup := fmt.Sprintf(
		"IF OBJECT_ID(N'%s', 'P') IS NOT NULL DROP PROCEDURE %s; "+
			"IF OBJECT_ID(N'%s', 'U') IS NOT NULL DROP TABLE %s; "+
			"IF OBJECT_ID(N'%s', 'SO') IS NOT NULL DROP SEQUENCE %s; "+
			"IF SCHEMA_ID(N'%s') IS NOT NULL EXEC(N'DROP SCHEMA %s'); "+
			"IF SCHEMA_ID(N'%s') IS NOT NULL EXEC(N'DROP SCHEMA %s'); "+
			"IF SCHEMA_ID(N'%s') IS NOT NULL EXEC(N'DROP SCHEMA %s');",
		escapeSQLString(procedureFQTN), procedureFQTN,
		escapeSQLString(tableFQTN), tableFQTN,
		escapeSQLString(sequenceFQTN), sequenceFQTN,
		escapeSQLString(procSchema), quoteIdent(procSchema),
		escapeSQLString(seqSchema), quoteIdent(seqSchema),
		escapeSQLString(dataSchema), quoteIdent(dataSchema),
	)
	defer sourceDB.ExecContext(ctx, cleanup)
	defer targetDB.ExecContext(ctx, cleanup)

	seedStatements := []string{
		fmt.Sprintf("CREATE SCHEMA %s;", quoteIdent(dataSchema)),
		fmt.Sprintf("CREATE SCHEMA %s;", quoteIdent(seqSchema)),
		fmt.Sprintf("CREATE SCHEMA %s;", quoteIdent(procSchema)),
		fmt.Sprintf("CREATE SEQUENCE %s AS int START WITH 100 INCREMENT BY 5 MINVALUE 100 MAXVALUE 1000 NO CYCLE NO CACHE;", sequenceFQTN),
		fmt.Sprintf("CREATE TABLE %s ([id] int NOT NULL CONSTRAINT [DF_orders_id] DEFAULT NEXT VALUE FOR %s, [name] nvarchar(50) NOT NULL, CONSTRAINT [PK_orders] PRIMARY KEY CLUSTERED ([id] ASC));", tableFQTN, sequenceFQTN),
		fmt.Sprintf("INSERT INTO %s ([name]) VALUES (N'alpha');", tableFQTN),
		fmt.Sprintf("INSERT INTO %s ([name]) VALUES (N'beta');", tableFQTN),
		fmt.Sprintf("CREATE PROCEDURE %s @minID int AS BEGIN SELECT COUNT(*) AS [count] FROM %s WHERE [id] >= @minID END", procedureFQTN, tableFQTN),
	}
	for _, statement := range seedStatements {
		if _, err := sourceDB.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed source: %v", err)
		}
	}

	c := &copier{
		cfg: config{
			SourceDSN:      sourceDSN,
			TargetDSN:      targetDSN,
			Workers:        2,
			BatchSize:      1000,
			Verbose:        false,
			DropExisting:   true,
			IncludeSchemas: []string{dataSchema, seqSchema, procSchema},
		},
		sourceDB: sourceDB,
		targetDB: targetDB,
	}

	if err := c.run(ctx); err != nil {
		t.Fatalf("run copier: %v", err)
	}

	if _, err := targetDB.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s ([name]) VALUES (N'gamma');", tableFQTN)); err != nil {
		t.Fatalf("insert target row using copied sequence default: %v", err)
	}

	var nextID int
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("SELECT [id] FROM %s WHERE [name] = N'gamma'", tableFQTN)).Scan(&nextID); err != nil {
		t.Fatalf("read inserted target row: %v", err)
	}
	if nextID <= 105 || nextID%5 != 0 {
		t.Fatalf("expected copied sequence default to generate a value above 105 in steps of 5, got %d", nextID)
	}

	var procCount int
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("EXEC %s @minID = @p1", procedureFQTN), 105).Scan(&procCount); err != nil {
		t.Fatalf("execute copied procedure: %v", err)
	}
	if procCount != 2 {
		t.Fatalf("expected copied procedure to count 2 rows, got %d", procCount)
	}
}

func TestCopierIntegrationTableTypes(t *testing.T) {
	if os.Getenv("COPY_MSSQL_RUN_INTEGRATION") == "" {
		t.Skip("set COPY_MSSQL_RUN_INTEGRATION=1 to run the integration test")
	}
	ctx := context.Background()

	sourceContainer, sourceDSN := startSQLServerContainer(ctx, t, "copy-mssql-source-table-types")
	defer terminateContainer(ctx, t, sourceContainer)
	targetContainer, targetDSN := startSQLServerContainer(ctx, t, "copy-mssql-target-table-types")
	defer terminateContainer(ctx, t, targetContainer)

	sourceDB, err := openTestDB(ctx, sourceDSN, 4)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer sourceDB.Close()

	targetDB, err := openTestDB(ctx, targetDSN, 4)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer targetDB.Close()

	schemaName := fmt.Sprintf("tt_it_%d", time.Now().UnixNano())
	typeFQTN := quoteIdent(schemaName) + ".[order_line_type]"
	procedureFQTN := quoteIdent(schemaName) + ".[sum_order_lines]"

	cleanup := fmt.Sprintf(
		"IF OBJECT_ID(N'%s', 'P') IS NOT NULL DROP PROCEDURE %s; "+
			"IF TYPE_ID(N'%s') IS NOT NULL DROP TYPE %s; "+
			"IF SCHEMA_ID(N'%s') IS NOT NULL EXEC(N'DROP SCHEMA %s');",
		escapeSQLString(procedureFQTN), procedureFQTN,
		escapeSQLString(typeFQTN), typeFQTN,
		escapeSQLString(schemaName), quoteIdent(schemaName),
	)
	defer sourceDB.ExecContext(ctx, cleanup)
	defer targetDB.ExecContext(ctx, cleanup)

	seedStatements := []string{
		fmt.Sprintf("CREATE SCHEMA %s;", quoteIdent(schemaName)),
		fmt.Sprintf("CREATE TYPE %s AS TABLE ([order_id] int NOT NULL PRIMARY KEY NONCLUSTERED, [qty] int NOT NULL);", typeFQTN),
		fmt.Sprintf("CREATE PROCEDURE %s @lines %s READONLY AS BEGIN SELECT SUM([qty]) AS [total_qty] FROM @lines END", procedureFQTN, typeFQTN),
	}
	for _, statement := range seedStatements {
		if _, err := sourceDB.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed source: %v", err)
		}
	}

	c := &copier{
		cfg: config{
			SourceDSN:      sourceDSN,
			TargetDSN:      targetDSN,
			Workers:        1,
			BatchSize:      1000,
			Verbose:        false,
			DropExisting:   true,
			IncludeSchemas: []string{schemaName},
		},
		sourceDB: sourceDB,
		targetDB: targetDB,
	}

	if err := c.run(ctx); err != nil {
		t.Fatalf("run copier: %v", err)
	}

	var typeCount int
	if err := targetDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sys.table_types tt JOIN sys.schemas s ON s.schema_id = tt.schema_id WHERE s.name = @p1 AND tt.name = @p2", schemaName, "order_line_type").Scan(&typeCount); err != nil {
		t.Fatalf("check copied table type: %v", err)
	}
	if typeCount != 1 {
		t.Fatalf("expected copied table type to exist, got count %d", typeCount)
	}

	procSQL := fmt.Sprintf("DECLARE @lines %s; INSERT INTO @lines ([order_id], [qty]) VALUES (1, 2), (2, 5); EXEC %s @lines = @lines;", typeFQTN, procedureFQTN)
	var totalQty int
	if err := targetDB.QueryRowContext(ctx, procSQL).Scan(&totalQty); err != nil {
		t.Fatalf("execute copied TVP procedure: %v", err)
	}
	if totalQty != 7 {
		t.Fatalf("expected copied TVP procedure to return 7, got %d", totalQty)
	}

	if _, err := sourceDB.ExecContext(ctx, fmt.Sprintf("ALTER PROCEDURE %s @lines %s READONLY AS BEGIN SELECT SUM([qty]) + 1 AS [total_qty] FROM @lines END", procedureFQTN, typeFQTN)); err != nil {
		t.Fatalf("alter source TVP procedure: %v", err)
	}

	if err := c.run(ctx); err != nil {
		t.Fatalf("rerun copier: %v", err)
	}

	if err := targetDB.QueryRowContext(ctx, procSQL).Scan(&totalQty); err != nil {
		t.Fatalf("execute altered copied TVP procedure: %v", err)
	}
	if totalQty != 8 {
		t.Fatalf("expected altered copied TVP procedure to return 8, got %d", totalQty)
	}
}

func TestCopierIntegrationProcedureDependsOnSynonym(t *testing.T) {
	if os.Getenv("COPY_MSSQL_RUN_INTEGRATION") == "" {
		t.Skip("set COPY_MSSQL_RUN_INTEGRATION=1 to run the integration test")
	}
	ctx := context.Background()

	sourceContainer, sourceDSN := startSQLServerContainer(ctx, t, "copy-mssql-source-proc-synonym")
	defer terminateContainer(ctx, t, sourceContainer)
	targetContainer, targetDSN := startSQLServerContainer(ctx, t, "copy-mssql-target-proc-synonym")
	defer terminateContainer(ctx, t, targetContainer)

	sourceDB, err := openTestDB(ctx, sourceDSN, 4)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer sourceDB.Close()

	targetDB, err := openTestDB(ctx, targetDSN, 4)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer targetDB.Close()

	schemaName := fmt.Sprintf("proc_syn_it_%d", time.Now().UnixNano())
	ordersFQTN := quoteIdent(schemaName) + ".[orders]"
	synonymFQTN := quoteIdent(schemaName) + ".[orders_alias]"
	procedureFQTN := quoteIdent(schemaName) + ".[count_orders]"

	cleanup := fmt.Sprintf(
		"IF OBJECT_ID(N'%s', 'P') IS NOT NULL DROP PROCEDURE %s; "+
			"IF OBJECT_ID(N'%s', 'SN') IS NOT NULL DROP SYNONYM %s; "+
			"IF OBJECT_ID(N'%s', 'U') IS NOT NULL DROP TABLE %s; "+
			"IF SCHEMA_ID(N'%s') IS NOT NULL EXEC(N'DROP SCHEMA %s');",
		escapeSQLString(procedureFQTN), procedureFQTN,
		escapeSQLString(synonymFQTN), synonymFQTN,
		escapeSQLString(ordersFQTN), ordersFQTN,
		escapeSQLString(schemaName), quoteIdent(schemaName),
	)
	defer sourceDB.ExecContext(ctx, cleanup)
	defer targetDB.ExecContext(ctx, cleanup)

	seedStatements := []string{
		fmt.Sprintf("CREATE SCHEMA %s;", quoteIdent(schemaName)),
		fmt.Sprintf("CREATE TABLE %s ([id] int NOT NULL, [name] nvarchar(50) NOT NULL, CONSTRAINT [PK_orders] PRIMARY KEY CLUSTERED ([id] ASC));", ordersFQTN),
		fmt.Sprintf("INSERT INTO %s ([id], [name]) VALUES (1, N'alpha'), (2, N'beta');", ordersFQTN),
		fmt.Sprintf("CREATE SYNONYM %s FOR %s;", synonymFQTN, ordersFQTN),
		fmt.Sprintf("CREATE PROCEDURE %s AS BEGIN SELECT COUNT(*) AS [count] FROM %s END", procedureFQTN, synonymFQTN),
	}
	for _, statement := range seedStatements {
		if _, err := sourceDB.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed source: %v", err)
		}
	}

	c := &copier{
		cfg: config{
			SourceDSN:      sourceDSN,
			TargetDSN:      targetDSN,
			Workers:        2,
			BatchSize:      1000,
			Verbose:        false,
			DropExisting:   true,
			IncludeSchemas: []string{schemaName},
		},
		sourceDB: sourceDB,
		targetDB: targetDB,
	}

	if err := c.run(ctx); err != nil {
		t.Fatalf("run copier first pass: %v", err)
	}

	var firstCount int
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("EXEC %s", procedureFQTN)).Scan(&firstCount); err != nil {
		t.Fatalf("execute copied synonym-backed procedure: %v", err)
	}
	if firstCount != 2 {
		t.Fatalf("expected copied synonym-backed procedure to return 2, got %d", firstCount)
	}

	if _, err := sourceDB.ExecContext(ctx, fmt.Sprintf("ALTER PROCEDURE %s AS BEGIN SELECT COUNT(*) + 1 AS [count] FROM %s END", procedureFQTN, synonymFQTN)); err != nil {
		t.Fatalf("alter source synonym-backed procedure: %v", err)
	}

	if err := c.run(ctx); err != nil {
		t.Fatalf("run copier second pass: %v", err)
	}

	var secondCount int
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("EXEC %s", procedureFQTN)).Scan(&secondCount); err != nil {
		t.Fatalf("execute altered synonym-backed procedure: %v", err)
	}
	if secondCount != 3 {
		t.Fatalf("expected altered synonym-backed procedure to return 3, got %d", secondCount)
	}
}

func TestCopierIntegrationTriggers(t *testing.T) {
	if os.Getenv("COPY_MSSQL_RUN_INTEGRATION") == "" {
		t.Skip("set COPY_MSSQL_RUN_INTEGRATION=1 to run the integration test")
	}
	ctx := context.Background()

	sourceContainer, sourceDSN := startSQLServerContainer(ctx, t, "copy-mssql-source-triggers")
	defer terminateContainer(ctx, t, sourceContainer)
	targetContainer, targetDSN := startSQLServerContainer(ctx, t, "copy-mssql-target-triggers")
	defer terminateContainer(ctx, t, targetContainer)

	sourceDB, err := openTestDB(ctx, sourceDSN, 4)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer sourceDB.Close()

	targetDB, err := openTestDB(ctx, targetDSN, 4)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer targetDB.Close()

	schemaName := fmt.Sprintf("trg_it_%d", time.Now().UnixNano())
	ordersFQTN := quoteIdent(schemaName) + ".[orders]"
	auditFQTN := quoteIdent(schemaName) + ".[order_audit]"
	triggerFQTN := quoteIdent(schemaName) + ".[trg_orders_audit]"

	cleanup := fmt.Sprintf(
		"IF OBJECT_ID(N'%s', 'TR') IS NOT NULL DROP TRIGGER %s; "+
			"IF OBJECT_ID(N'%s', 'U') IS NOT NULL DROP TABLE %s; "+
			"IF OBJECT_ID(N'%s', 'U') IS NOT NULL DROP TABLE %s; "+
			"IF SCHEMA_ID(N'%s') IS NOT NULL EXEC(N'DROP SCHEMA %s');",
		escapeSQLString(triggerFQTN), triggerFQTN,
		escapeSQLString(auditFQTN), auditFQTN,
		escapeSQLString(ordersFQTN), ordersFQTN,
		escapeSQLString(schemaName), quoteIdent(schemaName),
	)
	defer sourceDB.ExecContext(ctx, cleanup)
	defer targetDB.ExecContext(ctx, cleanup)

	seedStatements := []string{
		fmt.Sprintf("CREATE SCHEMA %s;", quoteIdent(schemaName)),
		fmt.Sprintf("CREATE TABLE %s ([id] int NOT NULL, [name] nvarchar(50) NOT NULL, CONSTRAINT [PK_orders] PRIMARY KEY CLUSTERED ([id] ASC));", ordersFQTN),
		fmt.Sprintf("CREATE TABLE %s ([order_id] int NOT NULL, [note] nvarchar(50) NOT NULL);", auditFQTN),
		fmt.Sprintf("INSERT INTO %s ([id], [name]) VALUES (1, N'alpha');", ordersFQTN),
		fmt.Sprintf(`CREATE TRIGGER %s ON %s
AFTER INSERT
AS
BEGIN
    INSERT INTO %s ([order_id], [note])
    SELECT [id], N'inserted' FROM inserted;
END`, triggerFQTN, ordersFQTN, auditFQTN),
	}
	for _, statement := range seedStatements {
		if _, err := sourceDB.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed source: %v", err)
		}
	}

	c := &copier{
		cfg: config{
			SourceDSN:      sourceDSN,
			TargetDSN:      targetDSN,
			Workers:        2,
			BatchSize:      1000,
			Verbose:        false,
			DropExisting:   true,
			IncludeSchemas: []string{schemaName},
		},
		sourceDB: sourceDB,
		targetDB: targetDB,
	}

	if err := c.run(ctx); err != nil {
		t.Fatalf("run copier first pass: %v", err)
	}

	if _, err := targetDB.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s ([id], [name]) VALUES (2, N'beta');", ordersFQTN)); err != nil {
		t.Fatalf("insert target row through copied trigger: %v", err)
	}

	var firstNote string
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("SELECT TOP 1 [note] FROM %s WHERE [order_id] = 2", auditFQTN)).Scan(&firstNote); err != nil {
		t.Fatalf("query audit row after first trigger run: %v", err)
	}
	if firstNote != "inserted" {
		t.Fatalf("expected copied trigger note inserted, got %q", firstNote)
	}

	if _, err := sourceDB.ExecContext(ctx, fmt.Sprintf(`ALTER TRIGGER %s ON %s
AFTER INSERT
AS
BEGIN
    INSERT INTO %s ([order_id], [note])
    SELECT [id], N'inserted-v2' FROM inserted;
END`, triggerFQTN, ordersFQTN, auditFQTN)); err != nil {
		t.Fatalf("alter source trigger: %v", err)
	}

	if err := c.run(ctx); err != nil {
		t.Fatalf("run copier second pass: %v", err)
	}

	if _, err := targetDB.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s ([id], [name]) VALUES (3, N'gamma');", ordersFQTN)); err != nil {
		t.Fatalf("insert target row after trigger rerun: %v", err)
	}

	var secondNote string
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("SELECT TOP 1 [note] FROM %s WHERE [order_id] = 3", auditFQTN)).Scan(&secondNote); err != nil {
		t.Fatalf("query audit row after second trigger run: %v", err)
	}
	if secondNote != "inserted-v2" {
		t.Fatalf("expected copied trigger note inserted-v2, got %q", secondNote)
	}
}

func TestCopierIntegrationTriggerDependsOnSynonym(t *testing.T) {
	if os.Getenv("COPY_MSSQL_RUN_INTEGRATION") == "" {
		t.Skip("set COPY_MSSQL_RUN_INTEGRATION=1 to run the integration test")
	}
	ctx := context.Background()

	sourceContainer, sourceDSN := startSQLServerContainer(ctx, t, "copy-mssql-source-trigger-synonym")
	defer terminateContainer(ctx, t, sourceContainer)
	targetContainer, targetDSN := startSQLServerContainer(ctx, t, "copy-mssql-target-trigger-synonym")
	defer terminateContainer(ctx, t, targetContainer)

	sourceDB, err := openTestDB(ctx, sourceDSN, 4)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer sourceDB.Close()

	targetDB, err := openTestDB(ctx, targetDSN, 4)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer targetDB.Close()

	schemaName := fmt.Sprintf("trg_syn_it_%d", time.Now().UnixNano())
	ordersFQTN := quoteIdent(schemaName) + ".[orders]"
	auditFQTN := quoteIdent(schemaName) + ".[order_audit]"
	synonymFQTN := quoteIdent(schemaName) + ".[order_audit_alias]"
	triggerFQTN := quoteIdent(schemaName) + ".[trg_orders_audit_alias]"

	cleanup := fmt.Sprintf(
		"IF OBJECT_ID(N'%s', 'TR') IS NOT NULL DROP TRIGGER %s; "+
			"IF OBJECT_ID(N'%s', 'SN') IS NOT NULL DROP SYNONYM %s; "+
			"IF OBJECT_ID(N'%s', 'U') IS NOT NULL DROP TABLE %s; "+
			"IF OBJECT_ID(N'%s', 'U') IS NOT NULL DROP TABLE %s; "+
			"IF SCHEMA_ID(N'%s') IS NOT NULL EXEC(N'DROP SCHEMA %s');",
		escapeSQLString(triggerFQTN), triggerFQTN,
		escapeSQLString(synonymFQTN), synonymFQTN,
		escapeSQLString(auditFQTN), auditFQTN,
		escapeSQLString(ordersFQTN), ordersFQTN,
		escapeSQLString(schemaName), quoteIdent(schemaName),
	)
	defer sourceDB.ExecContext(ctx, cleanup)
	defer targetDB.ExecContext(ctx, cleanup)

	seedStatements := []string{
		fmt.Sprintf("CREATE SCHEMA %s;", quoteIdent(schemaName)),
		fmt.Sprintf("CREATE TABLE %s ([id] int NOT NULL, [name] nvarchar(50) NOT NULL, CONSTRAINT [PK_orders] PRIMARY KEY CLUSTERED ([id] ASC));", ordersFQTN),
		fmt.Sprintf("CREATE TABLE %s ([order_id] int NOT NULL, [note] nvarchar(50) NOT NULL);", auditFQTN),
		fmt.Sprintf("CREATE SYNONYM %s FOR %s;", synonymFQTN, auditFQTN),
		fmt.Sprintf(`CREATE TRIGGER %s ON %s
AFTER INSERT
AS
BEGIN
    INSERT INTO %s ([order_id], [note])
    SELECT [id], N'via-synonym' FROM inserted;
END`, triggerFQTN, ordersFQTN, synonymFQTN),
	}
	for _, statement := range seedStatements {
		if _, err := sourceDB.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed source: %v", err)
		}
	}

	c := &copier{
		cfg: config{
			SourceDSN:      sourceDSN,
			TargetDSN:      targetDSN,
			Workers:        2,
			BatchSize:      1000,
			Verbose:        false,
			DropExisting:   true,
			IncludeSchemas: []string{schemaName},
		},
		sourceDB: sourceDB,
		targetDB: targetDB,
	}

	if err := c.run(ctx); err != nil {
		t.Fatalf("run copier first pass: %v", err)
	}

	if _, err := targetDB.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s ([id], [name]) VALUES (1, N'alpha');", ordersFQTN)); err != nil {
		t.Fatalf("insert target row through synonym-backed trigger: %v", err)
	}

	var firstNote string
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("SELECT TOP 1 [note] FROM %s WHERE [order_id] = 1", auditFQTN)).Scan(&firstNote); err != nil {
		t.Fatalf("query audit row after first synonym-backed trigger run: %v", err)
	}
	if firstNote != "via-synonym" {
		t.Fatalf("expected copied trigger note via-synonym, got %q", firstNote)
	}

	if _, err := sourceDB.ExecContext(ctx, fmt.Sprintf(`ALTER TRIGGER %s ON %s
AFTER INSERT
AS
BEGIN
    INSERT INTO %s ([order_id], [note])
    SELECT [id], N'via-synonym-v2' FROM inserted;
END`, triggerFQTN, ordersFQTN, synonymFQTN)); err != nil {
		t.Fatalf("alter source synonym-backed trigger: %v", err)
	}

	if err := c.run(ctx); err != nil {
		t.Fatalf("run copier second pass: %v", err)
	}

	if _, err := targetDB.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s ([id], [name]) VALUES (2, N'beta');", ordersFQTN)); err != nil {
		t.Fatalf("insert target row after synonym-backed trigger rerun: %v", err)
	}

	var secondNote string
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("SELECT TOP 1 [note] FROM %s WHERE [order_id] = 2", auditFQTN)).Scan(&secondNote); err != nil {
		t.Fatalf("query audit row after second synonym-backed trigger run: %v", err)
	}
	if secondNote != "via-synonym-v2" {
		t.Fatalf("expected copied trigger note via-synonym-v2, got %q", secondNote)
	}
}

func TestCopierIntegrationFunctions(t *testing.T) {
	if os.Getenv("COPY_MSSQL_RUN_INTEGRATION") == "" {
		t.Skip("set COPY_MSSQL_RUN_INTEGRATION=1 to run the integration test")
	}
	ctx := context.Background()

	sourceContainer, sourceDSN := startSQLServerContainer(ctx, t, "copy-mssql-source-functions")
	defer terminateContainer(ctx, t, sourceContainer)
	targetContainer, targetDSN := startSQLServerContainer(ctx, t, "copy-mssql-target-functions")
	defer terminateContainer(ctx, t, targetContainer)

	sourceDB, err := openTestDB(ctx, sourceDSN, 4)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer sourceDB.Close()

	targetDB, err := openTestDB(ctx, targetDSN, 4)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer targetDB.Close()

	schemaName := fmt.Sprintf("fn_it_%d", time.Now().UnixNano())
	baseFunctionFQTN := quoteIdent(schemaName) + ".[base_count]"
	childFunctionFQTN := quoteIdent(schemaName) + ".[double_count]"

	cleanup := fmt.Sprintf(
		"IF OBJECT_ID(N'%s', 'FN') IS NOT NULL DROP FUNCTION %s; "+
			"IF OBJECT_ID(N'%s', 'FN') IS NOT NULL DROP FUNCTION %s; "+
			"IF SCHEMA_ID(N'%s') IS NOT NULL EXEC(N'DROP SCHEMA %s');",
		escapeSQLString(childFunctionFQTN), childFunctionFQTN,
		escapeSQLString(baseFunctionFQTN), baseFunctionFQTN,
		escapeSQLString(schemaName), quoteIdent(schemaName),
	)
	defer sourceDB.ExecContext(ctx, cleanup)
	defer targetDB.ExecContext(ctx, cleanup)

	seedStatements := []string{
		fmt.Sprintf("CREATE SCHEMA %s;", quoteIdent(schemaName)),
		fmt.Sprintf("CREATE FUNCTION %s (@value int) RETURNS int AS BEGIN RETURN @value + 1 END", baseFunctionFQTN),
		fmt.Sprintf("CREATE FUNCTION %s (@value int) RETURNS int AS BEGIN RETURN ABS(%s(@value)) * 2 END", childFunctionFQTN, baseFunctionFQTN),
	}
	for _, statement := range seedStatements {
		if _, err := sourceDB.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed source: %v", err)
		}
	}

	c := &copier{
		cfg: config{
			SourceDSN:      sourceDSN,
			TargetDSN:      targetDSN,
			Workers:        1,
			BatchSize:      1000,
			Verbose:        false,
			DropExisting:   true,
			IncludeSchemas: []string{schemaName},
		},
		sourceDB: sourceDB,
		targetDB: targetDB,
	}

	if err := c.run(ctx); err != nil {
		t.Fatalf("run copier first pass: %v", err)
	}

	var firstValue int
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("SELECT %s(@p1)", childFunctionFQTN), 3).Scan(&firstValue); err != nil {
		t.Fatalf("execute copied function: %v", err)
	}
	if firstValue != 8 {
		t.Fatalf("expected copied function result 8, got %d", firstValue)
	}

	if _, err := sourceDB.ExecContext(ctx, fmt.Sprintf("ALTER FUNCTION %s (@value int) RETURNS int AS BEGIN RETURN %s(@value) * 3 END", childFunctionFQTN, baseFunctionFQTN)); err != nil {
		t.Fatalf("alter source function: %v", err)
	}

	if err := c.run(ctx); err != nil {
		t.Fatalf("run copier second pass: %v", err)
	}

	var secondValue int
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("SELECT %s(@p1)", childFunctionFQTN), 3).Scan(&secondValue); err != nil {
		t.Fatalf("execute altered copied function: %v", err)
	}
	if secondValue != 12 {
		t.Fatalf("expected altered copied function result 12, got %d", secondValue)
	}
}

func TestCopierIntegrationSynonyms(t *testing.T) {
	if os.Getenv("COPY_MSSQL_RUN_INTEGRATION") == "" {
		t.Skip("set COPY_MSSQL_RUN_INTEGRATION=1 to run the integration test")
	}
	ctx := context.Background()

	sourceContainer, sourceDSN := startSQLServerContainer(ctx, t, "copy-mssql-source-synonyms")
	defer terminateContainer(ctx, t, sourceContainer)
	targetContainer, targetDSN := startSQLServerContainer(ctx, t, "copy-mssql-target-synonyms")
	defer terminateContainer(ctx, t, targetContainer)

	sourceDB, err := openTestDB(ctx, sourceDSN, 4)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer sourceDB.Close()

	targetDB, err := openTestDB(ctx, targetDSN, 4)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer targetDB.Close()

	schemaName := fmt.Sprintf("syn_it_%d", time.Now().UnixNano())
	tableOneFQTN := quoteIdent(schemaName) + ".[orders_a]"
	tableTwoFQTN := quoteIdent(schemaName) + ".[orders_b]"
	synonymFQTN := quoteIdent(schemaName) + ".[orders_current]"

	cleanup := fmt.Sprintf(
		"IF OBJECT_ID(N'%s', 'SN') IS NOT NULL DROP SYNONYM %s; "+
			"IF OBJECT_ID(N'%s', 'U') IS NOT NULL DROP TABLE %s; "+
			"IF OBJECT_ID(N'%s', 'U') IS NOT NULL DROP TABLE %s; "+
			"IF SCHEMA_ID(N'%s') IS NOT NULL EXEC(N'DROP SCHEMA %s');",
		escapeSQLString(synonymFQTN), synonymFQTN,
		escapeSQLString(tableTwoFQTN), tableTwoFQTN,
		escapeSQLString(tableOneFQTN), tableOneFQTN,
		escapeSQLString(schemaName), quoteIdent(schemaName),
	)
	defer sourceDB.ExecContext(ctx, cleanup)
	defer targetDB.ExecContext(ctx, cleanup)

	seedStatements := []string{
		fmt.Sprintf("CREATE SCHEMA %s;", quoteIdent(schemaName)),
		fmt.Sprintf("CREATE TABLE %s ([id] int NOT NULL PRIMARY KEY, [name] nvarchar(50) NOT NULL);", tableOneFQTN),
		fmt.Sprintf("CREATE TABLE %s ([id] int NOT NULL PRIMARY KEY, [name] nvarchar(50) NOT NULL);", tableTwoFQTN),
		fmt.Sprintf("INSERT INTO %s ([id], [name]) VALUES (1, N'alpha');", tableOneFQTN),
		fmt.Sprintf("INSERT INTO %s ([id], [name]) VALUES (2, N'beta');", tableTwoFQTN),
		fmt.Sprintf("CREATE SYNONYM %s FOR %s;", synonymFQTN, tableOneFQTN),
	}
	for _, statement := range seedStatements {
		if _, err := sourceDB.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed source: %v", err)
		}
	}

	c := &copier{
		cfg: config{
			SourceDSN:      sourceDSN,
			TargetDSN:      targetDSN,
			Workers:        2,
			BatchSize:      1000,
			Verbose:        false,
			DropExisting:   true,
			IncludeSchemas: []string{schemaName},
		},
		sourceDB: sourceDB,
		targetDB: targetDB,
	}

	if err := c.run(ctx); err != nil {
		t.Fatalf("run copier first pass: %v", err)
	}

	var firstName string
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("SELECT TOP 1 [name] FROM %s", synonymFQTN)).Scan(&firstName); err != nil {
		t.Fatalf("query copied synonym first pass: %v", err)
	}
	if firstName != "alpha" {
		t.Fatalf("expected copied synonym to point at orders_a, got %q", firstName)
	}

	if _, err := sourceDB.ExecContext(ctx, fmt.Sprintf("DROP SYNONYM %s; CREATE SYNONYM %s FOR %s;", synonymFQTN, synonymFQTN, tableTwoFQTN)); err != nil {
		t.Fatalf("repoint source synonym: %v", err)
	}

	if err := c.run(ctx); err != nil {
		t.Fatalf("run copier second pass: %v", err)
	}

	var secondName string
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("SELECT TOP 1 [name] FROM %s", synonymFQTN)).Scan(&secondName); err != nil {
		t.Fatalf("query copied synonym second pass: %v", err)
	}
	if secondName != "beta" {
		t.Fatalf("expected copied synonym to point at orders_b after rerun, got %q", secondName)
	}
}

func TestCopierIntegrationMixedObjects(t *testing.T) {
	if os.Getenv("COPY_MSSQL_RUN_INTEGRATION") == "" {
		t.Skip("set COPY_MSSQL_RUN_INTEGRATION=1 to run the integration test")
	}
	ctx := context.Background()

	sourceContainer, sourceDSN := startSQLServerContainer(ctx, t, "copy-mssql-source-mixed")
	defer terminateContainer(ctx, t, sourceContainer)
	targetContainer, targetDSN := startSQLServerContainer(ctx, t, "copy-mssql-target-mixed")
	defer terminateContainer(ctx, t, targetContainer)

	sourceDB, err := openTestDB(ctx, sourceDSN, 4)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer sourceDB.Close()

	targetDB, err := openTestDB(ctx, targetDSN, 4)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer targetDB.Close()

	schemaName := fmt.Sprintf("mixed_it_%d", time.Now().UnixNano())
	typeFQTN := quoteIdent(schemaName) + ".[order_code]"
	sequenceFQTN := quoteIdent(schemaName) + ".[order_seq]"
	tableFQTN := quoteIdent(schemaName) + ".[orders]"
	viewFQTN := quoteIdent(schemaName) + ".[active_orders]"
	functionFQTN := quoteIdent(schemaName) + ".[active_count]"
	procedureFQTN := quoteIdent(schemaName) + ".[count_active]"
	synonymFQTN := quoteIdent(schemaName) + ".[orders_alias]"

	cleanup := fmt.Sprintf(
		"IF OBJECT_ID(N'%s', 'SN') IS NOT NULL DROP SYNONYM %s; "+
			"IF OBJECT_ID(N'%s', 'P') IS NOT NULL DROP PROCEDURE %s; "+
			"IF OBJECT_ID(N'%s', 'FN') IS NOT NULL DROP FUNCTION %s; "+
			"IF OBJECT_ID(N'%s', 'V') IS NOT NULL DROP VIEW %s; "+
			"IF OBJECT_ID(N'%s', 'U') IS NOT NULL DROP TABLE %s; "+
			"IF OBJECT_ID(N'%s', 'SO') IS NOT NULL DROP SEQUENCE %s; "+
			"IF TYPE_ID(N'%s') IS NOT NULL DROP TYPE %s; "+
			"IF SCHEMA_ID(N'%s') IS NOT NULL EXEC(N'DROP SCHEMA %s');",
		escapeSQLString(synonymFQTN), synonymFQTN,
		escapeSQLString(procedureFQTN), procedureFQTN,
		escapeSQLString(functionFQTN), functionFQTN,
		escapeSQLString(viewFQTN), viewFQTN,
		escapeSQLString(tableFQTN), tableFQTN,
		escapeSQLString(sequenceFQTN), sequenceFQTN,
		escapeSQLString(typeFQTN), typeFQTN,
		escapeSQLString(schemaName), quoteIdent(schemaName),
	)
	defer sourceDB.ExecContext(ctx, cleanup)
	defer targetDB.ExecContext(ctx, cleanup)

	seedStatements := []string{
		fmt.Sprintf("CREATE SCHEMA %s;", quoteIdent(schemaName)),
		fmt.Sprintf("CREATE TYPE %s FROM nvarchar(12) NULL;", typeFQTN),
		fmt.Sprintf("CREATE SEQUENCE %s AS int START WITH 100 INCREMENT BY 5 MINVALUE 100 MAXVALUE 1000 NO CYCLE NO CACHE;", sequenceFQTN),
		fmt.Sprintf("CREATE TABLE %s ([id] int NOT NULL CONSTRAINT [DF_orders_id] DEFAULT NEXT VALUE FOR %s, [code] %s NOT NULL, [active] bit NOT NULL, CONSTRAINT [PK_orders] PRIMARY KEY CLUSTERED ([id] ASC));", tableFQTN, sequenceFQTN, typeFQTN),
		fmt.Sprintf("INSERT INTO %s ([code], [active]) VALUES (N'A-001', 1), (N'B-002', 0);", tableFQTN),
		fmt.Sprintf("CREATE VIEW %s AS SELECT [id], [code] FROM %s WHERE [active] = 1;", viewFQTN, tableFQTN),
		fmt.Sprintf("CREATE FUNCTION %s () RETURNS int AS BEGIN RETURN (SELECT COUNT(*) FROM %s) END", functionFQTN, viewFQTN),
		fmt.Sprintf("CREATE PROCEDURE %s AS BEGIN SELECT %s() AS [count] END", procedureFQTN, functionFQTN),
		fmt.Sprintf("CREATE SYNONYM %s FOR %s;", synonymFQTN, tableFQTN),
	}
	for _, statement := range seedStatements {
		if _, err := sourceDB.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed source: %v", err)
		}
	}

	c := &copier{
		cfg: config{
			SourceDSN:      sourceDSN,
			TargetDSN:      targetDSN,
			Workers:        2,
			BatchSize:      1000,
			Verbose:        false,
			DropExisting:   true,
			IncludeSchemas: []string{schemaName},
		},
		sourceDB: sourceDB,
		targetDB: targetDB,
	}

	if err := c.run(ctx); err != nil {
		t.Fatalf("run copier: %v", err)
	}

	if _, err := targetDB.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s ([code], [active]) VALUES (N'C-003', 1);", synonymFQTN)); err != nil {
		t.Fatalf("insert through copied synonym: %v", err)
	}

	var value string
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("SELECT TOP 1 [code] FROM %s ORDER BY [id] DESC", viewFQTN)).Scan(&value); err != nil {
		t.Fatalf("query copied view: %v", err)
	}
	if value != "C-003" {
		t.Fatalf("expected copied view to return inserted code C-003, got %q", value)
	}

	var count int
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("SELECT %s()", functionFQTN)).Scan(&count); err != nil {
		t.Fatalf("execute copied function: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected copied function to count 2 active rows, got %d", count)
	}

	var procCount int
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("EXEC %s", procedureFQTN)).Scan(&procCount); err != nil {
		t.Fatalf("execute copied procedure: %v", err)
	}
	if procCount != 2 {
		t.Fatalf("expected copied procedure to return 2 active rows, got %d", procCount)
	}

	var typeCount int
	if err := targetDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sys.types t JOIN sys.schemas s ON s.schema_id = t.schema_id WHERE s.name = @p1 AND t.name = @p2", schemaName, "order_code").Scan(&typeCount); err != nil {
		t.Fatalf("check copied alias type: %v", err)
	}
	if typeCount != 1 {
		t.Fatalf("expected copied alias type to exist, got count %d", typeCount)
	}

	var sequenceRowID int
	if err := targetDB.QueryRowContext(ctx, fmt.Sprintf("SELECT [id] FROM %s WHERE [code] = N'C-003'", tableFQTN)).Scan(&sequenceRowID); err != nil {
		t.Fatalf("read inserted row id: %v", err)
	}
	if sequenceRowID <= 105 || sequenceRowID%5 != 0 {
		t.Fatalf("expected copied sequence to generate a value above 105 in steps of 5, got %d", sequenceRowID)
	}
}

func startSQLServerContainer(ctx context.Context, t *testing.T, name string) (testcontainers.Container, string) {
	t.Helper()
	password := "Your_strong_Passw0rd!"
	image := sqlTestImage()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Name:         name,
			Image:        image,
			ExposedPorts: []string{"1433/tcp"},
			Env: map[string]string{
				"ACCEPT_EULA":       "Y",
				"MSSQL_SA_PASSWORD": password,
				"MSSQL_PID":         "Developer",
			},
			WaitingFor: wait.ForLog("SQL Server is now ready for client connections").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start SQL Server container with image %s: %v", image, err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("resolve SQL Server host: %v", err)
	}
	port, err := container.MappedPort(ctx, "1433/tcp")
	if err != nil {
		t.Fatalf("resolve SQL Server port: %v", err)
	}

	dsn := fmt.Sprintf("sqlserver://sa:%s@%s:%s?database=master&encrypt=disable", password, host, port.Port())
	return container, dsn
}

func sqlTestImage() string {
	if runtime.GOARCH == "arm64" {
		return "mcr.microsoft.com/azure-sql-edge:latest"
	}
	return "mcr.microsoft.com/mssql/server:2022-latest"
}

func openTestDB(ctx context.Context, dsn string, maxConns int) (*sql.DB, error) {
	deadline := time.Now().Add(2 * time.Minute)
	var lastErr error
	for time.Now().Before(deadline) {
		db, err := openDB(dsn, maxConns)
		if err == nil {
			return db, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, fmt.Errorf("open test db: %w", lastErr)
}

func terminateContainer(ctx context.Context, t *testing.T, container testcontainers.Container) {
	t.Helper()
	if err := container.Terminate(ctx); err != nil {
		t.Fatalf("terminate container: %v", err)
	}
}
