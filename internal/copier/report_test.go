package copier

import (
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

	report := c.markdownReport(3 * time.Second)

	for _, want := range []string{
		"# Copy Report",
		"| Selected tables | 2 |",
		"| Total rows copied | 12 |",
		"Largest copied table: [dbo].[orders] with 12 rows",
		"| [dbo].[orders] | 12 | 11 | 2/2 | bulk | 2s | identity insert |",
		"| [sales].[audit_log] | 0 | 3 | 0/1 | skipped | 10ms | no copyable columns |",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}
