package copier

import (
	"fmt"
	"log"
	"strings"
	"time"
)

type closeable interface {
	Close() error
}

func closeAndLog(closer closeable, name string) {
	if err := closer.Close(); err != nil {
		log.Printf("close %s: %v", name, err)
	}
}

func selectTableCopySQL(table tableMeta) string {
	return fmt.Sprintf("SELECT %s FROM %s", joinQuotedColumns(table.CopyColumns), table.FQTN())
}

func selectTableExportSQL(table tableMeta, rowLimit int) string {
	selectList := joinQuotedColumns(table.CopyColumns)
	if rowLimit > 0 {
		selectList = fmt.Sprintf("TOP (%d) %s", rowLimit, selectList)
	}
	query := fmt.Sprintf("SELECT %s FROM %s", selectList, table.FQTN())
	orderBy := tableExportOrderBy(table)
	if orderBy == "" {
		return query
	}
	return query + " ORDER BY " + orderBy
}

func insertTableCopySQL(table tableMeta) string {
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table.FQTN(), joinQuotedColumns(table.CopyColumns), placeholders(len(table.CopyColumns)))
}

func parseList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		normalized := normalizeFilterName(part)
		if normalized != "" {
			result = append(result, normalized)
		}
	}
	return result
}

func normalizeFilterName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "[", "")
	value = strings.ReplaceAll(value, "]", "")
	return value
}

func (c *copier) shouldCopyTable(schemaName string, tableName string) bool {
	normalizedSchema := normalizeFilterName(schemaName)
	normalizedTable := normalizeFilterName(tableName)
	fullName := normalizedSchema + "." + normalizedTable

	if matchesAny(c.cfg.ExcludeSchemas, normalizedSchema) {
		return false
	}
	if matchesAny(c.cfg.ExcludeTables, normalizedTable) || matchesAny(c.cfg.ExcludeTables, fullName) {
		return false
	}
	if len(c.cfg.IncludeSchemas) > 0 && !matchesAny(c.cfg.IncludeSchemas, normalizedSchema) {
		return false
	}
	if len(c.cfg.IncludeTables) > 0 && !matchesAny(c.cfg.IncludeTables, normalizedTable) && !matchesAny(c.cfg.IncludeTables, fullName) {
		return false
	}
	return true
}

func matchesAny(values []string, candidate string) bool {
	for _, value := range values {
		if wildcardMatch(value, candidate) {
			return true
		}
	}
	return false
}

func wildcardMatch(pattern string, candidate string) bool {
	pattern = normalizeFilterName(pattern)
	candidate = normalizeFilterName(candidate)

	patternRunes := []rune(pattern)
	candidateRunes := []rune(candidate)
	dp := make([][]bool, len(patternRunes)+1)
	for i := range dp {
		dp[i] = make([]bool, len(candidateRunes)+1)
	}
	dp[0][0] = true

	for i := 1; i <= len(patternRunes); i++ {
		if isWildcardMany(patternRunes[i-1]) {
			dp[i][0] = dp[i-1][0]
		}
	}

	for i := 1; i <= len(patternRunes); i++ {
		for j := 1; j <= len(candidateRunes); j++ {
			switch {
			case isWildcardMany(patternRunes[i-1]):
				dp[i][j] = dp[i-1][j] || dp[i][j-1]
			case isWildcardOne(patternRunes[i-1]):
				dp[i][j] = dp[i-1][j-1]
			default:
				dp[i][j] = dp[i-1][j-1] && patternRunes[i-1] == candidateRunes[j-1]
			}
		}
	}

	return dp[len(patternRunes)][len(candidateRunes)]
}

func isWildcardMany(value rune) bool {
	return value == '*' || value == '%'
}

func isWildcardOne(value rune) bool {
	return value == '?' || value == '_'
}

func supportsBulkType(col columnMeta) bool {
	switch col.SystemTypeName {
	case "bigint", "binary", "bit", "char", "date", "datetime", "datetime2", "datetimeoffset", "decimal", "float", "geography", "geometry", "hierarchyid", "image", "int", "nchar", "ntext", "numeric", "nvarchar", "real", "smalldatetime", "smallint", "text", "time", "tinyint", "uniqueidentifier", "varbinary", "varchar":
		return true
	default:
		return false
	}
}

func normalizeValue(value any, col columnMeta) any {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case []byte:
		if col.SystemTypeName == "decimal" || col.SystemTypeName == "numeric" || col.SystemTypeName == "money" || col.SystemTypeName == "smallmoney" {
			return string(v)
		}
		// Parse datetime strings from []byte into time.Time for bulk copy compatibility
		if isDateTimeType(col.SystemTypeName) {
			if t, err := parseDateTimeBytes(v); err == nil {
				return t
			}
		}
		copyBytes := make([]byte, len(v))
		copy(copyBytes, v)
		return copyBytes
	case string:
		// Parse datetime strings into time.Time for bulk copy compatibility
		if isDateTimeType(col.SystemTypeName) {
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				return t
			}
			if t, err := time.Parse("2006-01-02T15:04:05Z", v); err == nil {
				return t
			}
			if t, err := time.Parse("2006-01-02 15:04:05Z", v); err == nil {
				return t
			}
			if t, err := time.Parse("2006-01-02T15:04:05", v); err == nil {
				return t
			}
			if t, err := time.Parse("2006-01-02 15:04:05", v); err == nil {
				return t
			}
			if t, err := time.Parse("2006-01-02", v); err == nil {
				return t
			}
		}
		return v
	default:
		return v
	}
}

func isDateTimeType(typeName string) bool {
	switch typeName {
	case "datetime", "datetime2", "smalldatetime", "date", "datetimeoffset":
		return true
	default:
		return false
	}
}

func parseDateTimeBytes(b []byte) (time.Time, error) {
	s := string(b)
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse datetime: %s", s)
}

func joinQuotedColumns(cols []columnMeta) string {
	names := make([]string, 0, len(cols))
	for _, col := range cols {
		names = append(names, quoteIdent(col.Name))
	}
	return strings.Join(names, ", ")
}

func tableExportOrderBy(table tableMeta) string {
	if table.PrimaryKey != nil && len(table.PrimaryKey.Columns) > 0 {
		return joinKeyColumns(table.PrimaryKey.Columns)
	}
	if len(table.CopyColumns) == 0 {
		return ""
	}
	return joinQuotedColumns(table.CopyColumns)
}

func columnNames(cols []columnMeta) []string {
	names := make([]string, 0, len(cols))
	for _, col := range cols {
		names = append(names, col.Name)
	}
	return names
}

func joinQuotedNames(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, quoteIdent(name))
	}
	return strings.Join(quoted, ", ")
}

func joinKeyColumns(cols []keyColumn) string {
	parts := make([]string, 0, len(cols))
	for _, col := range cols {
		dir := "ASC"
		if col.Desc {
			dir = "DESC"
		}
		parts = append(parts, quoteIdent(col.Name)+" "+dir)
	}
	return strings.Join(parts, ", ")
}

func placeholders(count int) string {
	parts := make([]string, count)
	for i := range count {
		parts[i] = fmt.Sprintf("@p%d", i+1)
	}
	return strings.Join(parts, ", ")
}

func quoteIdent(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}

func escapeSQLString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func lengthSuffix(length int) string {
	if length == -1 {
		return "(max)"
	}
	return fmt.Sprintf("(%d)", length)
}

func normalizeReferentialAction(action string) string {
	switch strings.ToUpper(strings.TrimSpace(action)) {
	case "CASCADE":
		return "CASCADE"
	case "SET_NULL":
		return "SET NULL"
	case "SET_DEFAULT":
		return "SET DEFAULT"
	default:
		return ""
	}
}

func collationAllowed(typeName string) bool {
	switch typeName {
	case "char", "varchar", "text", "nchar", "nvarchar", "ntext":
		return true
	default:
		return false
	}
}
