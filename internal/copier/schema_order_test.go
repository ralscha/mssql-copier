package copier

import (
	"strings"
	"testing"
)

func TestSchemaCreationOrderHonorsCrossKindDependencies(t *testing.T) {
	c := &copier{
		tables: []tableMeta{
			{Schema: "dbo", Name: "base"},
			{Schema: "dbo", Name: "computed", DependsOn: []string{"[dbo].[format_value]"}},
		},
		functions: []functionMeta{{
			Schema: "dbo", Name: "format_value", DependsOn: []string{"[dbo].[base]"},
		}},
		views: []viewMeta{{
			Schema: "dbo", Name: "formatted_values", DependsOn: []string{"[dbo].[format_value]"},
		}},
	}

	ordered, err := c.schemaCreationOrder()
	if err != nil {
		t.Fatalf("schemaCreationOrder() unexpected error: %v", err)
	}
	positions := make(map[string]int, len(ordered))
	for i, object := range ordered {
		positions[strings.ToLower(object.name)] = i
	}
	assertBefore := func(first, second string) {
		t.Helper()
		if positions[first] >= positions[second] {
			t.Fatalf("expected %s before %s, positions=%v", first, second, positions)
		}
	}
	assertBefore("[dbo].[base]", "[dbo].[format_value]")
	assertBefore("[dbo].[format_value]", "[dbo].[computed]")
	assertBefore("[dbo].[format_value]", "[dbo].[formatted_values]")
}

func TestSchemaCreationOrderReportsCrossKindCycle(t *testing.T) {
	c := &copier{
		views:     []viewMeta{{Schema: "dbo", Name: "values", DependsOn: []string{"[dbo].[format_value]"}}},
		functions: []functionMeta{{Schema: "dbo", Name: "format_value", DependsOn: []string{"[dbo].[values]"}}},
	}

	_, err := c.schemaCreationOrder()
	if err == nil {
		t.Fatal("schemaCreationOrder() expected circular dependency error")
	}
	if !strings.Contains(err.Error(), "view [dbo].[values]") || !strings.Contains(err.Error(), "function [dbo].[format_value]") {
		t.Fatalf("cycle error does not identify objects: %v", err)
	}
}

func TestDataTablesExcludesDependencyOnlyTables(t *testing.T) {
	c := &copier{tables: []tableMeta{
		{Schema: "dbo", Name: "selected"},
		{Schema: "dbo", Name: "dependency", DependencyOnly: true},
	}}

	tables := c.dataTables()
	if len(tables) != 1 || tables[0].Name != "selected" {
		t.Fatalf("dataTables() = %+v, want only explicitly selected table", tables)
	}
}
