package copier

import (
	"fmt"
	"strings"
	"testing"

	mssql "github.com/denisenkom/go-mssqldb"
)

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
