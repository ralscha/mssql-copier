package copier

import (
	"strings"
	"testing"
)

func TestNewDataFakerRejectsUnknownFunction(t *testing.T) {
	_, err := newDataFaker(map[string]string{"name": "NoSuchFunction"}, nil)
	if err == nil {
		t.Fatal("expected unknown function error")
	}
	if !strings.Contains(err.Error(), "unknown gofakeit function") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewDataFakerRejectsInvalidParameterValue(t *testing.T) {
	_, err := newDataFaker(map[string]string{"body": "Words.LoremIpsumSentence;0"}, nil)
	if err == nil {
		t.Fatal("expected invalid parameter error")
	}
	if !strings.Contains(err.Error(), "could not initialize") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewDataFakerAcceptsSemicolonParameters(t *testing.T) {
	_, err := newDataFaker(map[string]string{
		"testcolumn":   "LoremIpsumSentence;10",
		"secondcolumn": "Price;1;100",
	}, nil)
	if err != nil {
		t.Fatalf("expected semicolon parameters to resolve, got %v", err)
	}
}

func TestNewDataFakerRejectsTooManyParameters(t *testing.T) {
	_, err := newDataFaker(map[string]string{"price": "Price;1;100;200"}, nil)
	if err == nil {
		t.Fatal("expected too-many-parameters error")
	}
	if !strings.Contains(err.Error(), "only accepts") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDataFakerUsesConfiguredParameters(t *testing.T) {
	faker, err := newDataFaker(map[string]string{"body": "LoremIpsumSentence;10"}, nil)
	if err != nil {
		t.Fatalf("newDataFaker() unexpected error: %v", err)
	}

	value, ok, err := faker.fakeValue(nil, tableMeta{Schema: "dbo", Name: "posts"}, columnMeta{Name: "body", SystemTypeName: "nvarchar"})
	if err != nil {
		t.Fatalf("fakeValue() unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected rule match")
	}
	text, isString := value.(string)
	if !isString {
		t.Fatalf("fakeValue() type = %T, want string", value)
	}
	if len(strings.Fields(text)) != 10 {
		t.Fatalf("fakeValue() word count = %d, want 10; value=%q", len(strings.Fields(text)), text)
	}
}

func TestNewDataFakerAcceptsCategoryQualifiedFunction(t *testing.T) {
	_, err := newDataFaker(map[string]string{"email": "Person.Email"}, nil)
	if err != nil {
		t.Fatalf("expected Person.Email to resolve, got %v", err)
	}
}

func TestNewDataFakerRejectsWrongCategoryQualifiedFunction(t *testing.T) {
	_, err := newDataFaker(map[string]string{"email": "Company.Email"}, nil)
	if err == nil {
		t.Fatal("expected wrong-category function error")
	}
	if !strings.Contains(err.Error(), "unknown gofakeit function") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDataFakerMatchesSpecificity(t *testing.T) {
	faker, err := newDataFaker(map[string]string{
		"dbo.users.name": "Name",
		"users.name":     "FirstName",
		"name":           "LastName",
	}, nil)
	if err != nil {
		t.Fatalf("newDataFaker() unexpected error: %v", err)
	}

	table := tableMeta{Schema: "dbo", Name: "users"}
	col := columnMeta{Name: "name", SystemTypeName: "nvarchar"}
	value, ok, err := faker.fakeValue(nil, table, col)
	if err != nil {
		t.Fatalf("fakeValue() unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected rule match")
	}
	if _, isString := value.(string); !isString {
		t.Fatalf("fakeValue() type = %T, want string", value)
	}
	if faker.fullNameRules["dbo.users.name"].lookupName != "name" {
		t.Fatalf("expected full-name rule to win, got %+v", faker.fullNameRules["dbo.users.name"])
	}
	_ = value
}

func TestDataFakerMatchesRegexSelector(t *testing.T) {
	faker, err := newDataFaker(map[string]string{"name.*": "FirstName"}, nil)
	if err != nil {
		t.Fatalf("newDataFaker() unexpected error: %v", err)
	}

	value, ok, err := faker.fakeValue(nil, tableMeta{Schema: "dbo", Name: "users"}, columnMeta{Name: "name_display", SystemTypeName: "nvarchar"})
	if err != nil {
		t.Fatalf("fakeValue() unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected regex rule match")
	}
	if _, isString := value.(string); !isString {
		t.Fatalf("fakeValue() type = %T, want string", value)
	}
}

func TestDataFakerMatchRuleReturnsWinningRule(t *testing.T) {
	faker, err := newDataFaker(map[string]string{
		"dbo.users.email": "Email",
		"email":           "FirstName",
	}, nil)
	if err != nil {
		t.Fatalf("newDataFaker() unexpected error: %v", err)
	}

	rule, ok := faker.matchRule(tableMeta{Schema: "dbo", Name: "users"}, columnMeta{Name: "email", SystemTypeName: "nvarchar"})
	if !ok {
		t.Fatal("expected rule match")
	}
	if rule.lookupName != "email" {
		t.Fatalf("matchRule() lookupName = %q, want %q", rule.lookupName, "email")
	}
}

func TestBuildFakeFunctionConfigRoundTripWithParams(t *testing.T) {
	rule, _, err := compileFakeDataRule("dbo.users.summary", buildFakeFunctionConfig("loremipsumsentence", []string{"10"}))
	if err != nil {
		t.Fatalf("compileFakeDataRule() error = %v", err)
	}
	got := buildFakeFunctionConfig(rule.lookupName, flattenFakeParams(rule.info, rule.params))
	if got != "loremipsumsentence;10" {
		t.Fatalf("buildFakeFunctionConfig() = %q, want %q", got, "loremipsumsentence;10")
	}
}

func TestReplaceValueFallsBackToOriginalNormalization(t *testing.T) {
	c := &copier{}
	got, err := c.replaceValue(tableMeta{}, columnMeta{SystemTypeName: "decimal"}, []byte("123.45"))
	if err != nil {
		t.Fatalf("replaceValue() unexpected error: %v", err)
	}
	if got != "123.45" {
		t.Fatalf("replaceValue() = %#v, want %q", got, "123.45")
	}
}

func TestReplaceValueDoesNotSilentlyTruncateSourceNvarchar(t *testing.T) {
	c := &copier{}
	table := tableMeta{Schema: "event", Name: "Event"}
	col := columnMeta{Name: "lokalitaet_kanton", SystemTypeName: "nvarchar", MaxLength: 8}
	got, err := c.replaceValue(table, col, "North Carolina")
	if err != nil {
		t.Fatalf("replaceValue() unexpected error: %v", err)
	}
	s, ok := got.(string)
	if !ok {
		t.Fatalf("replaceValue() = %T %#v, want string", got, got)
	}
	if s != "North Carolina" {
		t.Fatalf("replaceValue() = %q, want original source value", s)
	}
}

func TestReplaceValueDoesNotTruncateFittingNvarchar(t *testing.T) {
	c := &copier{}
	table := tableMeta{Schema: "event", Name: "Event"}
	col := columnMeta{Name: "lokalitaet_kanton", SystemTypeName: "nvarchar", MaxLength: 8}
	got, err := c.replaceValue(table, col, "BE")
	if err != nil {
		t.Fatalf("replaceValue() unexpected error: %v", err)
	}
	if got != "BE" {
		t.Fatalf("replaceValue() = %q, want %q", got, "BE")
	}
}

func TestReplaceValueDoesNotSilentlyTruncateSourceVarchar(t *testing.T) {
	c := &copier{}
	table := tableMeta{Schema: "dbo", Name: "T"}
	col := columnMeta{Name: "code", SystemTypeName: "varchar", MaxLength: 5}
	got, err := c.replaceValue(table, col, "abcdefghij")
	if err != nil {
		t.Fatalf("replaceValue() unexpected error: %v", err)
	}
	if got != "abcdefghij" {
		t.Fatalf("replaceValue() = %q, want original source value", got)
	}
}

func TestReplaceValueDoesNotTruncateMaxNvarchar(t *testing.T) {
	c := &copier{}
	table := tableMeta{Schema: "dbo", Name: "T"}
	col := columnMeta{Name: "notes", SystemTypeName: "nvarchar", MaxLength: -1}
	got, err := c.replaceValue(table, col, "A very long string that should not be truncated")
	if err != nil {
		t.Fatalf("replaceValue() unexpected error: %v", err)
	}
	if got != "A very long string that should not be truncated" {
		t.Fatalf("replaceValue() = %q, want unchanged value", got)
	}
}

func TestReplaceValueDoesNotSilentlyTruncateSourceBytes(t *testing.T) {
	c := &copier{}
	table := tableMeta{Schema: "dbo", Name: "T"}
	col := columnMeta{Name: "data", SystemTypeName: "varbinary", MaxLength: 4}
	got, err := c.replaceValue(table, col, []byte{1, 2, 3, 4, 5, 6})
	if err != nil {
		t.Fatalf("replaceValue() unexpected error: %v", err)
	}
	b, ok := got.([]byte)
	if !ok {
		t.Fatalf("replaceValue() = %T %#v, want []byte", got, got)
	}
	if len(b) != 6 || b[0] != 1 || b[5] != 6 {
		t.Fatalf("replaceValue() = %v, want original source bytes", b)
	}
}

func TestTruncateGeneratedNvarcharUsesUTF16Units(t *testing.T) {
	table := tableMeta{Schema: "dbo", Name: "T"}
	col := columnMeta{Name: "display_name", SystemTypeName: "nvarchar", MaxLength: 6}

	if got := truncateToColumnLength("A😀BC", col, table); got != "A😀" {
		t.Fatalf("truncateToColumnLength() = %q, want %q", got, "A😀")
	}
	if got := truncateToColumnLength("éé", col, table); got != "éé" {
		t.Fatalf("truncateToColumnLength() corrupted fitting Unicode value: %q", got)
	}
}

func TestTruncateGeneratedVarcharKeepsValidUTF8(t *testing.T) {
	table := tableMeta{Schema: "dbo", Name: "T"}
	col := columnMeta{Name: "code", SystemTypeName: "varchar", MaxLength: 3}

	got, ok := truncateToColumnLength("éé", col, table).(string)
	if !ok {
		t.Fatal("truncateToColumnLength() did not return a string")
	}
	if got != "é" {
		t.Fatalf("truncateToColumnLength() = %q, want a valid rune boundary", got)
	}
}

func TestTruncateGeneratedLegacyTextDoesNotUseMetadataPointerLength(t *testing.T) {
	table := tableMeta{Schema: "dbo", Name: "T"}
	col := columnMeta{Name: "notes", SystemTypeName: "ntext", MaxLength: 16}
	value := "This legacy text value is longer than eight characters"
	if got := truncateToColumnLength(value, col, table); got != value {
		t.Fatalf("truncateToColumnLength() = %q, want unchanged legacy text", got)
	}
}
