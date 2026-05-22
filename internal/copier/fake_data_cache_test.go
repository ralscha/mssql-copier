package copier

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSaveCachedFakeDataEntriesRoundTripBySourceDSN(t *testing.T) {
	tmp := t.TempDir()
	previousExecutablePath := executablePath
	executablePath = func() (string, error) {
		return filepath.Join(tmp, "bin", "mssql-copier.exe"), nil
	}
	defer func() {
		executablePath = previousExecutablePath
	}()

	want := []tuiFakeDataEntry{{
		Selector:        "dbo.users.email",
		Display:         "[dbo].[users].[email]",
		TypeName:        "nvarchar(255)",
		FunctionName:    "email",
		FunctionDisplay: "Email",
		FunctionParams:  []string{"work"},
	}}

	if err := saveCachedFakeDataEntries(" sqlserver://alice:secret@source-host:1433?database=SourceDB&encrypt=disable ", want); err != nil {
		t.Fatalf("saveCachedFakeDataEntries() error = %v", err)
	}

	cachePath, err := fakeDataCachePath()
	if err != nil {
		t.Fatalf("fakeDataCachePath() error = %v", err)
	}
	if got := filepath.Dir(cachePath); got != filepath.Join(tmp, "bin", fakeDataCacheDirName) {
		t.Fatalf("cache dir = %q, want %q", got, filepath.Join(tmp, "bin", fakeDataCacheDirName))
	}
	raw, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("os.ReadFile(cachePath) error = %v", err)
	}
	rawText := string(raw)
	if !strings.Contains(rawText, "server=source-host;port=1433;database=SourceDB") {
		t.Fatalf("cache file should persist only server/port/database in source key: %s", rawText)
	}
	if strings.Contains(rawText, "password=") || strings.Contains(rawText, "user id=") {
		t.Fatalf("cache file should not persist credentials in source key: %s", rawText)
	}

	got, found, err := loadCachedFakeDataEntries("server=source-host;port=1433;database=SourceDB;user id=bob;password=other-secret;encrypt=disable")
	if err != nil {
		t.Fatalf("loadCachedFakeDataEntries() error = %v", err)
	}
	if !found {
		t.Fatal("expected cached entries for source DSN")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadCachedFakeDataEntries() = %#v, want %#v", got, want)
	}

	got, found, err = loadCachedFakeDataEntries("sqlserver://carol:rotated-pass@source-host:1433?database=SourceDB&encrypt=disable")
	if err != nil {
		t.Fatalf("loadCachedFakeDataEntries(different credentials) error = %v", err)
	}
	if !found {
		t.Fatal("expected cached entries lookup to ignore credentials")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadCachedFakeDataEntries(different credentials) = %#v, want %#v", got, want)
	}

	if _, found, err := loadCachedFakeDataEntries("sqlserver://other"); err != nil {
		t.Fatalf("loadCachedFakeDataEntries(other) error = %v", err)
	} else if found {
		t.Fatal("expected no cached entries for a different source DSN")
	}
}

func TestLoadFakeDataEntriesUsesCachedEntriesForSourceDSN(t *testing.T) {
	tmp := t.TempDir()
	previousExecutablePath := executablePath
	executablePath = func() (string, error) {
		return filepath.Join(tmp, "bin", "mssql-copier.exe"), nil
	}
	defer func() {
		executablePath = previousExecutablePath
	}()

	want := []tuiFakeDataEntry{{
		Selector:        "dbo.users.email",
		Display:         "[dbo].[users].[email]",
		TypeName:        "nvarchar(255)",
		FunctionName:    "email",
		FunctionDisplay: "Email",
	}}
	if err := saveCachedFakeDataEntries("sqlserver://cached-source", want); err != nil {
		t.Fatalf("saveCachedFakeDataEntries() error = %v", err)
	}

	_, err := loadFakeDataEntries(config{
		SourceDSN: "definitely not a valid SQL Server DSN",
		Workers:   1,
	})
	if err == nil {
		t.Fatal("expected missing cache to require source access")
	}

	got, err := loadFakeDataEntries(config{
		SourceDSN: "sqlserver://cached-source",
		Workers:   1,
	})
	if err != nil {
		t.Fatalf("loadFakeDataEntries() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadFakeDataEntries() = %#v, want %#v", got, want)
	}
}

func TestLoadCachedFakeDataMappingsBySourceDSN(t *testing.T) {
	tmp := t.TempDir()
	previousExecutablePath := executablePath
	executablePath = func() (string, error) {
		return filepath.Join(tmp, "bin", "mssql-copier.exe"), nil
	}
	defer func() {
		executablePath = previousExecutablePath
	}()

	entries := []tuiFakeDataEntry{{
		Selector:        "dbo.users.email",
		Display:         "[dbo].[users].[email]",
		TypeName:        "nvarchar(255)",
		FunctionName:    "email",
		FunctionDisplay: "Email",
	}, {
		Selector: "dbo.users.notes",
		Display:  "[dbo].[users].[notes]",
		TypeName: "nvarchar(max)",
	}}
	if err := saveCachedFakeDataEntries("sqlserver://alice:secret@cached-source:1433?database=SourceDB", entries); err != nil {
		t.Fatalf("saveCachedFakeDataEntries() error = %v", err)
	}
	if err := saveCachedFakeDataMappings("sqlserver://alice:secret@cached-source:1433?database=SourceDB", map[string]string{
		"dbo.users.email": "email",
		"name.*":          "FirstName",
	}); err != nil {
		t.Fatalf("saveCachedFakeDataMappings() error = %v", err)
	}

	got, found, err := loadCachedFakeDataMappings("server=cached-source;port=1433;database=SourceDB;user id=sa;password=changed")
	if err != nil {
		t.Fatalf("loadCachedFakeDataMappings() error = %v", err)
	}
	if !found {
		t.Fatal("expected cached mappings for source DSN")
	}
	want := map[string]string{"dbo.users.email": "email", "name.*": "FirstName"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadCachedFakeDataMappings() = %#v, want %#v", got, want)
	}
}
