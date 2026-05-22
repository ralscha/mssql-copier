package copier

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
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

func (c *copier) writeDataExportFile(ctx context.Context) error {
	script, err := c.buildDataExportSQL(ctx)
	if err != nil {
		return err
	}

	dir := filepath.Dir(c.cfg.ExportDataFile)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create data export output directory: %w", err)
		}
	}

	if err := os.WriteFile(c.cfg.ExportDataFile, []byte(script), 0o600); err != nil {
		return fmt.Errorf("write data export file: %w", err)
	}
	return nil
}

func (c *copier) buildDataExportSQL(ctx context.Context) (string, error) {
	if len(c.tables) == 0 {
		return "-- mssql-copier data export\n", nil
	}

	tables := sortedTablesForDataExport(c.tables)
	exportRowsByTable, err := c.buildExportRowsByTable(ctx, tables)
	if err != nil {
		return "", err
	}

	var builder strings.Builder
	builder.WriteString("-- mssql-copier data export\n")
	builder.WriteString("\n")
	for _, table := range tables {
		builder.WriteString("ALTER TABLE ")
		builder.WriteString(table.FQTN())
		builder.WriteString(" NOCHECK CONSTRAINT ALL;\n")
	}

	for _, table := range tables {
		section, err := c.buildTableDataSection(table, exportRowsByTable[normalizedTableKey(table)])
		if err != nil {
			return "", err
		}
		if section == "" {
			continue
		}
		builder.WriteString("\n")
		builder.WriteString(section)
	}

	builder.WriteString("\n")
	for _, table := range tables {
		builder.WriteString("ALTER TABLE ")
		builder.WriteString(table.FQTN())
		builder.WriteString(" WITH CHECK CHECK CONSTRAINT ALL;\n")
	}

	return builder.String(), nil
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

	var result []exportRow
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

func (c *copier) buildTableDataSection(table tableMeta, rowsData []exportRow) (string, error) {
	if len(table.CopyColumns) == 0 {
		return fmt.Sprintf("-- table %s\n-- skipped: no copyable columns\n", table.FQTN()), nil
	}

	var builder strings.Builder
	builder.WriteString("-- table ")
	builder.WriteString(table.FQTN())
	builder.WriteString("\n")

	rowCount := 0
	if table.HasIdentity {
		builder.WriteString("SET IDENTITY_INSERT ")
		builder.WriteString(table.FQTN())
		builder.WriteString(" ON;\n")
	}

	for _, row := range rowsData {
		literals := make([]string, len(table.CopyColumns))
		for i, col := range table.CopyColumns {
			replaced, err := c.replaceValue(table, col, row.values[i])
			if err != nil {
				return "", err
			}
			literal, err := sqlLiteral(replaced, col, nil)
			if err != nil {
				return "", fmt.Errorf("format data export value %s.%s: %w", table.FQTN(), col.Name, err)
			}
			literals[i] = literal
		}

		builder.WriteString("INSERT INTO ")
		builder.WriteString(table.FQTN())
		builder.WriteString(" (")
		builder.WriteString(joinQuotedColumns(table.CopyColumns))
		builder.WriteString(") VALUES (")
		builder.WriteString(strings.Join(literals, ", "))
		builder.WriteString(");\n")
		rowCount++
	}

	if table.HasIdentity {
		builder.WriteString("SET IDENTITY_INSERT ")
		builder.WriteString(table.FQTN())
		builder.WriteString(" OFF;\n")
	}
	if rowCount == 0 {
		builder.WriteString("-- no rows\n")
	}
	return builder.String(), nil
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
