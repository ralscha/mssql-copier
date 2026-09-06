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
	mu             sync.Mutex
	tables         map[string]tableCopyReport
	skippedIndexes []skippedIndexReport
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

type skippedIndexReport struct {
	Table string
	Index string
	SQL   string
	Error string
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

func (r *copyReport) recordSkippedIndex(table tableMeta, index indexMeta, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skippedIndexes = append(r.skippedIndexes, skippedIndexReport{
		Table: table.FQTN(),
		Index: index.Name,
		SQL:   table.IndexSQL(index),
		Error: err.Error(),
	})
}

func (c *copier) writeMarkdownReport(runDuration time.Duration) error {
	if c.cfg.ReportMDFile == "" {
		return nil
	}
	script := c.markdownReport(runDuration)
	if err := ensureOutputDirectory(c.cfg.ReportMDFile, "report output"); err != nil {
		return err
	}
	if err := os.WriteFile(c.cfg.ReportMDFile, []byte(script), 0o600); err != nil {
		return fmt.Errorf("write markdown report %s: %w", c.cfg.ReportMDFile, err)
	}
	log.Printf("wrote markdown report to %s", c.cfg.ReportMDFile)
	return nil
}

func (c *copier) markdownReport(runDuration time.Duration) string {
	dataTables := c.dataTables()
	reports := c.report.snapshot(dataTables)
	skippedIndexes := c.report.skippedIndexSnapshot()
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
	fmt.Fprintf(&builder, "Generated: %s\n\n", time.Now().UTC().Format(time.RFC3339))
	builder.WriteString("## Summary\n\n")
	builder.WriteString("| Metric | Value |\n")
	builder.WriteString("| --- | ---: |\n")
	fmt.Fprintf(&builder, "| Selected tables | %d |\n", len(dataTables))
	fmt.Fprintf(&builder, "| Dependency-only tables | %d |\n", len(c.tables)-len(dataTables))
	fmt.Fprintf(&builder, "| Total rows copied | %d |\n", totalRows)
	fmt.Fprintf(&builder, "| Approximate source rows | %d |\n", approxRows)
	fmt.Fprintf(&builder, "| Bulk tables | %d |\n", bulkTables)
	fmt.Fprintf(&builder, "| Row-insert tables | %d |\n", rowInsertTables)
	fmt.Fprintf(&builder, "| Skipped tables | %d |\n", skippedTables)
	fmt.Fprintf(&builder, "| Zero-row tables | %d |\n", zeroRowTables)
	fmt.Fprintf(&builder, "| Identity tables | %d |\n", identityTables)
	fmt.Fprintf(&builder, "| Skipped indexes | %d |\n", len(skippedIndexes))
	fmt.Fprintf(&builder, "| Run duration | %s |\n", runDuration.Round(time.Millisecond))

	builder.WriteString("\n## Highlights\n\n")
	if largest.table != "" {
		fmt.Fprintf(&builder, "- Largest copied table: %s with %d rows\n", largest.table, largest.rows)
	}
	fmt.Fprintf(&builder, "- Estimate delta: %d actual rows versus %d approximate source rows\n", totalRows, approxRows)
	if skippedTables > 0 {
		fmt.Fprintf(&builder, "- %d selected table(s) had no copyable columns and were skipped during data copy\n", skippedTables)
	}
	if rowInsertTables > 0 {
		fmt.Fprintf(&builder, "- %d table(s) used row inserts instead of bulk copy\n", rowInsertTables)
	}
	if len(skippedIndexes) > 0 {
		fmt.Fprintf(&builder, "- %d unique index(es) were skipped because copied data contains duplicate key values\n", len(skippedIndexes))
	}

	builder.WriteString("\n## Tables\n\n")
	builder.WriteString("| Table | Rows Copied | Approx Rows | Columns | Mode | Duration | Notes |\n")
	builder.WriteString("| --- | ---: | ---: | ---: | --- | ---: | --- |\n")
	for _, report := range reports {
		builder.WriteString("| ")
		builder.WriteString(report.Table)
		builder.WriteString(" | ")
		fmt.Fprintf(&builder, "%d", report.RowsCopied)
		builder.WriteString(" | ")
		fmt.Fprintf(&builder, "%d", report.ApproxRows)
		builder.WriteString(" | ")
		fmt.Fprintf(&builder, "%d/%d", report.CopyableColumns, report.TotalColumns)
		builder.WriteString(" | ")
		builder.WriteString(report.Mode)
		builder.WriteString(" | ")
		builder.WriteString(report.Duration.String())
		builder.WriteString(" | ")
		builder.WriteString(report.notes())
		builder.WriteString(" |\n")
	}

	if len(skippedIndexes) > 0 {
		builder.WriteString("\n## Skipped Indexes\n\n")
		builder.WriteString("| Table | Index | Reason |\n")
		builder.WriteString("| --- | --- | --- |\n")
		for _, skipped := range skippedIndexes {
			builder.WriteString("| ")
			builder.WriteString(skipped.Table)
			builder.WriteString(" | ")
			builder.WriteString(skipped.Index)
			builder.WriteString(" | duplicate key values prevented unique index creation |")
			builder.WriteString("\n")
		}
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

func (r *copyReport) skippedIndexSnapshot() []skippedIndexReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	skipped := append([]skippedIndexReport(nil), r.skippedIndexes...)
	sort.Slice(skipped, func(i, j int) bool {
		left := strings.ToLower(skipped[i].Table + "." + skipped[i].Index)
		right := strings.ToLower(skipped[j].Table + "." + skipped[j].Index)
		return left < right
	})
	return skipped
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

func (c *copier) logSkippedIndexSummary() {
	for _, skipped := range c.report.skippedIndexSnapshot() {
		log.Printf("WARNING: skipped index missing on target: %s.%s (duplicate key values prevented unique index creation)", skipped.Table, skipped.Index)
	}
}
