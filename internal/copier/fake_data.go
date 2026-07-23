package copier

import (
	"fmt"
	"log"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/brianvoe/gofakeit/v7"
)

type dataFaker struct {
	fullNameRules  map[string]fakeDataRule
	tableNameRules map[string]fakeDataRule
	columnRules    map[string]fakeDataRule
	regexRules     []fakeDataRule
}

type fakeDataRule struct {
	selector     string
	functionName string
	lookupName   string
	info         gofakeit.Info
	params       gofakeit.MapParams
	regex        *regexp.Regexp
	requiresUnique bool
}

func newDataFaker(configured map[string]string, uniqueSelectors map[string]bool) (*dataFaker, error) {
	if len(configured) == 0 {
		return nil, nil
	}

	faker := &dataFaker{
		fullNameRules:  make(map[string]fakeDataRule),
		tableNameRules: make(map[string]fakeDataRule),
		columnRules:    make(map[string]fakeDataRule),
	}

	selectors := make([]string, 0, len(configured))
	for selector := range configured {
		selectors = append(selectors, selector)
	}
	slices.Sort(selectors)

	for _, selector := range selectors {
		rule, target, err := compileFakeDataRule(selector, configured[selector])
		if err != nil {
			return nil, err
		}
		// Set the requiresUnique flag based on uniqueSelectors or if column has unique constraint
		if uniqueSelectors != nil && uniqueSelectors[normalizeFilterName(selector)] {
			rule.requiresUnique = true
		}
		switch target {
		case "full":
			faker.fullNameRules[normalizeFilterName(selector)] = rule
		case "table":
			faker.tableNameRules[normalizeFilterName(selector)] = rule
		case "column":
			faker.columnRules[normalizeFilterName(selector)] = rule
		default:
			faker.regexRules = append(faker.regexRules, rule)
		}
	}

	return faker, nil
}

func compileFakeDataRule(selector string, functionConfig string) (fakeDataRule, string, error) {
	selector = strings.TrimSpace(selector)
	functionConfig = strings.TrimSpace(functionConfig)
	if selector == "" {
		return fakeDataRule{}, "", fmt.Errorf("fake-data selector cannot be empty")
	}
	if functionConfig == "" {
		return fakeDataRule{}, "", fmt.Errorf("fake-data function for %q cannot be empty", selector)
	}

	functionName, rawParams := parseFakeFunctionConfig(functionConfig)
	lookupName, info := resolveFakeFunction(functionName)
	if info == nil {
		return fakeDataRule{}, "", fmt.Errorf("fake-data %q uses unknown gofakeit function %q", selector, functionName)
	}
	params, err := buildFakeFunctionParams(selector, functionName, info, rawParams)
	if err != nil {
		return fakeDataRule{}, "", err
	}
	sample, err := info.Generate(gofakeit.New(1), cloneFakeParams(params), info)
	if err != nil {
		return fakeDataRule{}, "", fmt.Errorf("fake-data %q could not initialize gofakeit function %q: %w", selector, functionName, err)
	}
	if !supportedFakeValue(sample) {
		return fakeDataRule{}, "", fmt.Errorf("fake-data %q uses unsupported gofakeit function %q: output type %T is not supported", selector, functionName, sample)
	}

	rule := fakeDataRule{
		selector:     selector,
		functionName: functionName,
		lookupName:   lookupName,
		info:         *info,
		params:       params,
	}

	if target, normalized, ok := exactFakeDataTarget(selector); ok {
		rule.selector = normalized
		return rule, target, nil
	}

	re, err := regexp.Compile("(?i)^" + normalizeRegexSelector(selector) + "$")
	if err != nil {
		return fakeDataRule{}, "", fmt.Errorf("fake-data %q has invalid regex: %w", selector, err)
	}
	rule.regex = re
	return rule, "regex", nil
}

func exactFakeDataTarget(selector string) (string, string, bool) {
	normalized := normalizeFilterName(selector)
	if normalized == "" {
		return "", "", false
	}
	parts := strings.Split(normalized, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return "", "", false
	}
	for _, part := range parts {
		if part == "" {
			return "", "", false
		}
		for _, r := range part {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
				return "", "", false
			}
		}
	}
	if len(parts) == 3 {
		return "full", normalized, true
	}
	if len(parts) == 2 {
		return "table", normalized, true
	}
	return "column", normalized, true
}

func normalizeRegexSelector(selector string) string {
	return normalizeFilterName(selector)
}

func normalizeFakeFunctionName(name string) string {
	var builder strings.Builder
	for _, r := range strings.TrimSpace(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(unicode.ToLower(r))
		}
	}
	return builder.String()
}

func parseFakeFunctionConfig(value string) (string, []string) {
	parts := strings.Split(value, ";")
	functionName := strings.TrimSpace(parts[0])
	if len(parts) == 1 {
		return functionName, nil
	}
	params := make([]string, 0, len(parts)-1)
	for _, part := range parts[1:] {
		params = append(params, strings.TrimSpace(part))
	}
	return functionName, params
}

func buildFakeFunctionParams(selector string, functionName string, info *gofakeit.Info, values []string) (gofakeit.MapParams, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if len(info.Params) == 0 {
		return nil, fmt.Errorf("fake-data %q passes parameters to gofakeit function %q, but it does not accept any", selector, functionName)
	}

	params := gofakeit.MapParams{}
	valueIndex := 0
	for paramIndex, param := range info.Params {
		if valueIndex >= len(values) {
			break
		}

		remainingParams := len(info.Params) - paramIndex
		remainingValues := len(values) - valueIndex
		assignCount := 1
		if strings.HasPrefix(param.Type, "[]") {
			assignCount = max(remainingValues-(remainingParams-1), 1)
		}

		for i := 0; i < assignCount && valueIndex < len(values); i++ {
			params.Add(param.Field, values[valueIndex])
			valueIndex++
		}
	}

	if valueIndex != len(values) {
		return nil, fmt.Errorf("fake-data %q passes %d parameters to gofakeit function %q, but it only accepts %d", selector, len(values), functionName, len(info.Params))
	}

	return params, nil
}

func cloneFakeParams(params gofakeit.MapParams) *gofakeit.MapParams {
	if len(params) == 0 {
		return nil
	}
	cloned := gofakeit.MapParams{}
	for key, values := range params {
		clonedValues := append([]string(nil), values...)
		cloned[key] = clonedValues
	}
	return &cloned
}

func resolveFakeFunction(name string) (string, *gofakeit.Info) {
	normalizedName := normalizeFakeFunctionName(name)
	if normalizedName != "" {
		if info := gofakeit.GetFuncLookup(normalizedName); info != nil {
			return normalizedName, info
		}
	}

	if categoryName, functionName, ok := strings.Cut(strings.TrimSpace(name), "."); ok {
		normalizedCategory := normalizeFakeFunctionName(categoryName)
		normalizedFunction := normalizeFakeFunctionName(functionName)
		if normalizedCategory != "" && normalizedFunction != "" {
			if info := gofakeit.GetFuncLookup(normalizedFunction); info != nil {
				if slices.Contains(fakeFunctionCategoryCandidates(normalizedCategory), normalizeFakeFunctionName(info.Category)) {
					return normalizedFunction, info
				}
			}
		}
	}
	return "", nil
}

func fakeFunctionCategoryCandidates(category string) []string {
	aliases := map[string][]string{
		"words":    {"text", "word", "words"},
		"foods":    {"food"},
		"colors":   {"color"},
		"images":   {"image"},
		"language": {"language", "languages"},
		"datetime": {"datetime"},
	}
	if candidates, ok := aliases[category]; ok {
		return candidates
	}
	return []string{category}
}

func supportedFakeValue(value any) bool {
	if value == nil {
		return true
	}
	switch value.(type) {
	case string, []byte, bool, time.Time,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return true
	default:
		return false
	}
}

func (f *dataFaker) fakeValue(faker *gofakeit.Faker, table tableMeta, col columnMeta) (any, bool, error) {
	if f == nil {
		return nil, false, nil
	}
	if faker == nil {
		faker = gofakeit.GlobalFaker
	}
	rule, ok := f.matchRule(table, col)
	if !ok {
		return nil, false, nil
	}
	value, err := rule.info.Generate(faker, cloneFakeParams(rule.params), &rule.info)
	return value, true, err
}

func (f *dataFaker) matchRule(table tableMeta, col columnMeta) (fakeDataRule, bool) {
	if f == nil {
		return fakeDataRule{}, false
	}

	fullName := normalizeFilterName(table.Schema + "." + table.Name + "." + col.Name)
	tableName := normalizeFilterName(table.Name + "." + col.Name)
	columnName := normalizeFilterName(col.Name)

	if rule, ok := f.fullNameRules[fullName]; ok {
		// Enforce unique if column has unique constraint/index
		if columnHasUniqueConstraint(table, col) {
			rule.requiresUnique = true
		}
		return rule, true
	}
	if rule, ok := f.tableNameRules[tableName]; ok {
		if columnHasUniqueConstraint(table, col) {
			rule.requiresUnique = true
		}
		return rule, true
	}
	if rule, ok := f.columnRules[columnName]; ok {
		if columnHasUniqueConstraint(table, col) {
			rule.requiresUnique = true
		}
		return rule, true
	}
	for _, rule := range f.regexRules {
		if rule.regex.MatchString(fullName) || rule.regex.MatchString(tableName) || rule.regex.MatchString(columnName) {
			if columnHasUniqueConstraint(table, col) {
				rule.requiresUnique = true
			}
			return rule, true
		}
	}

	return fakeDataRule{}, false
}

func columnHasUniqueConstraint(table tableMeta, col columnMeta) bool {
	// Check primary key
	if table.PrimaryKey != nil {
		for _, keycol := range table.PrimaryKey.Columns {
			if keycol.Name == col.Name {
				return true
			}
		}
	}
	// Check unique indexes
	for _, idx := range table.Indexes {
		if idx.Unique {
			for _, keycol := range idx.KeyColumns {
				if keycol.Name == col.Name {
					return true
				}
			}
		}
	}
	return false
}

func (c *copier) replaceValue(table tableMeta, col columnMeta, value any) (any, error) {
	if !c.cfg.EnableFakeData || c.dataFaker == nil {
		return normalizeValue(value, col), nil
	}
	
	rule, ruleFound := c.dataFaker.matchRule(table, col)
	if !ruleFound {
		return normalizeValue(value, col), nil
	}

	// Generate a unique value if required
	if rule.requiresUnique {
		columnKey := normalizeFilterName(table.Schema + "." + table.Name + "." + col.Name)
		for attempts := 0; attempts < 1000; attempts++ {
			replacement, ok, err := c.dataFaker.fakeValue(c.faker, table, col)
			if err != nil {
				return nil, fmt.Errorf("generate fake value for %s.%s: %w", table.FQTN(), col.Name, err)
			}
			if !ok {
				return normalizeValue(value, col), nil
			}
			
			finalValue := normalizeValue(truncateToColumnLength(replacement, col, table), col)
			
			// Check if value already exists
			c.uniqueValuesMu.Lock()
			if c.uniqueValues[columnKey] == nil {
				c.uniqueValues[columnKey] = make(map[any]bool)
			}
			if !c.uniqueValues[columnKey][finalValue] {
				c.uniqueValues[columnKey][finalValue] = true
				c.uniqueValuesMu.Unlock()
				return finalValue, nil
			}
			c.uniqueValuesMu.Unlock()
		}
		return nil, fmt.Errorf("could not generate unique value for %s.%s after 1000 attempts", table.FQTN(), col.Name)
	}

	// Non-unique value generation
	replacement, ok, err := c.dataFaker.fakeValue(c.faker, table, col)
	if err != nil {
		return nil, fmt.Errorf("generate fake value for %s.%s: %w", table.FQTN(), col.Name, err)
	}
	if !ok {
		return normalizeValue(value, col), nil
	}
	return normalizeValue(truncateToColumnLength(replacement, col, table), col), nil
}

func truncateToColumnLength(value any, col columnMeta, table tableMeta) any {
	if col.MaxLength < 0 || col.MaxLength == 0 {
		return value
	}
	switch v := value.(type) {
	case string:
		limit := charLimit(col)
		if limit <= 0 {
			return value
		}
		var truncated string
		if col.SystemTypeName == "nvarchar" || col.SystemTypeName == "nchar" {
			truncated = truncateUTF16Units(v, limit)
		} else {
			truncated = truncateUTF8Bytes(v, limit)
		}
		if truncated != v {
			log.Printf("truncating generated value for %s.%s to fit %s", table.FQTN(), col.Name, col.SystemTypeName)
			return truncated
		}
	case []byte:
		if len(v) > col.MaxLength {
			log.Printf("truncating %s.%s from %d to %d bytes to fit %s", table.FQTN(), col.Name, len(v), col.MaxLength, col.SystemTypeName)
			result := make([]byte, col.MaxLength)
			copy(result, v[:col.MaxLength])
			return result
		}
	}
	return value
}

func charLimit(col columnMeta) int {
	switch col.SystemTypeName {
	case "nvarchar", "nchar":
		return col.MaxLength / 2
	case "varchar", "char":
		return col.MaxLength
	default:
		return 0
	}
}

func truncateUTF16Units(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	units := 0
	for index, r := range value {
		runeUnits := utf16.RuneLen(r)
		if runeUnits < 0 {
			runeUnits = 1
		}
		if units+runeUnits > limit {
			return value[:index]
		}
		units += runeUnits
	}
	return value
}

func truncateUTF8Bytes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

// matchingOutputTypes returns gofakeit output types compatible with the given
// SQL Server data type.  Returns nil when no type-based filtering should be
// applied (unknown data types).
func matchingOutputTypes(dataType string) []string {
	dt := strings.ToLower(dataType)

	// String-like types.
	if strings.Contains(dt, "char") || strings.Contains(dt, "text") ||
		dt == "sysname" || dt == "xml" || dt == "sql_variant" ||
		dt == "uniqueidentifier" {
		return []string{"string", "[]string", "[]byte", "byte", "net.IP"}
	}

	// Integer types.
	if dt == "int" || dt == "bigint" || dt == "smallint" || dt == "tinyint" {
		return []string{"int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64",
			"[]int", "[]uint"}
	}

	// Float / decimal / money types.
	if strings.Contains(dt, "float") ||
		strings.Contains(dt, "decimal") || strings.Contains(dt, "numeric") ||
		strings.Contains(dt, "real") || strings.Contains(dt, "money") {
		return []string{"float32", "float64",
			"int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64"}
	}

	// Boolean.
	if dt == "bit" {
		return []string{"bool"}
	}

	// Date / time types.
	if strings.Contains(dt, "date") || strings.Contains(dt, "datetime") ||
		strings.Contains(dt, "timestamp") || dt == "time" {
		return []string{"time", "time.Time"}
	}

	// Binary types.
	if dt == "binary" || dt == "varbinary" || dt == "image" ||
		dt == "timestamp" || dt == "rowversion" {
		return []string{"[]byte"}
	}

	return nil
}
