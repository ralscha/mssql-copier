package copier

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type exportRow struct {
	values []any
}

const maxExportQueryParameters = 2000

func (c *copier) writeDataExportFile(ctx context.Context) error {
	dir := filepath.Dir(c.cfg.ExportDataFile)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create data export output directory: %w", err)
		}
	}

	return writeFileAtomically(c.cfg.ExportDataFile, func(writer io.Writer) error {
		return c.writeDataExportSQL(ctx, writer)
	})
}

func writeFileAtomically(path string, write func(io.Writer) error) error {
	tempDir := filepath.Dir(path)
	if tempDir == "" {
		tempDir = "."
	}
	file, err := os.CreateTemp(tempDir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary data export file: %w", err)
	}
	tempPath := file.Name()
	closed := false
	committed := false
	defer func() {
		if !closed {
			closeAndLog(file, "temporary data export file")
		}
		if !committed {
			if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
				log.Printf("remove temporary data export file %s: %v", tempPath, err)
			}
		}
	}()

	writer := bufio.NewWriterSize(file, 256*1024)
	if err := write(writer); err != nil {
		return err
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush data export file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync data export file: %w", err)
	}
	if err := file.Close(); err != nil {
		closed = true
		return fmt.Errorf("close data export file: %w", err)
	}
	closed = true
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace data export file: %w", err)
	}
	committed = true
	return nil
}

func (c *copier) buildDataExportSQL(ctx context.Context) (string, error) {
	var builder strings.Builder
	if err := c.writeDataExportSQL(ctx, &builder); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func (c *copier) writeDataExportSQL(ctx context.Context, writer io.Writer) error {
	if _, err := io.WriteString(writer, "-- mssql-copier data export\n"); err != nil {
		return fmt.Errorf("write data export header: %w", err)
	}
	dataTables := c.dataTables()
	if len(dataTables) == 0 {
		return nil
	}

	tables := sortedTablesForDataExport(dataTables)
	var exportRowsByTable map[string][]exportRow
	if c.cfg.ExportDataRows > 0 {
		var err error
		exportRowsByTable, err = c.buildExportRowsByTable(ctx, tables)
		if err != nil {
			return err
		}
	}

	if _, err := io.WriteString(writer, "\n"); err != nil {
		return fmt.Errorf("write data export separator: %w", err)
	}
	for _, table := range tables {
		if _, err := fmt.Fprintf(writer, "ALTER TABLE %s NOCHECK CONSTRAINT ALL;\n", table.FQTN()); err != nil {
			return fmt.Errorf("write data export constraint preamble: %w", err)
		}
	}

	for _, table := range tables {
		if _, err := io.WriteString(writer, "\n"); err != nil {
			return fmt.Errorf("write data export separator: %w", err)
		}
		if c.cfg.ExportDataRows > 0 {
			if err := c.writeTableDataSection(writer, table, exportRowsByTable[normalizedTableKey(table)]); err != nil {
				return err
			}
		} else if err := c.streamTableDataSection(ctx, writer, table); err != nil {
			return err
		}
	}

	if _, err := io.WriteString(writer, "\n"); err != nil {
		return fmt.Errorf("write data export separator: %w", err)
	}
	for _, table := range tables {
		if _, err := fmt.Fprintf(writer, "ALTER TABLE %s WITH CHECK CHECK CONSTRAINT ALL;\n", table.FQTN()); err != nil {
			return fmt.Errorf("write data export constraint epilogue: %w", err)
		}
	}
	return nil
}

func sortedTablesForDataExport(tables []tableMeta) []tableMeta {
	ordered := append([]tableMeta(nil), tables...)
	sort.Slice(ordered, func(i int, j int) bool {
		left := strings.ToLower(ordered[i].FQTN())
		right := strings.ToLower(ordered[j].FQTN())
		return left < right
	})
	return ordered
}

func (c *copier) buildExportRowsByTable(ctx context.Context, tables []tableMeta) (map[string][]exportRow, error) {
	rowsByTable := make(map[string][]exportRow, len(tables))
	for _, table := range tables {
		rows, err := c.fetchTableExportRows(ctx, table, c.cfg.ExportDataRows)
		if err != nil {
			return nil, err
		}
		rowsByTable[normalizedTableKey(table)] = rows
	}
	if c.cfg.ExportDataRows <= 0 {
		return rowsByTable, nil
	}
	return resolveSampledExportRows(ctx, tables, rowsByTable, c.fetchParentRowsForExport)
}

func (c *copier) fetchTableExportRows(ctx context.Context, table tableMeta, rowLimit int) ([]exportRow, error) {
	if len(table.CopyColumns) == 0 {
		return nil, nil
	}

	rows, err := c.sourceDB.QueryContext(ctx, selectTableExportSQL(table, rowLimit))
	if err != nil {
		return nil, fmt.Errorf("query data export %s: %w", table.FQTN(), err)
	}
	defer closeAndLog(rows, "data export rows")

	values := make([]any, len(table.CopyColumns))
	scanDest := make([]any, len(table.CopyColumns))
	for i := range values {
		scanDest[i] = &values[i]
	}

	var result []exportRow
	for rows.Next() {
		if err := rows.Scan(scanDest...); err != nil {
			return nil, fmt.Errorf("scan data export row %s: %w", table.FQTN(), err)
		}
		rowValues := make([]any, len(table.CopyColumns))
		for i, col := range table.CopyColumns {
			rowValues[i] = normalizeValue(values[i], col)
		}
		result = append(result, exportRow{values: rowValues})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate data export rows %s: %w", table.FQTN(), err)
	}
	return result, nil
}

func (c *copier) fetchParentRowsForExport(ctx context.Context, table tableMeta, refColumns []string, tuples [][]any) ([]exportRow, error) {
	if len(table.CopyColumns) == 0 || len(refColumns) == 0 || len(tuples) == 0 {
		return nil, nil
	}
	batchSize := exportParentBatchSize(len(refColumns))
	if batchSize < 1 {
		return nil, fmt.Errorf("query parent rows %s: foreign key width %d exceeds the export query parameter budget", table.FQTN(), len(refColumns))
	}

	result := make([]exportRow, 0)
	for start := 0; start < len(tuples); start += batchSize {
		end := min(start+batchSize, len(tuples))
		rows, err := c.fetchParentRowBatch(ctx, table, refColumns, tuples[start:end])
		if err != nil {
			return nil, err
		}
		result = append(result, rows...)
	}
	return result, nil
}

func exportParentBatchSize(columnCount int) int {
	if columnCount <= 0 {
		return 0
	}
	return maxExportQueryParameters / columnCount
}

func (c *copier) fetchParentRowBatch(ctx context.Context, table tableMeta, refColumns []string, tuples [][]any) ([]exportRow, error) {
	whereParts := make([]string, 0, len(tuples))
	args := make([]any, 0, len(refColumns)*len(tuples))
	paramOrdinal := 1
	for _, tuple := range tuples {
		if len(tuple) != len(refColumns) {
			return nil, fmt.Errorf("query parent rows %s: tuple width %d does not match reference column width %d", table.FQTN(), len(tuple), len(refColumns))
		}
		predicates := make([]string, 0, len(refColumns))
		for i, columnName := range refColumns {
			predicates = append(predicates, fmt.Sprintf("%s = @p%d", quoteIdent(columnName), paramOrdinal))
			args = append(args, tuple[i])
			paramOrdinal++
		}
		whereParts = append(whereParts, "("+strings.Join(predicates, " AND ")+")")
	}

	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s", joinQuotedColumns(table.CopyColumns), table.FQTN(), strings.Join(whereParts, " OR ")) //nolint:gosec // columns and table name are quoted; WHERE predicates use parameterized placeholders
	if orderBy := tableExportOrderBy(table); orderBy != "" {
		query += " ORDER BY " + orderBy
	}

	rows, err := c.sourceDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query referenced parent rows %s: %w", table.FQTN(), err)
	}
	defer closeAndLog(rows, "referenced parent rows")

	values := make([]any, len(table.CopyColumns))
	scanDest := make([]any, len(table.CopyColumns))
	for i := range values {
		scanDest[i] = &values[i]
	}

	result := make([]exportRow, 0)
	for rows.Next() {
		if err := rows.Scan(scanDest...); err != nil {
			return nil, fmt.Errorf("scan referenced parent row %s: %w", table.FQTN(), err)
		}
		rowValues := make([]any, len(table.CopyColumns))
		for i, col := range table.CopyColumns {
			rowValues[i] = normalizeValue(values[i], col)
		}
		result = append(result, exportRow{values: rowValues})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate referenced parent rows %s: %w", table.FQTN(), err)
	}
	return result, nil
}

func (c *copier) writeTableDataSection(writer io.Writer, table tableMeta, rowsData []exportRow) error {
	if err := writeTableDataHeader(writer, table); err != nil {
		return err
	}
	if len(table.CopyColumns) == 0 {
		return nil
	}
	for _, row := range rowsData {
		if err := c.writeTableDataRow(writer, table, row); err != nil {
			return err
		}
	}
	return writeTableDataFooter(writer, table, len(rowsData))
}

func (c *copier) streamTableDataSection(ctx context.Context, writer io.Writer, table tableMeta) error {
	if err := writeTableDataHeader(writer, table); err != nil {
		return err
	}
	if len(table.CopyColumns) == 0 {
		return nil
	}

	rows, err := c.sourceDB.QueryContext(ctx, selectTableExportSQL(table, 0))
	if err != nil {
		return fmt.Errorf("query data export %s: %w", table.FQTN(), err)
	}
	defer closeAndLog(rows, "streaming data export rows")

	values := make([]any, len(table.CopyColumns))
	scanDest := make([]any, len(table.CopyColumns))
	for i := range values {
		scanDest[i] = &values[i]
	}

	rowCount := 0
	for rows.Next() {
		if err := rows.Scan(scanDest...); err != nil {
			return fmt.Errorf("scan data export row %s: %w", table.FQTN(), err)
		}
		if err := c.writeTableDataRow(writer, table, exportRow{values: values}); err != nil {
			return err
		}
		rowCount++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate data export rows %s: %w", table.FQTN(), err)
	}
	return writeTableDataFooter(writer, table, rowCount)
}

func writeTableDataHeader(writer io.Writer, table tableMeta) error {
	if _, err := fmt.Fprintf(writer, "-- table %s\n", table.FQTN()); err != nil {
		return fmt.Errorf("write data export table header: %w", err)
	}
	if len(table.CopyColumns) == 0 {
		if _, err := io.WriteString(writer, "-- skipped: no copyable columns\n"); err != nil {
			return fmt.Errorf("write data export skipped table: %w", err)
		}
		return nil
	}
	if table.HasIdentity {
		if _, err := fmt.Fprintf(writer, "SET IDENTITY_INSERT %s ON;\n", table.FQTN()); err != nil {
			return fmt.Errorf("write data export identity preamble: %w", err)
		}
	}
	return nil
}

func (c *copier) writeTableDataRow(writer io.Writer, table tableMeta, row exportRow) error {
	if len(row.values) != len(table.CopyColumns) {
		return fmt.Errorf("format data export row %s: got %d values for %d columns", table.FQTN(), len(row.values), len(table.CopyColumns))
	}
	literals := make([]string, len(table.CopyColumns))
	for i, col := range table.CopyColumns {
		replaced, err := c.replaceValue(table, col, row.values[i])
		if err != nil {
			return err
		}
		literal, err := sqlLiteral(replaced, col, nil)
		if err != nil {
			return fmt.Errorf("format data export value %s.%s: %w", table.FQTN(), col.Name, err)
		}
		literals[i] = literal
	}
	if _, err := fmt.Fprintf(writer, "INSERT INTO %s (%s) VALUES (%s);\n", table.FQTN(), joinQuotedColumns(table.CopyColumns), strings.Join(literals, ", ")); err != nil {
		return fmt.Errorf("write data export row %s: %w", table.FQTN(), err)
	}
	return nil
}

func writeTableDataFooter(writer io.Writer, table tableMeta, rowCount int) error {
	if table.HasIdentity {
		if _, err := fmt.Fprintf(writer, "SET IDENTITY_INSERT %s OFF;\n", table.FQTN()); err != nil {
			return fmt.Errorf("write data export identity epilogue: %w", err)
		}
	}
	if rowCount == 0 {
		if _, err := io.WriteString(writer, "-- no rows\n"); err != nil {
			return fmt.Errorf("write empty data export table: %w", err)
		}
	}
	return nil
}

func sqlLiteral(value any, col columnMeta, columnType *sql.ColumnType) (string, error) {
	if value == nil {
		return "NULL", nil
	}

	switch v := value.(type) {
	case string:
		if isUnicodeType(col.SystemTypeName) {
			return "N'" + escapeSQLString(v) + "'", nil
		}
		return "'" + escapeSQLString(v) + "'", nil
	case []byte:
		typeName := strings.ToLower(strings.TrimSpace(col.SystemTypeName))
		if typeName == "binary" || typeName == "varbinary" || typeName == "image" || typeName == "rowversion" || typeName == "timestamp" {
			return "0x" + strings.ToUpper(hex.EncodeToString(v)), nil
		}
		if isUnicodeType(typeName) {
			return "N'" + escapeSQLString(string(v)) + "'", nil
		}
		return "'" + escapeSQLString(string(v)) + "'", nil
	case bool:
		if v {
			return "1", nil
		}
		return "0", nil
	case time.Time:
		return "'" + v.UTC().Format("2006-01-02 15:04:05.9999999") + "'", nil
	case int:
		return strconv.Itoa(v), nil
	case int8:
		return strconv.FormatInt(int64(v), 10), nil
	case int16:
		return strconv.FormatInt(int64(v), 10), nil
	case int32:
		return strconv.FormatInt(int64(v), 10), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case uint:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	case float32:
		return formatFloatLiteral(float64(v), 32)
	case float64:
		return formatFloatLiteral(v, 64)
	default:
		if stringer, ok := value.(fmt.Stringer); ok {
			text := stringer.String()
			if isLikelyNumericType(col.SystemTypeName, columnType) {
				return text, nil
			}
			if isUnicodeType(col.SystemTypeName) {
				return "N'" + escapeSQLString(text) + "'", nil
			}
			return "'" + escapeSQLString(text) + "'", nil
		}
		text := fmt.Sprint(value)
		if isLikelyNumericType(col.SystemTypeName, columnType) {
			return text, nil
		}
		return "'" + escapeSQLString(text) + "'", nil
	}
}

func formatFloatLiteral(value float64, bitSize int) (string, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("unsupported floating-point value %v", value)
	}
	return strconv.FormatFloat(value, 'g', -1, bitSize), nil
}

func isUnicodeType(typeName string) bool {
	switch strings.ToLower(strings.TrimSpace(typeName)) {
	case "nchar", "nvarchar", "ntext", "xml":
		return true
	default:
		return false
	}
}

func isLikelyNumericType(typeName string, columnType *sql.ColumnType) bool {
	normalized := strings.ToLower(strings.TrimSpace(typeName))
	switch normalized {
	case "bigint", "bit", "decimal", "float", "int", "money", "numeric", "real", "smallint", "smallmoney", "tinyint":
		return true
	}
	if columnType == nil {
		return false
	}
	scanType := columnType.ScanType()
	if scanType == nil {
		return false
	}
	kind := scanType.Kind()
	return kind >= 2 && kind <= 10
}
