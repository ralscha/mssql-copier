package copier

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type copyReport struct {
	mu     sync.Mutex
	tables map[string]tableCopyReport
}

type tableCopyReport struct {
	Table              string
	RowsCopied         int64
	ApproxRows         int64
	CopyableColumns    int
	TotalColumns       int
	Mode               string
	Duration           time.Duration
	HasIdentity        bool
	NoCopyableColumns  bool
	BulkFallbackReason string
}

func (r *copyReport) record(table tableMeta, rowsCopied int64, duration time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tables == nil {
		r.tables = make(map[string]tableCopyReport)
	}
	mode := "bulk"
	if len(table.CopyColumns) == 0 {
		mode = "skipped"
	} else if !table.BulkOK {
		mode = "row-insert"
	}
	report := tableCopyReport{
		Table:              table.FQTN(),
		RowsCopied:         rowsCopied,
		ApproxRows:         table.ApproxRows,
		CopyableColumns:    len(table.CopyColumns),
		TotalColumns:       len(table.Columns),
		Mode:               mode,
		Duration:           duration.Round(time.Millisecond),
		HasIdentity:        table.HasIdentity,
		NoCopyableColumns:  len(table.CopyColumns) == 0,
		BulkFallbackReason: table.BulkReason,
	}
	r.tables[strings.ToLower(report.Table)] = report
}

func (c *copier) writeMarkdownReport(runDuration time.Duration) error {
	if c.cfg.ReportMDFile == "" {
		return nil
	}
	script := c.markdownReport(runDuration)
	if err := os.WriteFile(c.cfg.ReportMDFile, []byte(script), 0o600); err != nil {
		return fmt.Errorf("write markdown report %s: %w", c.cfg.ReportMDFile, err)
	}
	log.Printf("wrote markdown report to %s", c.cfg.ReportMDFile)
	return nil
}

func (c *copier) markdownReport(runDuration time.Duration) string {
	reports := c.report.snapshot(c.tables)
	type highlight struct {
		table string
		rows  int64
	}

	var totalRows int64
	var approxRows int64
	var bulkTables int
	var rowInsertTables int
	var skippedTables int
	var zeroRowTables int
	var identityTables int
	var largest highlight
	for _, report := range reports {
		totalRows += report.RowsCopied
		approxRows += report.ApproxRows
		switch report.Mode {
		case "bulk":
			bulkTables++
		case "row-insert":
			rowInsertTables++
		case "skipped":
			skippedTables++
		}
		if report.RowsCopied == 0 {
			zeroRowTables++
		}
		if report.HasIdentity {
			identityTables++
		}
		if report.RowsCopied > largest.rows {
			largest = highlight{table: report.Table, rows: report.RowsCopied}
		}
	}

	var builder strings.Builder
	builder.WriteString("# Copy Report\n\n")
	builder.WriteString(fmt.Sprintf("Generated: %s\n\n", time.Now().UTC().Format(time.RFC3339)))
	builder.WriteString("## Summary\n\n")
	builder.WriteString("| Metric | Value |\n")
	builder.WriteString("| --- | ---: |\n")
	builder.WriteString(fmt.Sprintf("| Selected tables | %d |\n", len(c.tables)))
	builder.WriteString(fmt.Sprintf("| Total rows copied | %d |\n", totalRows))
	builder.WriteString(fmt.Sprintf("| Approximate source rows | %d |\n", approxRows))
	builder.WriteString(fmt.Sprintf("| Bulk tables | %d |\n", bulkTables))
	builder.WriteString(fmt.Sprintf("| Row-insert tables | %d |\n", rowInsertTables))
	builder.WriteString(fmt.Sprintf("| Skipped tables | %d |\n", skippedTables))
	builder.WriteString(fmt.Sprintf("| Zero-row tables | %d |\n", zeroRowTables))
	builder.WriteString(fmt.Sprintf("| Identity tables | %d |\n", identityTables))
	builder.WriteString(fmt.Sprintf("| Run duration | %s |\n", runDuration.Round(time.Millisecond)))

	builder.WriteString("\n## Highlights\n\n")
	if largest.table != "" {
		builder.WriteString(fmt.Sprintf("- Largest copied table: %s with %d rows\n", largest.table, largest.rows))
	}
	builder.WriteString(fmt.Sprintf("- Estimate delta: %d actual rows versus %d approximate source rows\n", totalRows, approxRows))
	if skippedTables > 0 {
		builder.WriteString(fmt.Sprintf("- %d selected table(s) had no copyable columns and were skipped during data copy\n", skippedTables))
	}
	if rowInsertTables > 0 {
		builder.WriteString(fmt.Sprintf("- %d table(s) used row inserts instead of bulk copy\n", rowInsertTables))
	}

	builder.WriteString("\n## Tables\n\n")
	builder.WriteString("| Table | Rows Copied | Approx Rows | Columns | Mode | Duration | Notes |\n")
	builder.WriteString("| --- | ---: | ---: | ---: | --- | ---: | --- |\n")
	for _, report := range reports {
		builder.WriteString("| ")
		builder.WriteString(report.Table)
		builder.WriteString(" | ")
		builder.WriteString(fmt.Sprintf("%d", report.RowsCopied))
		builder.WriteString(" | ")
		builder.WriteString(fmt.Sprintf("%d", report.ApproxRows))
		builder.WriteString(" | ")
		builder.WriteString(fmt.Sprintf("%d/%d", report.CopyableColumns, report.TotalColumns))
		builder.WriteString(" | ")
		builder.WriteString(report.Mode)
		builder.WriteString(" | ")
		builder.WriteString(report.Duration.String())
		builder.WriteString(" | ")
		builder.WriteString(report.notes())
		builder.WriteString(" |\n")
	}

	return builder.String()
}

func (r *copyReport) snapshot(tables []tableMeta) []tableCopyReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	reports := make([]tableCopyReport, 0, len(tables))
	for _, table := range tables {
		report, ok := r.tables[strings.ToLower(table.FQTN())]
		if !ok {
			report = tableCopyReport{
				Table:           table.FQTN(),
				ApproxRows:      table.ApproxRows,
				CopyableColumns: len(table.CopyColumns),
				TotalColumns:    len(table.Columns),
				Mode:            "pending",
			}
		}
		reports = append(reports, report)
	}
	sort.Slice(reports, func(i, j int) bool {
		left := strings.ToLower(reports[i].Table)
		right := strings.ToLower(reports[j].Table)
		return left < right
	})
	return reports
}

func (r tableCopyReport) notes() string {
	var notes []string
	if r.NoCopyableColumns {
		notes = append(notes, "no copyable columns")
	}
	if r.HasIdentity {
		notes = append(notes, "identity insert")
	}
	if r.Mode == "row-insert" && r.BulkFallbackReason != "" {
		notes = append(notes, "bulk fallback: "+r.BulkFallbackReason)
	}
	if len(notes) == 0 {
		return "-"
	}
	return strings.Join(notes, "; ")
}