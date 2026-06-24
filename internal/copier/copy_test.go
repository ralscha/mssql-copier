package copier

import (
	"fmt"
	"strings"
	"testing"

	mssql "github.com/denisenkom/go-mssqldb"
)

func TestTopologicalSortViews(t *testing.T) {
	views := []viewMeta{
		{
			Schema:    "reporting",
			Name:      "base_orders",
			DependsOn: nil,
		},
		{
			Schema:    "reporting",
			Name:      "order_names",
			DependsOn: []string{"[reporting].[base_orders]"},
		},
		{
			Schema:    "reporting",
			Name:      "order_names_filtered",
			DependsOn: []string{"[reporting].[order_names]"},
		},
	}

	ordered, err := topologicalSortViews(views)
	if err != nil {
		t.Fatalf("topologicalSortViews() unexpected error: %v", err)
	}
	if len(ordered) != len(views) {
		t.Fatalf("topologicalSortViews() returned %d views, want %d", len(ordered), len(views))
	}

	positions := make(map[string]int, len(ordered))
	for i, view := range ordered {
		positions[strings.ToLower(view.FQTN())] = i
	}

	if positions["[reporting].[base_orders]"] > positions["[reporting].[order_names]"] {
		t.Fatalf("expected base_orders before order_names, got %v", positions)
	}
	if positions["[reporting].[order_names]"] > positions["[reporting].[order_names_filtered]"] {
		t.Fatalf("expected order_names before order_names_filtered, got %v", positions)
	}
}

func TestIsDuplicateKeyUniqueIndexError(t *testing.T) {
	uniqueIndex := indexMeta{Name: "IX_users_name", Unique: true}

	err := fmt.Errorf("create index: %w", mssql.Error{Number: 1505, Message: "duplicate key"})
	if !isDuplicateKeyUniqueIndexError(uniqueIndex, err) {
		t.Fatal("expected duplicate key error for unique index to be skippable")
	}

	nonUniqueIndex := indexMeta{Name: "IX_users_name", Unique: false}
	if isDuplicateKeyUniqueIndexError(nonUniqueIndex, err) {
		t.Fatal("expected duplicate key error for non-unique index to remain fatal")
	}

	otherErr := mssql.Error{Number: 1919, Message: "invalid column type"}
	if isDuplicateKeyUniqueIndexError(uniqueIndex, otherErr) {
		t.Fatal("expected non-duplicate unique index error to remain fatal")
	}
}

func TestTopologicalSortViewsCircularDependency(t *testing.T) {
	views := []viewMeta{
		{
			Schema:    "reporting",
			Name:      "view_a",
			DependsOn: []string{"[reporting].[view_b]"},
		},
		{
			Schema:    "reporting",
			Name:      "view_b",
			DependsOn: []string{"[reporting].[view_a]"},
		},
	}

	_, err := topologicalSortViews(views)
	if err == nil {
		t.Fatal("topologicalSortViews() expected circular dependency error, got nil")
	}
	if !strings.Contains(err.Error(), "circular dependency") {
		t.Fatalf("expected circular dependency error, got %v", err)
	}
}

func TestTopologicalSortFunctionsCircularDependency(t *testing.T) {
	functions := []functionMeta{
		{
			Schema:    "reporting",
			Name:      "fn_a",
			DependsOn: []string{"[reporting].[fn_b]"},
		},
		{
			Schema:    "reporting",
			Name:      "fn_b",
			DependsOn: []string{"[reporting].[fn_a]"},
		},
	}

	_, err := topologicalSortFunctions(functions)
	if err == nil {
		t.Fatal("topologicalSortFunctions() expected circular dependency error, got nil")
	}
	if !strings.Contains(err.Error(), "circular dependency") {
		t.Fatalf("expected circular dependency error, got %v", err)
	}
}

func TestTopologicalSortProcedures(t *testing.T) {
	procedures := []procedureMeta{
		{
			Schema:    "reporting",
			Name:      "base_proc",
			DependsOn: nil,
		},
		{
			Schema:    "reporting",
			Name:      "dependent_proc",
			DependsOn: []string{"[reporting].[base_proc]", "[reporting].[orders_alias]"},
		},
	}

	ordered, err := topologicalSortProcedures(procedures)
	if err != nil {
		t.Fatalf("topologicalSortProcedures() unexpected error: %v", err)
	}
	if len(ordered) != len(procedures) {
		t.Fatalf("topologicalSortProcedures() returned %d procedures, want %d", len(ordered), len(procedures))
	}

	positions := make(map[string]int, len(ordered))
	for i, procedure := range ordered {
		positions[strings.ToLower(procedure.FQTN())] = i
	}

	if positions["[reporting].[base_proc]"] > positions["[reporting].[dependent_proc]"] {
		t.Fatalf("expected base_proc before dependent_proc, got %v", positions)
	}
	if len(ordered[1].DependsOn) != 2 {
		t.Fatalf("expected non-procedure dependency to remain recorded, got %+v", ordered[1].DependsOn)
	}
}

func TestTopologicalSortProceduresCircularDependency(t *testing.T) {
	procedures := []procedureMeta{
		{
			Schema:    "reporting",
			Name:      "proc_a",
			DependsOn: []string{"[reporting].[proc_b]"},
		},
		{
			Schema:    "reporting",
			Name:      "proc_b",
			DependsOn: []string{"[reporting].[proc_a]"},
		},
	}

	_, err := topologicalSortProcedures(procedures)
	if err == nil {
		t.Fatal("topologicalSortProcedures() expected circular dependency error, got nil")
	}
	if !strings.Contains(err.Error(), "circular dependency") {
		t.Fatalf("expected circular dependency error, got %v", err)
	}
}

func TestTopologicalSortTriggers(t *testing.T) {
	triggers := []triggerMeta{
		{
			Schema:    "reporting",
			Name:      "trg_base",
			DependsOn: nil,
		},
		{
			Schema:    "reporting",
			Name:      "trg_dependent",
			DependsOn: []string{"[reporting].[trg_base]", "[reporting].[helper_view]"},
		},
	}

	ordered, err := topologicalSortTriggers(triggers)
	if err != nil {
		t.Fatalf("topologicalSortTriggers() unexpected error: %v", err)
	}
	if len(ordered) != len(triggers) {
		t.Fatalf("topologicalSortTriggers() returned %d triggers, want %d", len(ordered), len(triggers))
	}

	positions := make(map[string]int, len(ordered))
	for i, trigger := range ordered {
		positions[strings.ToLower(trigger.FQTN())] = i
	}

	if positions["[reporting].[trg_base]"] > positions["[reporting].[trg_dependent]"] {
		t.Fatalf("expected trg_base before trg_dependent, got %v", positions)
	}
	if len(ordered[1].DependsOn) != 2 {
		t.Fatalf("expected non-trigger dependency to remain recorded, got %+v", ordered[1].DependsOn)
	}
}

func TestTopologicalSortTriggersCircularDependency(t *testing.T) {
	triggers := []triggerMeta{
		{
			Schema:    "reporting",
			Name:      "trg_a",
			DependsOn: []string{"[reporting].[trg_b]"},
		},
		{
			Schema:    "reporting",
			Name:      "trg_b",
			DependsOn: []string{"[reporting].[trg_a]"},
		},
	}

	_, err := topologicalSortTriggers(triggers)
	if err == nil {
		t.Fatal("topologicalSortTriggers() expected circular dependency error, got nil")
	}
	if !strings.Contains(err.Error(), "circular dependency") {
		t.Fatalf("expected circular dependency error, got %v", err)
	}
}
