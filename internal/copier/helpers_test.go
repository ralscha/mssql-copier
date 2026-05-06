package copier

import "testing"

func TestWildcardMatch(t *testing.T) {
	tests := []struct {
		name      string
		pattern   string
		candidate string
		want      bool
	}{
		{name: "exact match", pattern: "sales.orders", candidate: "sales.orders", want: true},
		{name: "star wildcard", pattern: "sales.*", candidate: "sales.orders", want: true},
		{name: "single char wildcard", pattern: "dbo.ord?rs", candidate: "dbo.orders", want: true},
		{name: "sql like percent", pattern: "sales.%", candidate: "sales.orders", want: true},
		{name: "sql like underscore", pattern: "dbo.audit_2026", candidate: "dbo.auditX2026", want: true},
		{name: "bracketed identifier normalization", pattern: "[sales].[orders]", candidate: "sales.orders", want: true},
		{name: "non-match", pattern: "sales.*", candidate: "dbo.orders", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := wildcardMatch(test.pattern, test.candidate)
			if got != test.want {
				t.Fatalf("wildcardMatch(%q, %q) = %v, want %v", test.pattern, test.candidate, got, test.want)
			}
		})
	}
}

func TestIsWildcardMany(t *testing.T) {
	if !isWildcardMany('*') {
		t.Fatal("expected * to be wildcard many")
	}
	if !isWildcardMany('%') {
		t.Fatal("expected % to be wildcard many")
	}
	if isWildcardMany('?') {
		t.Fatal("expected ? not to be wildcard many")
	}
	if isWildcardMany('a') {
		t.Fatal("expected 'a' not to be wildcard many")
	}
}

func TestIsWildcardOne(t *testing.T) {
	if !isWildcardOne('?') {
		t.Fatal("expected ? to be wildcard one")
	}
	if !isWildcardOne('_') {
		t.Fatal("expected _ to be wildcard one")
	}
	if isWildcardOne('*') {
		t.Fatal("expected * not to be wildcard one")
	}
	if isWildcardOne('a') {
		t.Fatal("expected 'a' not to be wildcard one")
	}
}

func TestParseList(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "empty", input: "", want: nil},
		{name: "whitespace only", input: "  ,  , ", want: nil},
		{name: "single", input: "dbo", want: []string{"dbo"}},
		{name: "multiple", input: "dbo,sales,hr", want: []string{"dbo", "sales", "hr"}},
		{name: "with whitespace", input: " dbo , sales ", want: []string{"dbo", "sales"}},
		{name: "bracketed", input: "[dbo],[sales]", want: []string{"dbo", "sales"}},
		{name: "mixed case", input: "Dbo,Sales", want: []string{"dbo", "sales"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := parseList(test.input)
			if len(got) != len(test.want) {
				t.Fatalf("parseList(%q) len = %d, want %d: %v", test.input, len(got), len(test.want), got)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("parseList(%q)[%d] = %q, want %q", test.input, i, got[i], test.want[i])
				}
			}
		})
	}
}

func TestNormalizeFilterName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"  DBO  ", "dbo"},
		{"[Sales]", "sales"},
		{"[dbo].[orders]", "dbo.orders"},
		{"SALES", "sales"},
	}
	for _, test := range tests {
		got := normalizeFilterName(test.input)
		if got != test.want {
			t.Fatalf("normalizeFilterName(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestMatchesAny(t *testing.T) {
	values := []string{"sales*", "hr.%"}
	if !matchesAny(values, "sales.orders") {
		t.Fatal("expected sales.orders to match sales*")
	}
	if !matchesAny(values, "hr.employees") {
		t.Fatal("expected hr.employees to match hr.%")
	}
	if matchesAny(values, "dbo.orders") {
		t.Fatal("expected dbo.orders not to match")
	}
	if matchesAny(nil, "anything") {
		t.Fatal("expected nil values to match nothing")
	}
}

func TestShouldCopyTable(t *testing.T) {
	// No filters: copy everything
	c := &copier{cfg: config{}}
	if !c.shouldCopyTable("dbo", "users") {
		t.Fatal("expected dbo.users to be included with no filters")
	}

	// Include schemas only
	c = &copier{cfg: config{IncludeSchemas: []string{"sales", "hr"}}}
	if !c.shouldCopyTable("sales", "orders") {
		t.Fatal("expected sales.orders to be included")
	}
	if c.shouldCopyTable("dbo", "orders") {
		t.Fatal("expected dbo.orders to be excluded")
	}

	// Exclude schemas
	c = &copier{cfg: config{ExcludeSchemas: []string{"log"}}}
	if c.shouldCopyTable("log", "events") {
		t.Fatal("expected log.events to be excluded")
	}
	if !c.shouldCopyTable("dbo", "users") {
		t.Fatal("expected dbo.users to be included")
	}

	// Include tables by full name
	c = &copier{cfg: config{IncludeTables: []string{"dbo.users", "dbo.orders"}}}
	if !c.shouldCopyTable("dbo", "users") {
		t.Fatal("expected dbo.users to be included")
	}
	if c.shouldCopyTable("sales", "orders") {
		t.Fatal("expected sales.orders to be excluded")
	}

	// Exclude tables by full name
	c = &copier{cfg: config{ExcludeTables: []string{"dbo.temp*"}}}
	if c.shouldCopyTable("dbo", "temp_2026") {
		t.Fatal("expected dbo.temp_2026 to be excluded")
	}
	if !c.shouldCopyTable("dbo", "users") {
		t.Fatal("expected dbo.users to be included")
	}

	// Include tables by short name
	c = &copier{cfg: config{IncludeTables: []string{"users"}}}
	if !c.shouldCopyTable("dbo", "users") {
		t.Fatal("expected dbo.users to be included by short name")
	}
	if !c.shouldCopyTable("sales", "users") {
		t.Fatal("expected sales.users to be included by short name")
	}
	if c.shouldCopyTable("dbo", "orders") {
		t.Fatal("expected dbo.orders to be excluded")
	}
}

func TestShouldCopyTableWithWildcards(t *testing.T) {
	c := &copier{cfg: config{
		IncludeSchemas: []string{"sales*"},
		ExcludeTables:  []string{"*.audit_%"},
	}}

	if !c.shouldCopyTable("sales", "orders") {
		t.Fatal("expected sales.orders to be included")
	}
	if c.shouldCopyTable("sales", "audit_2026") {
		t.Fatal("expected sales.audit_2026 to be excluded by wildcard")
	}
	if c.shouldCopyTable("dbo", "orders") {
		t.Fatal("expected dbo.orders to be excluded by include-schemas filter")
	}
}

func TestSupportsBulkType(t *testing.T) {
	bulkTypes := []string{"bigint", "binary", "bit", "char", "date", "datetime", "datetime2", "datetimeoffset", "decimal", "float", "geography", "geometry", "hierarchyid", "image", "int", "nchar", "ntext", "numeric", "nvarchar", "real", "smalldatetime", "smallint", "text", "time", "tinyint", "uniqueidentifier", "varbinary", "varchar"}
	for _, typeName := range bulkTypes {
		col := columnMeta{SystemTypeName: typeName}
		if !supportsBulkType(col) {
			t.Fatalf("expected %s to support bulk", typeName)
		}
	}

	unsupported := []string{"xml", "sql_variant", "money", "smallmoney"}
	for _, typeName := range unsupported {
		col := columnMeta{SystemTypeName: typeName}
		if supportsBulkType(col) {
			t.Fatalf("expected %s NOT to support bulk", typeName)
		}
	}
}

func TestNormalizeValue(t *testing.T) {
	// nil returns nil
	if normalizeValue(nil, columnMeta{}) != nil {
		t.Fatal("expected nil for nil input")
	}

	// decimal []byte -> string
	col := columnMeta{SystemTypeName: "decimal"}
	got := normalizeValue([]byte("123.45"), col)
	if got != "123.45" {
		t.Fatalf("expected decimal []byte to become string, got %T: %v", got, got)
	}

	// numeric []byte -> string
	col = columnMeta{SystemTypeName: "numeric"}
	got = normalizeValue([]byte("99.99"), col)
	if got != "99.99" {
		t.Fatalf("expected numeric []byte to become string, got %T: %v", got, got)
	}

	// money []byte -> string
	col = columnMeta{SystemTypeName: "money"}
	got = normalizeValue([]byte("100.00"), col)
	if got != "100.00" {
		t.Fatalf("expected money []byte to become string, got %T: %v", got, got)
	}

	// smallmoney []byte -> string
	col = columnMeta{SystemTypeName: "smallmoney"}
	got = normalizeValue([]byte("10.50"), col)
	if got != "10.50" {
		t.Fatalf("expected smallmoney []byte to become string, got %T: %v", got, got)
	}

	// non-money []byte -> copied []byte
	col = columnMeta{SystemTypeName: "varbinary"}
	input := []byte{0x01, 0x02, 0x03}
	got = normalizeValue(input, col)
	gotBytes, ok := got.([]byte)
	if !ok {
		t.Fatalf("expected varbinary []byte to remain []byte, got %T", got)
	}
	if &gotBytes[0] == &input[0] {
		t.Fatal("expected varbinary []byte to be copied, not same backing array")
	}

	// string -> string
	got = normalizeValue("hello", columnMeta{SystemTypeName: "varchar"})
	if got != "hello" {
		t.Fatalf("expected string to pass through, got %v", got)
	}

	// int64 -> int64
	got = normalizeValue(int64(42), columnMeta{SystemTypeName: "int"})
	if got != int64(42) {
		t.Fatalf("expected int64 to pass through, got %v", got)
	}
}

func TestJoinQuotedColumns(t *testing.T) {
	cols := []columnMeta{
		{Name: "id"},
		{Name: "name"},
		{Name: "created_at"},
	}
	got := joinQuotedColumns(cols)
	want := "[id], [name], [created_at]"
	if got != want {
		t.Fatalf("joinQuotedColumns = %q, want %q", got, want)
	}
}

func TestJoinQuotedColumnsEmpty(t *testing.T) {
	got := joinQuotedColumns(nil)
	if got != "" {
		t.Fatalf("expected empty string for nil, got %q", got)
	}
}

func TestColumnNames(t *testing.T) {
	cols := []columnMeta{
		{Name: "id"},
		{Name: "name"},
	}
	got := columnNames(cols)
	if len(got) != 2 || got[0] != "id" || got[1] != "name" {
		t.Fatalf("columnNames = %v, want [id name]", got)
	}
}

func TestJoinQuotedNames(t *testing.T) {
	names := []string{"id", "name", "data"}
	got := joinQuotedNames(names)
	want := "[id], [name], [data]"
	if got != want {
		t.Fatalf("joinQuotedNames = %q, want %q", got, want)
	}
}

func TestJoinQuotedNamesEmpty(t *testing.T) {
	got := joinQuotedNames(nil)
	if got != "" {
		t.Fatalf("expected empty string for nil, got %q", got)
	}
}

func TestJoinKeyColumns(t *testing.T) {
	cols := []keyColumn{
		{Name: "id", Desc: false},
		{Name: "created_at", Desc: true},
	}
	got := joinKeyColumns(cols)
	want := "[id] ASC, [created_at] DESC"
	if got != want {
		t.Fatalf("joinKeyColumns = %q, want %q", got, want)
	}
}

func TestJoinKeyColumnsEmpty(t *testing.T) {
	got := joinKeyColumns(nil)
	if got != "" {
		t.Fatalf("expected empty string for nil, got %q", got)
	}
}

func TestPlaceholders(t *testing.T) {
	got := placeholders(4)
	want := "@p1, @p2, @p3, @p4"
	if got != want {
		t.Fatalf("placeholders(4) = %q, want %q", got, want)
	}
}

func TestPlaceholdersZero(t *testing.T) {
	got := placeholders(0)
	if got != "" {
		t.Fatalf("placeholders(0) = %q, want empty", got)
	}
}

func TestQuoteIdent(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"dbo", "[dbo]"},
		{"table name", "[table name]"},
		{"close]bracket", "[close]]bracket]"},
		{"", "[]"},
	}
	for _, test := range tests {
		got := quoteIdent(test.input)
		if got != test.want {
			t.Fatalf("quoteIdent(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestEscapeSQLString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"it's", "it''s"},
		{"a'b'c", "a''b''c"},
		{"", ""},
	}
	for _, test := range tests {
		got := escapeSQLString(test.input)
		if got != test.want {
			t.Fatalf("escapeSQLString(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestLengthSuffix(t *testing.T) {
	if got := lengthSuffix(-1); got != "(max)" {
		t.Fatalf("lengthSuffix(-1) = %q, want (max)", got)
	}
	if got := lengthSuffix(50); got != "(50)" {
		t.Fatalf("lengthSuffix(50) = %q, want (50)", got)
	}
	if got := lengthSuffix(0); got != "(0)" {
		t.Fatalf("lengthSuffix(0) = %q, want (0)", got)
	}
}

func TestNormalizeReferentialAction(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"CASCADE", "CASCADE"},
		{"cascade", "CASCADE"},
		{" Cascade ", "CASCADE"},
		{"SET_NULL", "SET NULL"},
		{"set_null", "SET NULL"},
		{"SET_DEFAULT", "SET DEFAULT"},
		{"set_default", "SET DEFAULT"},
		{"NO_ACTION", ""},
		{"no_action", ""},
		{"", ""},
		{"unknown", ""},
	}
	for _, test := range tests {
		got := normalizeReferentialAction(test.input)
		if got != test.want {
			t.Fatalf("normalizeReferentialAction(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestCollationAllowed(t *testing.T) {
	allowed := []string{"char", "varchar", "text", "nchar", "nvarchar", "ntext"}
	for _, typeName := range allowed {
		if !collationAllowed(typeName) {
			t.Fatalf("expected %s to allow collation", typeName)
		}
	}

	disallowed := []string{"int", "bigint", "datetime", "decimal", "xml", "bit", "uniqueidentifier"}
	for _, typeName := range disallowed {
		if collationAllowed(typeName) {
			t.Fatalf("expected %s NOT to allow collation", typeName)
		}
	}
}
