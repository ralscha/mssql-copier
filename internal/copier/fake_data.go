package copier

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

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
}

func newDataFaker(configured map[string]string) (*dataFaker, error) {
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

	fullName := normalizeFilterName(table.Schema + "." + table.Name + "." + col.Name)
	tableName := normalizeFilterName(table.Name + "." + col.Name)
	columnName := normalizeFilterName(col.Name)

	if rule, ok := f.fullNameRules[fullName]; ok {
		value, err := rule.info.Generate(faker, cloneFakeParams(rule.params), &rule.info)
		return value, true, err
	}
	if rule, ok := f.tableNameRules[tableName]; ok {
		value, err := rule.info.Generate(faker, cloneFakeParams(rule.params), &rule.info)
		return value, true, err
	}
	if rule, ok := f.columnRules[columnName]; ok {
		value, err := rule.info.Generate(faker, cloneFakeParams(rule.params), &rule.info)
		return value, true, err
	}
	for _, rule := range f.regexRules {
		if rule.regex.MatchString(fullName) || rule.regex.MatchString(tableName) || rule.regex.MatchString(columnName) {
			value, err := rule.info.Generate(faker, cloneFakeParams(rule.params), &rule.info)
			return value, true, err
		}
	}

	return nil, false, nil
}

func (c *copier) replaceValue(table tableMeta, col columnMeta, value any) (any, error) {
	if c.dataFaker == nil {
		return normalizeValue(value, col), nil
	}
	replacement, ok, err := c.dataFaker.fakeValue(c.faker, table, col)
	if err != nil {
		return nil, fmt.Errorf("generate fake value for %s.%s: %w", table.FQTN(), col.Name, err)
	}
	if !ok {
		return normalizeValue(value, col), nil
	}
	return normalizeValue(replacement, col), nil
}
