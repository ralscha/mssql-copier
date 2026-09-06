package copier

import (
	"fmt"
	"os"
	"strings"
)

func (c *copier) printPlan() {
	_, _ = fmt.Fprintln(os.Stdout, "Plan summary")
	_, _ = fmt.Fprintf(os.Stdout, "  source: %s\n", stripDSNPassword(c.cfg.SourceDSN))
	if c.cfg.TargetDSN != "" {
		_, _ = fmt.Fprintf(os.Stdout, "  target: %s\n", stripDSNPassword(c.cfg.TargetDSN))
	}
	_, _ = fmt.Fprintf(os.Stdout, "  tables selected: %d\n", len(c.tables))
	_, _ = fmt.Fprintf(os.Stdout, "  tables selected for data: %d\n", len(c.dataTables()))
	_, _ = fmt.Fprintf(os.Stdout, "  alias types selected: %d\n", len(c.aliasTypes))
	_, _ = fmt.Fprintf(os.Stdout, "  table types selected: %d\n", len(c.tableTypes))
	_, _ = fmt.Fprintf(os.Stdout, "  sequences selected: %d\n", len(c.sequences))
	if c.cfg.DropExisting {
		_, _ = fmt.Fprintln(os.Stdout, "  target preparation: drop existing selected tables")
	} else {
		_, _ = fmt.Fprintln(os.Stdout, "  target preparation: create-only")
	}
	_, _ = fmt.Fprintf(os.Stdout, "  views selected: %d\n", len(c.views))
	_, _ = fmt.Fprintf(os.Stdout, "  functions selected: %d\n", len(c.functions))
	_, _ = fmt.Fprintf(os.Stdout, "  procedures selected: %d\n", len(c.procedures))
	_, _ = fmt.Fprintf(os.Stdout, "  triggers selected: %d\n", len(c.triggers))
	_, _ = fmt.Fprintf(os.Stdout, "  synonyms selected: %d\n", len(c.synonyms))

	if len(c.tables) == 0 && len(c.aliasTypes) == 0 && len(c.tableTypes) == 0 && len(c.sequences) == 0 && len(c.views) == 0 && len(c.functions) == 0 && len(c.procedures) == 0 && len(c.triggers) == 0 && len(c.synonyms) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "No tables, alias types, table types, sequences, views, functions, procedures, triggers, or synonyms matched the current filters.")
		return
	}

	if len(c.aliasTypes) > 0 {
		_, _ = fmt.Fprintln(os.Stdout)
		_, _ = fmt.Fprintln(os.Stdout, "Alias Types")
		for _, aliasType := range c.aliasTypes {
			nullability := "not null"
			if aliasType.Nullable {
				nullability = "null"
			}
			_, _ = fmt.Fprintf(os.Stdout, "- %s | base=%s | %s\n", aliasType.FQTN(), aliasType.SystemTypeName, nullability)
		}
	}

	if len(c.tableTypes) > 0 {
		_, _ = fmt.Fprintln(os.Stdout)
		_, _ = fmt.Fprintln(os.Stdout, "Table Types")
		for _, tableType := range c.tableTypes {
			_, _ = fmt.Fprintf(os.Stdout, "- %s | columns=%d\n", tableType.FQTN(), len(tableType.Columns))
		}
	}

	if len(c.sequences) > 0 {
		_, _ = fmt.Fprintln(os.Stdout)
		_, _ = fmt.Fprintln(os.Stdout, "Sequences")
		for _, sequence := range c.sequences {
			_, _ = fmt.Fprintf(os.Stdout, "- %s | restart with=%s | increment=%s\n", sequence.FQTN(), sequence.RestartWith, sequence.Increment)
		}
	}

	if len(c.tables) > 0 {
		_, _ = fmt.Fprintln(os.Stdout)
		_, _ = fmt.Fprintln(os.Stdout, "Tables")
		for _, table := range c.tables {
			_, _ = fmt.Fprintln(os.Stdout, formatTablePlan(table, c.cfg.DropExisting))
		}
	}
	if len(c.views) > 0 {
		_, _ = fmt.Fprintln(os.Stdout)
		_, _ = fmt.Fprintln(os.Stdout, "Views")
		for _, v := range c.views {
			depInfo := ""
			if len(v.DependsOn) > 0 {
				depInfo = fmt.Sprintf(" | depends on: %s", strings.Join(v.DependsOn, ", "))
			}
			_, _ = fmt.Fprintf(os.Stdout, "- %s%s\n", v.FQTN(), depInfo)
		}
	}
	if len(c.functions) > 0 {
		_, _ = fmt.Fprintln(os.Stdout)
		_, _ = fmt.Fprintln(os.Stdout, "Functions")
		for _, function := range c.functions {
			depInfo := ""
			if len(function.DependsOn) > 0 {
				depInfo = fmt.Sprintf(" | depends on: %s", strings.Join(function.DependsOn, ", "))
			}
			_, _ = fmt.Fprintf(os.Stdout, "- %s%s\n", function.FQTN(), depInfo)
		}
	}
	if len(c.procedures) > 0 {
		_, _ = fmt.Fprintln(os.Stdout)
		_, _ = fmt.Fprintln(os.Stdout, "Procedures")
		for _, procedure := range c.procedures {
			depInfo := ""
			if len(procedure.DependsOn) > 0 {
				depInfo = fmt.Sprintf(" | depends on: %s", strings.Join(procedure.DependsOn, ", "))
			}
			_, _ = fmt.Fprintf(os.Stdout, "- %s%s\n", procedure.FQTN(), depInfo)
		}
	}
	if len(c.triggers) > 0 {
		_, _ = fmt.Fprintln(os.Stdout)
		_, _ = fmt.Fprintln(os.Stdout, "Triggers")
		for _, trigger := range c.triggers {
			state := "enabled"
			if trigger.Disabled {
				state = "disabled"
			}
			depInfo := ""
			if len(trigger.DependsOn) > 0 {
				depInfo = fmt.Sprintf(" | depends on: %s", strings.Join(trigger.DependsOn, ", "))
			}
			_, _ = fmt.Fprintf(os.Stdout, "- %s | on %s | %s%s\n", trigger.FQTN(), trigger.TableFQTN(), state, depInfo)
		}
	}
	if len(c.synonyms) > 0 {
		_, _ = fmt.Fprintln(os.Stdout)
		_, _ = fmt.Fprintln(os.Stdout, "Synonyms")
		for _, synonym := range c.synonyms {
			_, _ = fmt.Fprintf(os.Stdout, "- %s | for %s\n", synonym.FQTN(), synonym.BaseObjectName)
		}
	}
}

func formatTablePlan(table tableMeta, dropExisting bool) string {
	if table.DependencyOnly {
		return fmt.Sprintf("- %s | dependency-only | columns=%d | actions=create if missing, no data copy", table.FQTN(), len(table.Columns))
	}
	actions := []string{"create schema if needed", "create table", "copy data", "create post-data objects"}
	if dropExisting {
		actions = append([]string{"drop target table if present"}, actions...)
	}

	mode := "bulk"
	if !table.BulkOK {
		mode = "row-insert"
	}

	line := fmt.Sprintf("- %s | rows~%d | columns=%d/%d copyable | mode=%s | actions=%s",
		table.FQTN(),
		table.ApproxRows,
		len(table.CopyColumns),
		len(table.Columns),
		mode,
		strings.Join(actions, ", "),
	)

	if table.HasIdentity {
		line += " | identity insert"
	}
	if !table.BulkOK && table.BulkReason != "" {
		line += " | bulk fallback: " + table.BulkReason
	}
	return line
}
