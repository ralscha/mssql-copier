package copier

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMarkdownReportIncludesSummaryAndTableRows(t *testing.T) {
	c := &copier{
		cfg: config{ReportMDFile: "copy-report.md"},
		tables: []tableMeta{
			{
				Schema:      "dbo",
				Name:        "orders",
				ApproxRows:  11,
				Columns:     []columnMeta{{Name: "id"}, {Name: "status"}},
				CopyColumns: []columnMeta{{Name: "id"}, {Name: "status"}},
				HasIdentity: true,
				BulkOK:      true,
			},
			{
				Schema:      "sales",
				Name:        "audit_log",
				ApproxRows:  3,
				Columns:     []columnMeta{{Name: "payload"}},
				CopyColumns: nil,
				BulkOK:      false,
				BulkReason:  "unsupported LOB type",
			},
		},
	}

	c.report.record(c.tables[0], 12, 2*time.Second)
	c.report.record(c.tables[1], 0, 10*time.Millisecond)
	c.report.recordSkippedIndex(c.tables[0], indexMeta{
		Name:   "IX_orders_status",
		Unique: true,
		KeyColumns: []keyColumn{
			{Name: "status"},
		},
	}, errDuplicateKeyForTest())

	report := c.markdownReport(3 * time.Second)

	for _, want := range []string{
		"# Copy Report",
		"| Selected tables | 2 |",
		"| Total rows copied | 12 |",
		"| Skipped indexes | 1 |",
		"Largest copied table: [dbo].[orders] with 12 rows",
		"1 unique index(es) were skipped because copied data contains duplicate key values",
		"| [dbo].[orders] | 12 | 11 | 2/2 | bulk | 2s | identity insert |",
		"| [sales].[audit_log] | 0 | 3 | 0/1 | skipped | 10ms | no copyable columns |",
		"## Skipped Indexes",
		"| [dbo].[orders] | IX_orders_status | duplicate key values prevented unique index creation |",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestMarkdownReportRecordsBulkFallbackAsRowInsert(t *testing.T) {
	table := tableMeta{
		Schema:      "dbo",
		Name:        "orders",
		Columns:     []columnMeta{{Name: "id"}},
		CopyColumns: []columnMeta{{Name: "id"}},
		BulkOK:      false,
		BulkReason:  "flush bulk [dbo].[orders]: protocol error",
	}
	c := &copier{tables: []tableMeta{table}}
	c.report.record(table, 3, time.Second)

	report := c.markdownReport(time.Second)
	if !strings.Contains(report, "| [dbo].[orders] | 3 | 0 | 1/1 | row-insert |") {
		t.Fatalf("fallback table was not reported as row-insert:\n%s", report)
	}
	if !strings.Contains(report, "bulk fallback: flush bulk [dbo].[orders]: protocol error") {
		t.Fatalf("fallback reason missing from report:\n%s", report)
	}
}

func errDuplicateKeyForTest() error {
	return errors.New("mssql: The CREATE UNIQUE INDEX statement terminated because a duplicate key was found")
}
