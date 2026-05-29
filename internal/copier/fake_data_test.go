package copier

import (
	"strings"
	"testing"
)

func TestNewDataFakerRejectsUnknownFunction(t *testing.T) {
	_, err := newDataFaker(map[string]string{"name": "NoSuchFunction"})
	if err == nil {
		t.Fatal("expected unknown function error")
	}
	if !strings.Contains(err.Error(), "unknown gofakeit function") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewDataFakerRejectsInvalidParameterValue(t *testing.T) {
	_, err := newDataFaker(map[string]string{"body": "Words.LoremIpsumSentence;0"})
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
	})
	if err != nil {
		t.Fatalf("expected semicolon parameters to resolve, got %v", err)
	}
}

func TestNewDataFakerRejectsTooManyParameters(t *testing.T) {
	_, err := newDataFaker(map[string]string{"price": "Price;1;100;200"})
	if err == nil {
		t.Fatal("expected too-many-parameters error")
	}
	if !strings.Contains(err.Error(), "only accepts") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDataFakerUsesConfiguredParameters(t *testing.T) {
	faker, err := newDataFaker(map[string]string{"body": "LoremIpsumSentence;10"})
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
	_, err := newDataFaker(map[string]string{"email": "Person.Email"})
	if err != nil {
		t.Fatalf("expected Person.Email to resolve, got %v", err)
	}
}

func TestNewDataFakerRejectsWrongCategoryQualifiedFunction(t *testing.T) {
	_, err := newDataFaker(map[string]string{"email": "Company.Email"})
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
	})
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
	faker, err := newDataFaker(map[string]string{"name.*": "FirstName"})
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
	})
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

func TestReplaceValueTruncatesLongNvarchar(t *testing.T) {
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
	// nvarchar(8 bytes) = 4 characters max
	if s != "Nort" {
		t.Fatalf("replaceValue() = %q, want %q", s, "Nort")
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

func TestReplaceValueTruncatesLongVarchar(t *testing.T) {
	c := &copier{}
	table := tableMeta{Schema: "dbo", Name: "T"}
	col := columnMeta{Name: "code", SystemTypeName: "varchar", MaxLength: 5}
	got, err := c.replaceValue(table, col, "abcdefghij")
	if err != nil {
		t.Fatalf("replaceValue() unexpected error: %v", err)
	}
	if got != "abcde" {
		t.Fatalf("replaceValue() = %q, want %q", got, "abcde")
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

func TestReplaceValueTruncatesLongBytes(t *testing.T) {
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
	if len(b) != 4 || b[0] != 1 || b[3] != 4 {
		t.Fatalf("replaceValue() = %v, want [1 2 3 4]", b)
	}
}
