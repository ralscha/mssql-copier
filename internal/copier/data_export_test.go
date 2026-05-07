package copier

import (
	"database/sql"
	"math"
	"strings"
	"testing"
	"time"
)

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

	got := selectTableExportSQL(table)
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

	got := selectTableExportSQL(table)
	want := "SELECT [id], [name] FROM [sales].[orders] ORDER BY [id], [name]"
	if got != want {
		t.Fatalf("selectTableExportSQL() = %q, want %q", got, want)
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
