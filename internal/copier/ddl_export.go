package copier

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const flywayAuthor = "mssql-copier"

type flywayChange struct {
	id  string
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
	changes, err := c.flywayChanges()
	if err != nil {
		return "", err
	}

	var builder strings.Builder
	for _, change := range changes {
		builder.WriteString("\n")
		builder.WriteString("--changeset ")
		builder.WriteString(flywayAuthor)
		builder.WriteString(":")
		builder.WriteString(change.id)
		builder.WriteString(" splitStatements:false\n")
		builder.WriteString(strings.TrimSpace(change.sql))
		builder.WriteString("\n")
	}
	return builder.String(), nil
}

func (c *copier) flywayChanges() ([]flywayChange, error) {
	var changes []flywayChange

	for _, schema := range c.exportSchemaNames() {
		changes = append(changes, flywayChange{
			id:  "schema-" + flywayIDPart(schema),
			sql: fmt.Sprintf(`IF SCHEMA_ID(N'%s') IS NULL EXEC(N'CREATE SCHEMA %s')`, escapeSQLString(schema), quoteIdent(schema)),
		})
	}

	for _, aliasType := range c.aliasTypes {
		sqlText, err := aliasType.CreateTypeSQL()
		if err != nil {
			return nil, err
		}
		changes = append(changes, flywayChange{
			id:  "alias-type-" + flywayIDPart(aliasType.Schema) + "-" + flywayIDPart(aliasType.Name),
			sql: sqlText,
		})
	}

	for _, tableType := range c.tableTypes {
		sqlText, err := tableType.CreateTypeSQL()
		if err != nil {
			return nil, err
		}
		changes = append(changes, flywayChange{
			id:  "table-type-" + flywayIDPart(tableType.Schema) + "-" + flywayIDPart(tableType.Name),
			sql: sqlText,
		})
	}

	for _, sequence := range c.sequences {
		sqlText, err := sequence.CreateSequenceSQL()
		if err != nil {
			return nil, err
		}
		changes = append(changes, flywayChange{
			id:  "sequence-" + flywayIDPart(sequence.Schema) + "-" + flywayIDPart(sequence.Name),
			sql: sqlText,
		})
	}

	for _, table := range c.tables {
		sqlText, err := table.CreateTableSQL()
		if err != nil {
			return nil, err
		}
		changes = append(changes, flywayChange{
			id:  "table-" + flywayIDPart(table.Schema) + "-" + flywayIDPart(table.Name),
			sql: sqlText,
		})
	}

	selectedTables := selectedTableSet(c.tables)
	for _, table := range c.tables {
		if table.PrimaryKey != nil {
			changes = append(changes, flywayChange{
				id:  "primary-key-" + flywayIDPart(table.Schema) + "-" + flywayIDPart(table.Name),
				sql: table.PrimaryKeySQL(),
			})
		}
		for _, check := range table.Checks {
			sqlText := table.CheckSQL(check)
			if check.Disabled {
				sqlText += "\n" + fmt.Sprintf("ALTER TABLE %s NOCHECK CONSTRAINT %s;", table.FQTN(), quoteIdent(check.Name))
			}
			changes = append(changes, flywayChange{
				id:  "check-" + flywayIDPart(table.Schema) + "-" + flywayIDPart(table.Name) + "-" + flywayIDPart(check.Name),
				sql: sqlText,
			})
		}
		for _, index := range table.Indexes {
			changes = append(changes, flywayChange{
				id:  "index-" + flywayIDPart(table.Schema) + "-" + flywayIDPart(table.Name) + "-" + flywayIDPart(index.Name),
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
			changes = append(changes, flywayChange{
				id:  "foreign-key-" + flywayIDPart(table.Schema) + "-" + flywayIDPart(table.Name) + "-" + flywayIDPart(fk.Name),
				sql: sqlText,
			})
		}
	}

	orderedViews, err := topologicalSortViews(c.views)
	if err != nil {
		return nil, fmt.Errorf("view dependency resolution: %w", err)
	}
	for _, view := range orderedViews {
		changes = append(changes, flywayChange{
			id:  "view-" + flywayIDPart(view.Schema) + "-" + flywayIDPart(view.Name),
			sql: view.CreateViewSQL(),
		})
	}

	orderedFunctions, err := topologicalSortFunctions(c.functions)
	if err != nil {
		return nil, fmt.Errorf("function dependency resolution: %w", err)
	}
	for _, function := range orderedFunctions {
		changes = append(changes, flywayChange{
			id:  "function-" + flywayIDPart(function.Schema) + "-" + flywayIDPart(function.Name),
			sql: function.CreateFunctionSQL(),
		})
	}

	for _, synonym := range c.synonyms {
		changes = append(changes, flywayChange{
			id:  "synonym-" + flywayIDPart(synonym.Schema) + "-" + flywayIDPart(synonym.Name),
			sql: synonym.CreateSynonymSQL(),
		})
	}

	orderedProcedures, err := topologicalSortProcedures(c.procedures)
	if err != nil {
		return nil, fmt.Errorf("procedure dependency resolution: %w", err)
	}
	for _, procedure := range orderedProcedures {
		changes = append(changes, flywayChange{
			id:  "procedure-" + flywayIDPart(procedure.Schema) + "-" + flywayIDPart(procedure.Name),
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
		changes = append(changes, flywayChange{
			id:  "trigger-" + flywayIDPart(trigger.Schema) + "-" + flywayIDPart(trigger.Name),
			sql: sqlText,
		})
	}

	return changes, nil
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

func flywayIDPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(" ", "-", ".", "-", "[", "", "]", "", "/", "-", `\\`, "-", ":", "-", "'", "", `"`, "")
	value = replacer.Replace(value)
	value = strings.Trim(value, "-")
	if value == "" {
		return "item"
	}
	return value
}
