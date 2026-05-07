package copier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadYAMLConfigMissingDefaultOptional(t *testing.T) {
	_, loaded, err := loadYAMLConfig(defaultConfigPath, false)
	if err != nil {
		t.Fatalf("expected no error for optional missing default config, got %v", err)
	}
	if loaded {
		t.Fatal("expected loaded=false for optional missing default config")
	}
}

func TestLoadYAMLConfigMissingExplicit(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "custom.yml")

	_, _, err := loadYAMLConfig(path, true)
	if err == nil {
		t.Fatal("expected error for missing explicit config path")
	}
}

func TestLoadYAMLConfigRejectsExportFlags(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "cfg.yml")
	content := "source: sqlserver://src\nexport-ddl: out.sql\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, _, err := loadYAMLConfig(path, true)
	if err == nil {
		t.Fatal("expected error when export-ddl is configured in YAML")
	}
	if !strings.Contains(err.Error(), "cannot set export-ddl") {
		t.Fatalf("expected export-ddl validation error, got %v", err)
	}
}

func TestYAMLApplyAndNormalizeList(t *testing.T) {
	workers := 6
	batchSize := 9000
	verbose := false
	plan := true
	dropExisting := true
	whitespaceKey := " users.name "
	yCfg := yamlConfig{
		SourceDSN:      " sqlserver://source ",
		TargetDSN:      " sqlserver://target ",
		Workers:        &workers,
		BatchSize:      &batchSize,
		Verbose:        &verbose,
		Plan:           &plan,
		DropExisting:   &dropExisting,
		IncludeSchemas: []string{" Sales ", "[dbo]"},
		ExcludeSchemas: []string{"", "  "},
		IncludeTables:  []string{"[sales].[orders]", "users"},
		ExcludeTables:  []string{"*.audit_%"},
		FakeData: map[string]string{
			whitespaceKey:  " Person.Name ",
			"customer_ssn": "ssn",
			"ignored":      " ",
		},
	}

	cfg := config{Workers: 2, BatchSize: 5000, Verbose: true}
	yCfg.applyTo(&cfg)

	if cfg.SourceDSN != "sqlserver://source" {
		t.Fatalf("source dsn = %q, want trimmed source", cfg.SourceDSN)
	}
	if cfg.TargetDSN != "sqlserver://target" {
		t.Fatalf("target dsn = %q, want trimmed target", cfg.TargetDSN)
	}
	if cfg.Workers != workers || cfg.BatchSize != batchSize {
		t.Fatalf("workers/batch-size = %d/%d, want %d/%d", cfg.Workers, cfg.BatchSize, workers, batchSize)
	}
	if cfg.Verbose != verbose || cfg.Plan != plan || cfg.DropExisting != dropExisting {
		t.Fatalf("bool flags not applied as expected: %+v", cfg)
	}
	if len(cfg.IncludeSchemas) != 2 || cfg.IncludeSchemas[0] != "sales" || cfg.IncludeSchemas[1] != "dbo" {
		t.Fatalf("include-schemas = %#v, want [sales dbo]", cfg.IncludeSchemas)
	}
	if cfg.ExcludeSchemas != nil {
		t.Fatalf("exclude-schemas = %#v, want nil", cfg.ExcludeSchemas)
	}
	if len(cfg.IncludeTables) != 2 || cfg.IncludeTables[0] != "sales.orders" || cfg.IncludeTables[1] != "users" {
		t.Fatalf("include-tables = %#v, want [sales.orders users]", cfg.IncludeTables)
	}
	if len(cfg.ExcludeTables) != 1 || cfg.ExcludeTables[0] != "*.audit_%" {
		t.Fatalf("exclude-tables = %#v, want [*.audit_%%]", cfg.ExcludeTables)
	}
	if len(cfg.FakeData) != 2 || cfg.FakeData["users.name"] != "Person.Name" || cfg.FakeData["customer_ssn"] != "ssn" {
		t.Fatalf("fake-data = %#v, want normalized fake-data map", cfg.FakeData)
	}
}
