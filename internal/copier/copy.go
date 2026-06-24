package copier

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mssql "github.com/denisenkom/go-mssqldb"
)

func (c *copier) createSchemas(ctx context.Context) error {
	schemas := map[string]struct{}{}
	for _, table := range c.tables {
		schemas[table.Schema] = struct{}{}
	}
	for _, aliasType := range c.aliasTypes {
		schemas[aliasType.Schema] = struct{}{}
	}
	for _, tableType := range c.tableTypes {
		schemas[tableType.Schema] = struct{}{}
	}
	for _, sequence := range c.sequences {
		schemas[sequence.Schema] = struct{}{}
	}
	for _, view := range c.views {
		schemas[view.Schema] = struct{}{}
	}
	for _, function := range c.functions {
		schemas[function.Schema] = struct{}{}
	}
	for _, procedure := range c.procedures {
		schemas[procedure.Schema] = struct{}{}
	}
	for _, trigger := range c.triggers {
		schemas[trigger.Schema] = struct{}{}
	}
	for _, synonym := range c.synonyms {
		schemas[synonym.Schema] = struct{}{}
	}
	names := make([]string, 0, len(schemas))
	for schema := range schemas {
		names = append(names, schema)
	}
	sort.Strings(names)

	for _, schema := range names {
		if schema == "dbo" {
			continue
		}
		sqlText := fmt.Sprintf(`IF SCHEMA_ID(N'%s') IS NULL EXEC(N'CREATE SCHEMA %s')`, escapeSQLString(schema), quoteIdent(schema))
		if _, err := c.targetDB.ExecContext(ctx, sqlText); err != nil {
			return fmt.Errorf("create schema %s: %w", schema, err)
		}
	}
	return nil
}

func (c *copier) createAliasTypes(ctx context.Context) error {
	for _, aliasType := range c.aliasTypes {
		if c.cfg.DropExisting {
			dropSQL := fmt.Sprintf("IF TYPE_ID(N'%s') IS NOT NULL DROP TYPE %s;", escapeSQLString(aliasType.FQTN()), aliasType.FQTN())
			if _, err := c.targetDB.ExecContext(ctx, dropSQL); err != nil {
				return fmt.Errorf("drop type %s: %w", aliasType.FQTN(), err)
			}
		}

		sqlText, err := aliasType.CreateTypeSQL()
		if err != nil {
			return err
		}
		if c.cfg.Verbose {
			log.Printf("creating alias type %s", aliasType.FQTN())
		}
		if _, err := c.targetDB.ExecContext(ctx, sqlText); err != nil {
			return fmt.Errorf("create type %s: %w", aliasType.FQTN(), err)
		}
	}
	return nil
}

func (c *copier) createTableTypes(ctx context.Context) error {
	for _, tableType := range c.tableTypes {
		sqlText, err := tableType.CreateTypeSQL()
		if err != nil {
			return err
		}
		if c.cfg.Verbose {
			log.Printf("creating table type %s", tableType.FQTN())
		}
		if _, err := c.targetDB.ExecContext(ctx, sqlText); err != nil {
			return fmt.Errorf("create table type %s: %w", tableType.FQTN(), err)
		}
	}
	return nil
}

func (c *copier) createSequences(ctx context.Context) error {
	for _, sequence := range c.sequences {
		sqlText, err := sequence.CreateSequenceSQL()
		if err != nil {
			return err
		}
		if c.cfg.Verbose {
			log.Printf("creating sequence %s", sequence.FQTN())
		}
		if _, err := c.targetDB.ExecContext(ctx, sqlText); err != nil {
			return fmt.Errorf("create sequence %s: %w", sequence.FQTN(), err)
		}
	}
	return nil
}

func (c *copier) prepareTargetTables(ctx context.Context) error {
	if !c.cfg.DropExisting {
		return nil
	}
	if err := c.dropTargetForeignKeys(ctx); err != nil {
		return err
	}
	for _, table := range c.tables {
		if c.cfg.Verbose {
			log.Printf("dropping existing target table %s if present", table.FQTN())
		}
		dropSQL := fmt.Sprintf("IF OBJECT_ID(N'%s', 'U') IS NOT NULL DROP TABLE %s;", escapeSQLString(table.FQTN()), table.FQTN())
		if _, err := c.targetDB.ExecContext(ctx, dropSQL); err != nil {
			return fmt.Errorf("drop existing %s: %w", table.FQTN(), err)
		}
	}
	return nil
}

func (c *copier) createTables(ctx context.Context) error {
	for _, table := range c.tables {
		createSQL, err := table.CreateTableSQL()
		if err != nil {
			return err
		}
		if c.cfg.Verbose {
			log.Printf("creating %s", table.FQTN())
		}
		if _, err := c.targetDB.ExecContext(ctx, createSQL); err != nil {
			return fmt.Errorf("create %s: %w", table.FQTN(), err)
		}
		for _, col := range table.Columns {
			if col.SkipReason != "" && c.cfg.Verbose {
				log.Printf("%s: skipping source writes for column %s (%s)", table.FQTN(), col.Name, col.SkipReason)
			}
		}
	}
	return nil
}

func (c *copier) dropTargetForeignKeys(ctx context.Context) error {
	seen := map[string]struct{}{}
	for _, table := range c.tables {
		fks, err := c.listTargetForeignKeys(ctx, table)
		if err != nil {
			return err
		}
		for _, fk := range fks {
			if _, ok := seen[strings.ToLower(fk.parentFQTN+"."+fk.name)]; ok {
				continue
			}
			seen[strings.ToLower(fk.parentFQTN+"."+fk.name)] = struct{}{}
			dropSQL := fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s;", fk.parentFQTN, quoteIdent(fk.name))
			if _, err := c.targetDB.ExecContext(ctx, dropSQL); err != nil {
				return fmt.Errorf("drop foreign key %s on %s: %w", fk.name, fk.parentFQTN, err)
			}
		}
	}
	return nil
}

type targetForeignKey struct {
	name       string
	parentFQTN string
}

func (c *copier) listTargetForeignKeys(ctx context.Context, table tableMeta) ([]targetForeignKey, error) {
	const sqlText = `
SELECT fk.name, ps.name, pt.name
FROM sys.foreign_keys fk
JOIN sys.tables pt ON pt.object_id = fk.parent_object_id
JOIN sys.schemas ps ON ps.schema_id = pt.schema_id
WHERE fk.parent_object_id = OBJECT_ID(@p1)
   OR fk.referenced_object_id = OBJECT_ID(@p1);`

	rows, err := c.targetDB.QueryContext(ctx, sqlText, table.FQTN())
	if err != nil {
		return nil, fmt.Errorf("list target foreign keys for %s: %w", table.FQTN(), err)
	}
	defer closeAndLog(rows, "target foreign key rows")

	var result []targetForeignKey
	for rows.Next() {
		var fk targetForeignKey
		var schemaName string
		var tableName string
		if err := rows.Scan(&fk.name, &schemaName, &tableName); err != nil {
			return nil, err
		}
		fk.parentFQTN = quoteIdent(schemaName) + "." + quoteIdent(tableName)
		result = append(result, fk)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *copier) copyTableData(ctx context.Context) error {
	var completed atomic.Int32
	jobs := make(chan tableMeta)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	workerCount := c.cfg.Workers
	if workerCount > len(c.tables) && len(c.tables) > 0 {
		workerCount = len(c.tables)
	}
	if workerCount == 0 {
		return nil
	}

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Go(func() {
			for table := range jobs {
				if ctx.Err() != nil {
					return
				}
				rowsCopied, err := c.copySingleTable(ctx, table)
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					cancel()
					return
				}
				n := completed.Add(1)
				log.Printf("copied %s rows=%d (%d/%d)", table.FQTN(), rowsCopied, n, len(c.tables))
			}
		})
	}

dispatch:
	for _, table := range c.tables {
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- table:
		}
	}
	close(jobs)
	wg.Wait()

	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func (c *copier) copySingleTable(ctx context.Context, table tableMeta) (int64, error) {
	started := time.Now()
	if len(table.CopyColumns) == 0 {
		c.report.record(table, 0, time.Since(started))
		return 0, nil
	}
	if c.cfg.Verbose {
		mode := "bulk"
		if !table.BulkOK {
			mode = "row-insert"
		}
		log.Printf("starting %s using %s", table.FQTN(), mode)
	}

	sourceConn, err := c.sourceDB.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("source conn %s: %w", table.FQTN(), err)
	}
	defer closeAndLog(sourceConn, "source connection")

	targetConn, err := c.targetDB.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("target conn %s: %w", table.FQTN(), err)
	}
	defer closeAndLog(targetConn, "target connection")

	query := selectTableCopySQL(table)
	rows, err := sourceConn.QueryContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("query %s: %w", table.FQTN(), err)
	}
	defer closeAndLog(rows, "source rows")

	tx, err := targetConn.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin target tx %s: %w", table.FQTN(), err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if table.HasIdentity {
		if _, err := targetConn.ExecContext(ctx, fmt.Sprintf("SET IDENTITY_INSERT %s ON", table.FQTN())); err != nil {
			return 0, fmt.Errorf("identity insert on %s: %w", table.FQTN(), err)
		}
		defer func() {
			_, _ = targetConn.ExecContext(context.Background(), fmt.Sprintf("SET IDENTITY_INSERT %s OFF", table.FQTN()))
		}()
	}

	var stmt *sql.Stmt
	if table.BulkOK {
		stmt, err = tx.PrepareContext(ctx, mssql.CopyIn(table.FQTN(), mssql.BulkOptions{Tablock: true, RowsPerBatch: c.cfg.BatchSize}, columnNames(table.CopyColumns)...))
		if err != nil {
			return 0, fmt.Errorf("prepare bulk %s: %w", table.FQTN(), err)
		}
	} else {
		insertSQL := insertTableCopySQL(table)
		stmt, err = tx.PrepareContext(ctx, insertSQL)
		if err != nil {
			return 0, fmt.Errorf("prepare insert %s: %w", table.FQTN(), err)
		}
	}
	defer closeAndLog(stmt, "copy statement")

	values := make([]any, len(table.CopyColumns))
	scanDest := make([]any, len(table.CopyColumns))
	for i := range values {
		scanDest[i] = &values[i]
	}

	var rowsCopied int64
	for rows.Next() {
		if err := rows.Scan(scanDest...); err != nil {
			return 0, fmt.Errorf("scan %s: %w", table.FQTN(), err)
		}
		params := make([]any, len(table.CopyColumns))
		for i, col := range table.CopyColumns {
			params[i], err = c.replaceValue(table, col, values[i])
			if err != nil {
				return 0, err
			}
		}
		if _, err := stmt.ExecContext(ctx, params...); err != nil {
			return 0, fmt.Errorf("insert row %s: %w", table.FQTN(), err)
		}
		rowsCopied++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate %s: %w", table.FQTN(), err)
	}

	if table.BulkOK {
		if _, err := stmt.ExecContext(ctx); err != nil {
			enrichedErr := enrichBCPError(table, err)
			log.Printf("BCP flush failed for %s, falling back to row-by-row inserts: %v", table.FQTN(), enrichedErr)
			return c.fallbackCopyRowByRow(ctx, table, started)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit %s: %w", table.FQTN(), err)
	}
	c.report.record(table, rowsCopied, time.Since(started))
	return rowsCopied, nil
}

func (c *copier) fallbackCopyRowByRow(ctx context.Context, table tableMeta, started time.Time) (int64, error) {
	sourceConn, err := c.sourceDB.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("fallback source conn %s: %w", table.FQTN(), err)
	}
	defer closeAndLog(sourceConn, "fallback source connection")

	targetConn, err := c.targetDB.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("fallback target conn %s: %w", table.FQTN(), err)
	}
	defer closeAndLog(targetConn, "fallback target connection")

	query := selectTableCopySQL(table)
	rows, err := sourceConn.QueryContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("fallback query %s: %w", table.FQTN(), err)
	}
	defer closeAndLog(rows, "fallback source rows")

	tx, err := targetConn.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("fallback begin tx %s: %w", table.FQTN(), err)
	}
	defer func() { _ = tx.Rollback() }()

	if table.HasIdentity {
		if _, err := targetConn.ExecContext(ctx, fmt.Sprintf("SET IDENTITY_INSERT %s ON", table.FQTN())); err != nil {
			return 0, fmt.Errorf("fallback identity insert on %s: %w", table.FQTN(), err)
		}
		defer func() {
			_, _ = targetConn.ExecContext(context.Background(), fmt.Sprintf("SET IDENTITY_INSERT %s OFF", table.FQTN()))
		}()
	}

	insertSQL := insertTableCopySQL(table)
	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return 0, fmt.Errorf("fallback prepare insert %s: %w", table.FQTN(), err)
	}
	defer closeAndLog(stmt, "fallback insert statement")

	values := make([]any, len(table.CopyColumns))
	scanDest := make([]any, len(table.CopyColumns))
	for i := range values {
		scanDest[i] = &values[i]
	}

	var rowsCopied int64
	for rows.Next() {
		if err := rows.Scan(scanDest...); err != nil {
			return 0, fmt.Errorf("fallback scan %s: %w", table.FQTN(), err)
		}
		params := make([]any, len(table.CopyColumns))
		for i, col := range table.CopyColumns {
			params[i], err = c.replaceValue(table, col, values[i])
			if err != nil {
				return 0, err
			}
		}
		if _, err := stmt.ExecContext(ctx, params...); err != nil {
			for i, col := range table.CopyColumns {
				log.Printf("fallback %s row %d col[%d] %s (%s) = %#v", table.FQTN(), rowsCopied+1, i, col.Name, col.SystemTypeName, truncateForLog(params[i]))
			}
			return 0, fmt.Errorf("fallback insert row %d in %s: %w", rowsCopied+1, table.FQTN(), err)
		}
		rowsCopied++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("fallback iterate %s: %w", table.FQTN(), err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("fallback commit %s: %w", table.FQTN(), err)
	}
	c.report.record(table, rowsCopied, time.Since(started))
	log.Printf("fallback row-by-row copy succeeded for %s rows=%d", table.FQTN(), rowsCopied)
	return rowsCopied, nil
}

func truncateForLog(v any) any {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case []byte:
		if len(val) > 200 {
			return fmt.Sprintf("%s...(%d bytes total)", string(val[:200]), len(val))
		}
		return string(val)
	case string:
		if len(val) > 200 {
			return val[:200] + fmt.Sprintf("...(%d chars total)", len(val))
		}
		return val
	default:
		return fmt.Sprintf("%v", v)
	}
}

func (c *copier) createPostDataObjects(ctx context.Context) error {
	for _, table := range c.tables {
		if table.PrimaryKey != nil {
			if _, err := c.targetDB.ExecContext(ctx, table.PrimaryKeySQL()); err != nil {
				return fmt.Errorf("create primary key %s: %w", table.FQTN(), err)
			}
		}
		for _, check := range table.Checks {
			if _, err := c.targetDB.ExecContext(ctx, table.CheckSQL(check)); err != nil {
				return fmt.Errorf("create check %s.%s: %w", table.FQTN(), check.Name, err)
			}
			if check.Disabled {
				if _, err := c.targetDB.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s NOCHECK CONSTRAINT %s", table.FQTN(), quoteIdent(check.Name))); err != nil {
					return fmt.Errorf("disable check %s.%s: %w", table.FQTN(), check.Name, err)
				}
			}
		}
		for _, index := range table.Indexes {
			if _, err := c.targetDB.ExecContext(ctx, table.IndexSQL(index)); err != nil {
				if isDuplicateKeyUniqueIndexError(index, err) {
					log.Printf("WARNING: skipping unique index %s.%s because target data contains duplicate key values: %v", table.FQTN(), index.Name, err)
					c.report.recordSkippedIndex(table, index, err)
					continue
				}
				return fmt.Errorf("create index %s.%s: %w", table.FQTN(), index.Name, err)
			}
		}
	}

	for _, table := range c.tables {
		for _, fk := range table.ForeignKeys {
			exists, err := c.targetTableExists(ctx, fk.RefSchema, fk.RefTable)
			if err != nil {
				return fmt.Errorf("check foreign key target %s.%s -> [%s].[%s]: %w", table.FQTN(), fk.Name, fk.RefSchema, fk.RefTable, err)
			}
			if !exists {
				log.Printf("skipping foreign key %s.%s because referenced table [%s].[%s] does not exist on target", table.FQTN(), fk.Name, fk.RefSchema, fk.RefTable)
				continue
			}

			nocreateSQL := table.ForeignKeySQLNOCHECK(fk)
			if _, err := c.targetDB.ExecContext(ctx, nocreateSQL); err != nil {
				return fmt.Errorf("create foreign key %s.%s: %w", table.FQTN(), fk.Name, err)
			}
			if fk.Disabled {
				// Already NOCHECK, nothing more to do.
			} else if fk.Trusted {
				enableSQL := fmt.Sprintf("ALTER TABLE %s WITH CHECK CHECK CONSTRAINT %s;", table.FQTN(), quoteIdent(fk.Name))
				if _, err := c.targetDB.ExecContext(ctx, enableSQL); err != nil {
					log.Printf("WARNING: foreign key %s.%s could not be trusted (data may be inconsistent): %v", table.FQTN(), fk.Name, err)
				}
			}
		}
	}
	return nil
}

func isDuplicateKeyUniqueIndexError(index indexMeta, err error) bool {
	if !index.Unique {
		return false
	}
	var sqlErr mssql.Error
	if !errors.As(err, &sqlErr) {
		return false
	}
	if sqlErr.Number == 1505 {
		return true
	}
	for _, nested := range sqlErr.All {
		if nested.Number == 1505 {
			return true
		}
	}
	return false
}

func (c *copier) targetTableExists(ctx context.Context, schemaName string, tableName string) (bool, error) {
	const sqlText = `
SELECT COUNT(*)
FROM sys.tables t
JOIN sys.schemas s ON s.schema_id = t.schema_id
WHERE s.name = @p1 AND t.name = @p2;`

	var count int
	if err := c.targetDB.QueryRowContext(ctx, sqlText, schemaName, tableName).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (c *copier) createViews(ctx context.Context) error {
	if len(c.views) == 0 {
		return nil
	}

	ordered, err := topologicalSortViews(c.views)
	if err != nil {
		return fmt.Errorf("view dependency resolution: %w", err)
	}

	for _, v := range ordered {
		sqlText := v.CreateViewSQL()
		if c.cfg.Verbose {
			log.Printf("creating view %s", v.FQTN())
		}
		if _, err := c.targetDB.ExecContext(ctx, sqlText); err != nil {
			return fmt.Errorf("create view %s: %w", v.FQTN(), err)
		}
	}
	return nil
}

func (c *copier) createProcedures(ctx context.Context) error {
	ordered, err := topologicalSortProcedures(c.procedures)
	if err != nil {
		return fmt.Errorf("procedure dependency resolution: %w", err)
	}

	for _, procedure := range ordered {
		sqlText := procedure.CreateProcedureSQL()
		if c.cfg.Verbose {
			log.Printf("creating procedure %s", procedure.FQTN())
		}
		if _, err := c.targetDB.ExecContext(ctx, sqlText); err != nil {
			return fmt.Errorf("create procedure %s: %w", procedure.FQTN(), err)
		}
	}
	return nil
}

func (c *copier) createTriggers(ctx context.Context) error {
	ordered, err := topologicalSortTriggers(c.triggers)
	if err != nil {
		return fmt.Errorf("trigger dependency resolution: %w", err)
	}

	for _, trigger := range ordered {
		sqlText := trigger.CreateTriggerSQL()
		if c.cfg.Verbose {
			log.Printf("creating trigger %s on %s", trigger.FQTN(), trigger.TableFQTN())
		}
		if _, err := c.targetDB.ExecContext(ctx, sqlText); err != nil {
			return fmt.Errorf("create trigger %s: %w", trigger.FQTN(), err)
		}

		stateSQL := fmt.Sprintf("ENABLE TRIGGER %s ON %s;", trigger.FQTN(), trigger.TableFQTN())
		if trigger.Disabled {
			stateSQL = fmt.Sprintf("DISABLE TRIGGER %s ON %s;", trigger.FQTN(), trigger.TableFQTN())
		}
		if _, err := c.targetDB.ExecContext(ctx, stateSQL); err != nil {
			return fmt.Errorf("set trigger state %s: %w", trigger.FQTN(), err)
		}
	}
	return nil
}

func (c *copier) createFunctions(ctx context.Context) error {
	if len(c.functions) == 0 {
		return nil
	}

	ordered, err := topologicalSortFunctions(c.functions)
	if err != nil {
		return fmt.Errorf("function dependency resolution: %w", err)
	}

	for _, function := range ordered {
		sqlText := function.CreateFunctionSQL()
		if c.cfg.Verbose {
			log.Printf("creating function %s", function.FQTN())
		}
		if _, err := c.targetDB.ExecContext(ctx, sqlText); err != nil {
			return fmt.Errorf("create function %s: %w", function.FQTN(), err)
		}
	}
	return nil
}

func (c *copier) createSynonyms(ctx context.Context) error {
	for _, synonym := range c.synonyms {
		sqlText := synonym.CreateSynonymSQL()
		if c.cfg.Verbose {
			log.Printf("creating synonym %s", synonym.FQTN())
		}
		if _, err := c.targetDB.ExecContext(ctx, sqlText); err != nil {
			return fmt.Errorf("create synonym %s: %w", synonym.FQTN(), err)
		}
	}
	return nil
}

// topologicalSortViews returns views in dependency order using Kahn's algorithm.
func topologicalSortViews(views []viewMeta) ([]viewMeta, error) {
	fqtnToIdx := make(map[string]int, len(views))
	for i, v := range views {
		fqtnToIdx[strings.ToLower(v.FQTN())] = i
	}

	inDegree := make([]int, len(views))
	adj := make([][]int, len(views))
	for i, v := range views {
		for _, dep := range v.DependsOn {
			if j, ok := fqtnToIdx[dep]; ok {
				adj[j] = append(adj[j], i)
				inDegree[i]++
			}
		}
	}

	var queue []int
	for i, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, i)
		}
	}

	var sorted []viewMeta
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		sorted = append(sorted, views[cur])
		for _, next := range adj[cur] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if len(sorted) != len(views) {
		return nil, fmt.Errorf("circular dependency detected among views")
	}
	return sorted, nil
}

func topologicalSortFunctions(functions []functionMeta) ([]functionMeta, error) {
	fqtnToIdx := make(map[string]int, len(functions))
	for i, fn := range functions {
		fqtnToIdx[strings.ToLower(fn.FQTN())] = i
	}

	inDegree := make([]int, len(functions))
	adj := make([][]int, len(functions))
	for i, fn := range functions {
		for _, dep := range fn.DependsOn {
			if j, ok := fqtnToIdx[dep]; ok {
				adj[j] = append(adj[j], i)
				inDegree[i]++
			}
		}
	}

	var queue []int
	for i, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, i)
		}
	}

	var sorted []functionMeta
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		sorted = append(sorted, functions[cur])
		for _, next := range adj[cur] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if len(sorted) != len(functions) {
		return nil, fmt.Errorf("circular dependency detected among functions")
	}
	return sorted, nil
}

func topologicalSortProcedures(procedures []procedureMeta) ([]procedureMeta, error) {
	fqtnToIdx := make(map[string]int, len(procedures))
	for i, procedure := range procedures {
		fqtnToIdx[strings.ToLower(procedure.FQTN())] = i
	}

	inDegree := make([]int, len(procedures))
	adj := make([][]int, len(procedures))
	for i, procedure := range procedures {
		for _, dep := range procedure.DependsOn {
			if j, ok := fqtnToIdx[dep]; ok {
				adj[j] = append(adj[j], i)
				inDegree[i]++
			}
		}
	}

	var queue []int
	for i, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, i)
		}
	}

	var sorted []procedureMeta
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		sorted = append(sorted, procedures[cur])
		for _, next := range adj[cur] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if len(sorted) != len(procedures) {
		return nil, fmt.Errorf("circular dependency detected among procedures")
	}
	return sorted, nil
}

func topologicalSortTriggers(triggers []triggerMeta) ([]triggerMeta, error) {
	fqtnToIdx := make(map[string]int, len(triggers))
	for i, trigger := range triggers {
		fqtnToIdx[strings.ToLower(trigger.FQTN())] = i
	}

	inDegree := make([]int, len(triggers))
	adj := make([][]int, len(triggers))
	for i, trigger := range triggers {
		for _, dep := range trigger.DependsOn {
			if j, ok := fqtnToIdx[dep]; ok {
				adj[j] = append(adj[j], i)
				inDegree[i]++
			}
		}
	}

	var queue []int
	for i, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, i)
		}
	}

	var sorted []triggerMeta
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		sorted = append(sorted, triggers[cur])
		for _, next := range adj[cur] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if len(sorted) != len(triggers) {
		return nil, fmt.Errorf("circular dependency detected among triggers")
	}
	return sorted, nil
}
