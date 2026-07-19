package copier

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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
	builder.WriteString("-- mssql-copier Flyway SQL Server baseline\n")
	builder.WriteString("-- Use a Flyway baseline or versioned migration filename, for example B001__initial_schema.sql.\n")
	for _, change := range changes {
		builder.WriteString("\n")
		builder.WriteString("-- object: ")
		builder.WriteString(change.id)
		builder.WriteString("\n")
		builder.WriteString(strings.TrimSpace(change.sql))
		builder.WriteString("\nGO\n")
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

	orderedSchemaObjects, err := c.schemaCreationOrder()
	if err != nil {
		return nil, fmt.Errorf("schema dependency resolution: %w", err)
	}
	for _, object := range orderedSchemaObjects {
		change, err := c.flywayChangeForSchemaObject(object)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
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

	orderedTriggers, err := topologicalSortTriggers(c.triggers)
	if err != nil {
		return nil, fmt.Errorf("trigger dependency resolution: %w", err)
	}
	for _, trigger := range orderedTriggers {
		sqlText := trigger.CreateTriggerSQL()
		if trigger.Disabled {
			sqlText += "\nGO\n" + fmt.Sprintf("DISABLE TRIGGER %s ON %s;", trigger.FQTN(), trigger.TableFQTN())
		} else {
			sqlText += "\nGO\n" + fmt.Sprintf("ENABLE TRIGGER %s ON %s;", trigger.FQTN(), trigger.TableFQTN())
		}
		changes = append(changes, flywayChange{
			id:  "trigger-" + flywayIDPart(trigger.Schema) + "-" + flywayIDPart(trigger.Name),
			sql: sqlText,
		})
	}

	return changes, nil
}

func (c *copier) flywayChangeForSchemaObject(object schemaObjectRef) (flywayChange, error) {
	switch object.kind {
	case "table":
		item := c.tables[object.index]
		sqlText, err := item.CreateTableSQL()
		if err != nil {
			return flywayChange{}, err
		}
		return flywayChange{id: "table-" + flywayIDPart(item.Schema) + "-" + flywayIDPart(item.Name), sql: sqlText}, nil
	case "view":
		item := c.views[object.index]
		return flywayChange{id: "view-" + flywayIDPart(item.Schema) + "-" + flywayIDPart(item.Name), sql: item.CreateViewSQL()}, nil
	case "function":
		item := c.functions[object.index]
		return flywayChange{id: "function-" + flywayIDPart(item.Schema) + "-" + flywayIDPart(item.Name), sql: item.CreateFunctionSQL()}, nil
	case "synonym":
		item := c.synonyms[object.index]
		return flywayChange{id: "synonym-" + flywayIDPart(item.Schema) + "-" + flywayIDPart(item.Name), sql: item.CreateSynonymSQL()}, nil
	case "procedure":
		item := c.procedures[object.index]
		return flywayChange{id: "procedure-" + flywayIDPart(item.Schema) + "-" + flywayIDPart(item.Name), sql: item.CreateProcedureSQL()}, nil
	default:
		return flywayChange{}, fmt.Errorf("unsupported schema object kind %q", object.kind)
	}
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
