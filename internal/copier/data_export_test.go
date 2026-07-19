package copier

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteFileAtomicallyPreservesExistingFileOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sql")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatalf("seed destination: %v", err)
	}

	wantErr := errors.New("generation failed")
	err := writeFileAtomically(path, func(writer io.Writer) error {
		_, _ = io.WriteString(writer, "partial")
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("writeFileAtomically() error = %v, want %v", err, wantErr)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read preserved destination: %v", err)
	}
	if string(got) != "original" {
		t.Fatalf("destination = %q, want original content", got)
	}
	temporary, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".data.sql.tmp-*"))
	if err != nil {
		t.Fatalf("glob temporary files: %v", err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary files were not cleaned up: %v", temporary)
	}
}

func TestWriteFileAtomicallyReplacesDestinationOnSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sql")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatalf("seed destination: %v", err)
	}
	if err := writeFileAtomically(path, func(writer io.Writer) error {
		_, err := io.WriteString(writer, "replacement")
		return err
	}); err != nil {
		t.Fatalf("writeFileAtomically() unexpected error: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(got) != "replacement" {
		t.Fatalf("destination = %q, want replacement", got)
	}
}

func TestSelectTableExportSQLOrdersByPrimaryKey(t *testing.T) {
	table := tableMeta{
		Schema: "sales",
		Name:   "orders",
		CopyColumns: []columnMeta{
			{Name: "id"},
			{Name: "created_at"},
		},
		PrimaryKey: &keyConstraint{Columns: []keyColumn{{Name: "id", Desc: true}}},
	}

	got := selectTableExportSQL(table, 0)
	want := "SELECT [id], [created_at] FROM [sales].[orders] ORDER BY [id] DESC"
	if got != want {
		t.Fatalf("selectTableExportSQL() = %q, want %q", got, want)
	}
}

func TestSelectTableExportSQLFallsBackToCopyColumns(t *testing.T) {
	table := tableMeta{
		Schema: "sales",
		Name:   "orders",
		CopyColumns: []columnMeta{
			{Name: "id"},
			{Name: "name"},
		},
	}

	got := selectTableExportSQL(table, 0)
	want := "SELECT [id], [name] FROM [sales].[orders] ORDER BY [id], [name]"
	if got != want {
		t.Fatalf("selectTableExportSQL() = %q, want %q", got, want)
	}
}

func TestSelectTableExportSQLAppliesRowLimit(t *testing.T) {
	table := tableMeta{
		Schema: "sales",
		Name:   "orders",
		CopyColumns: []columnMeta{
			{Name: "id"},
			{Name: "created_at"},
		},
		PrimaryKey: &keyConstraint{Columns: []keyColumn{{Name: "id"}}},
	}

	got := selectTableExportSQL(table, 25)
	want := "SELECT TOP (25) [id], [created_at] FROM [sales].[orders] ORDER BY [id] ASC"
	if got != want {
		t.Fatalf("selectTableExportSQL() = %q, want %q", got, want)
	}
}

func TestExportParentBatchSizeStaysWithinSQLServerParameterBudget(t *testing.T) {
	for _, width := range []int{1, 2, 3, 100, 1024} {
		batchSize := exportParentBatchSize(width)
		if batchSize < 1 {
			t.Fatalf("exportParentBatchSize(%d) = %d, want at least one tuple", width, batchSize)
		}
		if batchSize*width > maxExportQueryParameters {
			t.Fatalf("exportParentBatchSize(%d) uses %d parameters, budget is %d", width, batchSize*width, maxExportQueryParameters)
		}
	}
	if got := exportParentBatchSize(0); got != 0 {
		t.Fatalf("exportParentBatchSize(0) = %d, want 0", got)
	}
}

func TestSQLLiteral(t *testing.T) {
	numericColumnType := new(sql.ColumnType)

	tests := []struct {
		name  string
		value any
		col   columnMeta
		want  string
	}{
		{name: "null", value: nil, col: columnMeta{}, want: "NULL"},
		{name: "nvarchar", value: "O'Brien", col: columnMeta{SystemTypeName: "nvarchar"}, want: "N'O''Brien'"},
		{name: "varchar", value: "plain", col: columnMeta{SystemTypeName: "varchar"}, want: "'plain'"},
		{name: "varbinary", value: []byte{0xDE, 0xAD, 0xBE, 0xEF}, col: columnMeta{SystemTypeName: "varbinary"}, want: "0xDEADBEEF"},
		{name: "bit true", value: true, col: columnMeta{SystemTypeName: "bit"}, want: "1"},
		{name: "int64", value: int64(42), col: columnMeta{SystemTypeName: "int"}, want: "42"},
		{name: "decimal string", value: decimalString("12.34"), col: columnMeta{SystemTypeName: "decimal"}, want: "12.34"},
		{name: "datetime", value: time.Date(2024, time.January, 2, 3, 4, 5, 678900000, time.FixedZone("UTC+2", 2*60*60)), col: columnMeta{SystemTypeName: "datetime2"}, want: "'2024-01-02 01:04:05.6789'"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := sqlLiteral(test.value, test.col, numericColumnType)
			if err != nil {
				t.Fatalf("sqlLiteral() unexpected error: %v", err)
			}
			if got != test.want {
				t.Fatalf("sqlLiteral() = %q, want %q", got, test.want)
			}
		})
	}

	_, err := sqlLiteral(math.NaN(), columnMeta{SystemTypeName: "float"}, numericColumnType)
	if err == nil {
		t.Fatal("sqlLiteral() expected error for unsupported float-like value, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported error, got %v", err)
	}
}

func TestBuildDataExportSQLNoTables(t *testing.T) {
	c := &copier{}
	got, err := c.buildDataExportSQL(t.Context())
	if err != nil {
		t.Fatalf("buildDataExportSQL() unexpected error: %v", err)
	}
	if got != "-- mssql-copier data export\n" {
		t.Fatalf("buildDataExportSQL() = %q", got)
	}
}

func TestResolveSampledExportRowsAddsReferencedParents(t *testing.T) {
	parent := tableMeta{
		Schema: "dbo",
		Name:   "customers",
		CopyColumns: []columnMeta{
			{Name: "id"},
			{Name: "name"},
		},
		PrimaryKey: &keyConstraint{Columns: []keyColumn{{Name: "id"}}},
	}
	child := tableMeta{
		Schema: "dbo",
		Name:   "orders",
		CopyColumns: []columnMeta{
			{Name: "id"},
			{Name: "customer_id"},
		},
		PrimaryKey: &keyConstraint{Columns: []keyColumn{{Name: "id"}}},
		ForeignKeys: []foreignKey{
			{Name: "FK_orders_customers", Columns: []string{"customer_id"}, RefSchema: "dbo", RefTable: "customers", RefColumns: []string{"id"}},
		},
	}

	baseRows := map[string][]exportRow{
		normalizedTableKey(parent): nil,
		normalizedTableKey(child): {
			{values: []any{int64(10), int64(7)}},
		},
	}

	fetchCalls := 0
	got, err := resolveSampledExportRows(context.Background(), []tableMeta{parent, child}, baseRows, func(ctx context.Context, table tableMeta, columns []string, tuples [][]any) ([]exportRow, error) {
		fetchCalls++
		if table.Name != "customers" {
			t.Fatalf("unexpected fetch table %s", table.FQTN())
		}
		if len(columns) != 1 || columns[0] != "id" {
			t.Fatalf("unexpected fetch columns %#v", columns)
		}
		if len(tuples) != 1 || len(tuples[0]) != 1 || tuples[0][0] != int64(7) {
			t.Fatalf("unexpected tuples %#v", tuples)
		}
		return []exportRow{{values: []any{int64(7), "Acme"}}}, nil
	})
	if err != nil {
		t.Fatalf("resolveSampledExportRows() unexpected error: %v", err)
	}
	if fetchCalls != 1 {
		t.Fatalf("fetchCalls = %d, want 1", fetchCalls)
	}
	if len(got[normalizedTableKey(parent)]) != 1 {
		t.Fatalf("parent row count = %d, want 1", len(got[normalizedTableKey(parent)]))
	}
	if len(got[normalizedTableKey(child)]) != 1 {
		t.Fatalf("child row count = %d, want 1", len(got[normalizedTableKey(child)]))
	}
}

func TestResolveSampledExportRowsRecursesToGrandparents(t *testing.T) {
	grandparent := tableMeta{
		Schema:      "dbo",
		Name:        "accounts",
		CopyColumns: []columnMeta{{Name: "id"}},
		PrimaryKey:  &keyConstraint{Columns: []keyColumn{{Name: "id"}}},
	}
	parent := tableMeta{
		Schema:      "dbo",
		Name:        "customers",
		CopyColumns: []columnMeta{{Name: "id"}, {Name: "account_id"}},
		PrimaryKey:  &keyConstraint{Columns: []keyColumn{{Name: "id"}}},
		ForeignKeys: []foreignKey{{Name: "FK_customers_accounts", Columns: []string{"account_id"}, RefSchema: "dbo", RefTable: "accounts", RefColumns: []string{"id"}}},
	}
	child := tableMeta{
		Schema:      "dbo",
		Name:        "orders",
		CopyColumns: []columnMeta{{Name: "id"}, {Name: "customer_id"}},
		PrimaryKey:  &keyConstraint{Columns: []keyColumn{{Name: "id"}}},
		ForeignKeys: []foreignKey{{Name: "FK_orders_customers", Columns: []string{"customer_id"}, RefSchema: "dbo", RefTable: "customers", RefColumns: []string{"id"}}},
	}

	baseRows := map[string][]exportRow{
		normalizedTableKey(grandparent): nil,
		normalizedTableKey(parent):      nil,
		normalizedTableKey(child):       {{values: []any{int64(100), int64(7)}}},
	}

	got, err := resolveSampledExportRows(context.Background(), []tableMeta{grandparent, parent, child}, baseRows, func(ctx context.Context, table tableMeta, columns []string, tuples [][]any) ([]exportRow, error) {
		switch table.Name {
		case "customers":
			return []exportRow{{values: []any{int64(7), int64(3)}}}, nil
		case "accounts":
			return []exportRow{{values: []any{int64(3)}}}, nil
		default:
			t.Fatalf("unexpected fetch table %s", table.FQTN())
			return nil, nil
		}
	})
	if err != nil {
		t.Fatalf("resolveSampledExportRows() unexpected error: %v", err)
	}
	if len(got[normalizedTableKey(parent)]) != 1 {
		t.Fatalf("parent row count = %d, want 1", len(got[normalizedTableKey(parent)]))
	}
	if len(got[normalizedTableKey(grandparent)]) != 1 {
		t.Fatalf("grandparent row count = %d, want 1", len(got[normalizedTableKey(grandparent)]))
	}
}

func TestSortedTablesForDataExport(t *testing.T) {
	tables := []tableMeta{
		{Schema: "sales", Name: "orders"},
		{Schema: "dbo", Name: "users"},
		{Schema: "sales", Name: "customers"},
	}

	got := sortedTablesForDataExport(tables)
	want := []string{"[dbo].[users]", "[sales].[customers]", "[sales].[orders]"}
	if len(got) != len(want) {
		t.Fatalf("sortedTablesForDataExport() len = %d, want %d", len(got), len(want))
	}
	for i, table := range got {
		if table.FQTN() != want[i] {
			t.Fatalf("sortedTablesForDataExport()[%d] = %s, want %s", i, table.FQTN(), want[i])
		}
	}
}

type decimalString string

func (d decimalString) String() string {
	return string(d)
}
