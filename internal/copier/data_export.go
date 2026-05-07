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

	var builder strings.Builder
	builder.WriteString("-- mssql-copier data export\n")
	builder.WriteString("\n")
	for _, table := range tables {
		builder.WriteString("ALTER TABLE ")
		builder.WriteString(table.FQTN())
		builder.WriteString(" NOCHECK CONSTRAINT ALL;\n")
	}

	for _, table := range tables {
		section, err := c.buildTableDataSection(ctx, table)
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

func (c *copier) buildTableDataSection(ctx context.Context, table tableMeta) (string, error) {
	if len(table.CopyColumns) == 0 {
		return fmt.Sprintf("-- table %s\n-- skipped: no copyable columns\n", table.FQTN()), nil
	}

	rows, err := c.sourceDB.QueryContext(ctx, selectTableExportSQL(table))
	if err != nil {
		return "", fmt.Errorf("query data export %s: %w", table.FQTN(), err)
	}
	defer closeAndLog(rows, "data export rows")

	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return "", fmt.Errorf("describe data export columns %s: %w", table.FQTN(), err)
	}

	values := make([]any, len(table.CopyColumns))
	scanDest := make([]any, len(table.CopyColumns))
	for i := range values {
		scanDest[i] = &values[i]
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

	for rows.Next() {
		if err := rows.Scan(scanDest...); err != nil {
			return "", fmt.Errorf("scan data export row %s: %w", table.FQTN(), err)
		}

		literals := make([]string, len(table.CopyColumns))
		for i, col := range table.CopyColumns {
			literal, err := sqlLiteral(normalizeValue(values[i], col), col, columnTypes[i])
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
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate data export rows %s: %w", table.FQTN(), err)
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
