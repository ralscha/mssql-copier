package copier

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type flywayStatement struct {
	sql string
}

func (c *copier) writeFlywayBaselineFile() error {
	script, err := c.buildFlywayBaselineSQL()
	if err != nil {
		return err
	}

	dir := filepath.Dir(c.cfg.ExportDDLFile)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create flyway output directory: %w", err)
		}
	}

	if err := os.WriteFile(c.cfg.ExportDDLFile, []byte(script), 0o600); err != nil {
		return fmt.Errorf("write flyway baseline file: %w", err)
	}
	return nil
}

func (c *copier) buildFlywayBaselineSQL() (string, error) {
	statements, err := c.flywayStatements()
	if err != nil {
		return "", err
	}

	var builder strings.Builder
	for index, statement := range statements {
		if index > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(strings.TrimSpace(statement.sql))
		builder.WriteString("\n")
	}
	return builder.String(), nil
}

func (c *copier) flywayStatements() ([]flywayStatement, error) {
	var statements []flywayStatement

	for _, schema := range c.exportSchemaNames() {
		statements = append(statements, flywayStatement{
			sql: fmt.Sprintf(`IF SCHEMA_ID(N'%s') IS NULL EXEC(N'CREATE SCHEMA %s');`, escapeSQLString(schema), quoteIdent(schema)),
		})
	}

	for _, aliasType := range c.aliasTypes {
		sqlText, err := aliasType.CreateTypeSQL()
		if err != nil {
			return nil, err
		}
		statements = append(statements, flywayStatement{
			sql: sqlText,
		})
	}

	for _, tableType := range c.tableTypes {
		sqlText, err := tableType.CreateTypeSQL()
		if err != nil {
			return nil, err
		}
		statements = append(statements, flywayStatement{
			sql: sqlText,
		})
	}

	for _, sequence := range c.sequences {
		sqlText, err := sequence.CreateSequenceSQL()
		if err != nil {
			return nil, err
		}
		statements = append(statements, flywayStatement{
			sql: sqlText,
		})
	}

	for _, table := range c.tables {
		sqlText, err := table.CreateTableSQL()
		if err != nil {
			return nil, err
		}
		statements = append(statements, flywayStatement{
			sql: sqlText,
		})
	}

	selectedTables := selectedTableSet(c.tables)
	for _, table := range c.tables {
		if table.PrimaryKey != nil {
			statements = append(statements, flywayStatement{
				sql: table.PrimaryKeySQL(),
			})
		}
		for _, check := range table.Checks {
			sqlText := table.CheckSQL(check)
			if check.Disabled {
				sqlText += "\n" + fmt.Sprintf("ALTER TABLE %s NOCHECK CONSTRAINT %s;", table.FQTN(), quoteIdent(check.Name))
			}
			statements = append(statements, flywayStatement{
				sql: sqlText,
			})
		}
		for _, index := range table.Indexes {
			statements = append(statements, flywayStatement{
				sql: table.IndexSQL(index),
			})
		}
		for _, fk := range table.ForeignKeys {
			if _, ok := selectedTables[strings.ToLower(quoteIdent(fk.RefSchema)+"."+quoteIdent(fk.RefTable))]; !ok {
				continue
			}
			sqlText := table.ForeignKeySQL(fk)
			if fk.Disabled {
				sqlText += "\n" + fmt.Sprintf("ALTER TABLE %s NOCHECK CONSTRAINT %s;", table.FQTN(), quoteIdent(fk.Name))
			}
			statements = append(statements, flywayStatement{
				sql: sqlText,
			})
		}
	}

	orderedViews, err := topologicalSortViews(c.views)
	if err != nil {
		return nil, fmt.Errorf("view dependency resolution: %w", err)
	}
	for _, view := range orderedViews {
		statements = append(statements, flywayStatement{
			sql: view.CreateViewSQL(),
		})
	}

	orderedFunctions, err := topologicalSortFunctions(c.functions)
	if err != nil {
		return nil, fmt.Errorf("function dependency resolution: %w", err)
	}
	for _, function := range orderedFunctions {
		statements = append(statements, flywayStatement{
			sql: function.CreateFunctionSQL(),
		})
	}

	for _, synonym := range c.synonyms {
		statements = append(statements, flywayStatement{
			sql: synonym.CreateSynonymSQL(),
		})
	}

	orderedProcedures, err := topologicalSortProcedures(c.procedures)
	if err != nil {
		return nil, fmt.Errorf("procedure dependency resolution: %w", err)
	}
	for _, procedure := range orderedProcedures {
		statements = append(statements, flywayStatement{
			sql: procedure.CreateProcedureSQL(),
		})
	}

	orderedTriggers, err := topologicalSortTriggers(c.triggers)
	if err != nil {
		return nil, fmt.Errorf("trigger dependency resolution: %w", err)
	}
	for _, trigger := range orderedTriggers {
		sqlText := trigger.CreateTriggerSQL()
		if trigger.Disabled {
			sqlText += "\n" + fmt.Sprintf("DISABLE TRIGGER %s ON %s;", trigger.FQTN(), trigger.TableFQTN())
		} else {
			sqlText += "\n" + fmt.Sprintf("ENABLE TRIGGER %s ON %s;", trigger.FQTN(), trigger.TableFQTN())
		}
		statements = append(statements, flywayStatement{
			sql: sqlText,
		})
	}

	return statements, nil
}

func (c *copier) exportSchemaNames() []string {
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
		if schema == "dbo" {
			continue
		}
		names = append(names, schema)
	}
	sort.Strings(names)
	return names
}

func selectedTableSet(tables []tableMeta) map[string]struct{} {
	selected := make(map[string]struct{}, len(tables))
	for _, table := range tables {
		selected[strings.ToLower(table.FQTN())] = struct{}{}
	}
	return selected
}
