package copier

import (
	"bytes"
	"flag"
	"io"
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

func TestLoadYAMLConfigAcceptsExportSettings(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "cfg.yml")
	content := strings.Join([]string{
		"source: sqlserver://src",
		"report-md: copy-report.md",
		"export-ddl: out.sql",
		"export-data: seed.sql",
		"export-data-rows: 25",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	yamlCfg, loaded, err := loadYAMLConfig(path, true)
	if err != nil {
		t.Fatalf("loadYAMLConfig() error = %v", err)
	}
	if !loaded {
		t.Fatal("expected loaded config")
	}

	var got config
	yamlCfg.applyTo(&got)
	if got.ReportMDFile != "copy-report.md" || got.ExportDDLFile != "out.sql" || got.ExportDataFile != "seed.sql" || got.ExportDataRows != 25 {
		t.Fatalf("export settings = %+v", got)
	}
}

func TestWritePersistedConfigRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "exported.yml")

	cfg := config{
		ConfigPath:     path,
		SourceDSN:      "sqlserver://source",
		TargetDSN:      "sqlserver://target",
		ReportMDFile:   "copy-report.md",
		Workers:        4,
		BatchSize:      2500,
		Verbose:        true,
		DropExisting:   true,
		ExportDDLFile:  "./export/schema.sql",
		ExportDataFile: "./export/data.sql",
		ExportDataRows: 25,
		IncludeSchemas: []string{"sales", "dbo"},
		ExcludeSchemas: []string{"audit"},
		IncludeTables:  []string{"sales.orders"},
		ExcludeTables:  []string{"*.audit_%"},
		LLM:            llmConfig{Provider: "openai", Model: "gpt-4o-mini", APIKeyEnv: "OPENAI_API_KEY", BaseURL: "https://api.openai.com/v1"},
	}

	if err := writePersistedConfig(path, cfg); err != nil {
		t.Fatalf("writePersistedConfig() error = %v", err)
	}

	yamlCfg, loaded, err := loadYAMLConfig(path, true)
	if err != nil {
		t.Fatalf("loadYAMLConfig() error = %v", err)
	}
	if !loaded {
		t.Fatal("expected exported config to load")
	}

	var got config
	yamlCfg.applyTo(&got)
	if got.SourceDSN != cfg.SourceDSN || got.TargetDSN != cfg.TargetDSN {
		t.Fatalf("dsn round-trip mismatch: got %+v want %+v", got, cfg)
	}
	if got.Workers != cfg.Workers || got.BatchSize != cfg.BatchSize || got.DropExisting != cfg.DropExisting || got.Verbose != cfg.Verbose {
		t.Fatalf("settings round-trip mismatch: got %+v want %+v", got, cfg)
	}
	if got.ReportMDFile != cfg.ReportMDFile || got.ExportDDLFile != cfg.ExportDDLFile || got.ExportDataFile != cfg.ExportDataFile || got.ExportDataRows != cfg.ExportDataRows {
		t.Fatalf("export settings round-trip mismatch: got %+v want %+v", got, cfg)
	}
	if len(got.IncludeSchemas) != 2 || got.IncludeSchemas[0] != "sales" || got.IncludeSchemas[1] != "dbo" {
		t.Fatalf("include-schemas round-trip = %#v", got.IncludeSchemas)
	}
	if len(got.ExcludeTables) != 1 || got.ExcludeTables[0] != "*.audit_%" {
		t.Fatalf("exclude-tables round-trip = %#v", got.ExcludeTables)
	}
	if got.FakeData != nil {
		t.Fatalf("fake-data should not be exported to YAML, got %#v", got.FakeData)
	}
	if got.LLM.Provider != "openai" || got.LLM.Model != "gpt-4o-mini" || got.LLM.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("llm round-trip = %#v", got.LLM)
	}
}

func TestWritePersistedConfigStripsPassword(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "exported.yml")

	cfg := config{
		ConfigPath: path,
		SourceDSN:  "sqlserver://sa:secret123@db.example.com:1433?database=MyDB&encrypt=true",
		TargetDSN:  "server=target.example.com;user id=sa;password=hunter2;database=TargetDB",
		Workers:    2,
		BatchSize:  5000,
		LLM:        llmConfig{Provider: "openai", Model: "gpt-4o-mini", APIKey: "literal-llm-secret"},
		Docker:     dockerTargetConfig{Enabled: true, Port: 1433, SAPassword: "docker-sa-secret"},
	}

	if err := writePersistedConfig(path, cfg); err != nil {
		t.Fatalf("writePersistedConfig() error = %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read exported config: %v", err)
	}
	content := string(raw)
	if strings.Contains(content, "secret123") {
		t.Fatal("exported config contains source password")
	}
	if strings.Contains(content, "hunter2") {
		t.Fatal("exported config contains target password")
	}
	if strings.Contains(content, "literal-llm-secret") {
		t.Fatal("exported config contains literal LLM API key")
	}
	if strings.Contains(content, "docker-sa-secret") {
		t.Fatal("exported config contains Docker SA password")
	}

	yamlCfg, _, err := loadYAMLConfig(path, true)
	if err != nil {
		t.Fatalf("reload exported config: %v", err)
	}
	var reloaded config
	yamlCfg.applyTo(&reloaded)
	if reloaded.LLM.APIKey != "" || reloaded.Docker.SAPassword != "" {
		t.Fatalf("exported config reloaded secrets: llm=%q docker=%q", reloaded.LLM.APIKey, reloaded.Docker.SAPassword)
	}
}

func TestPortableDockerConfigRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "portable.yml")
	want := dockerTargetConfig{
		Enabled:    true,
		Persistent: true,
		Portable:   true,
		ComposeDir: "./docker-work",
		BundleDir:  "./database-bundle",
		Port:       1435,
		SAPassword: "not-exported",
	}
	if err := writePersistedConfig(configPath, config{Workers: 2, BatchSize: 5000, Docker: want}); err != nil {
		t.Fatal(err)
	}
	yamlCfg, loaded, err := loadYAMLConfig(configPath, true)
	if err != nil || !loaded {
		t.Fatalf("load portable config: loaded=%v err=%v", loaded, err)
	}
	var got config
	yamlCfg.applyTo(&got)
	if !got.Docker.Portable || !got.Docker.Persistent || got.Docker.ComposeDir != want.ComposeDir || got.Docker.BundleDir != want.BundleDir || got.Docker.Port != want.Port {
		t.Fatalf("Docker config = %#v, want %#v", got.Docker, want)
	}
	if got.Docker.SAPassword != "" {
		t.Fatalf("exported Docker password = %q", got.Docker.SAPassword)
	}
}

func TestStripDSNPassword(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "url dsn with password",
			dsn:  "sqlserver://sa:secret@db.example.com:1433?database=MyDB",
			want: "sqlserver://sa@db.example.com:1433?database=MyDB",
		},
		{
			name: "url dsn without password",
			dsn:  "sqlserver://sa@db.example.com?database=MyDB",
			want: "sqlserver://sa@db.example.com?database=MyDB",
		},
		{
			name: "key-value dsn with password",
			dsn:  "server=db.example.com;user id=sa;password=mypass;database=MyDB",
			want: "server=db.example.com;database=MyDB;user id=sa",
		},
		{
			name: "key-value dsn without password",
			dsn:  "server=db.example.com;database=MyDB",
			want: "server=db.example.com;database=MyDB",
		},
		{
			name: "empty dsn",
			dsn:  "",
			want: "",
		},
		{
			name: "whitespace only",
			dsn:  "  ",
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stripDSNPassword(tc.dsn)
			if got != tc.want {
				t.Fatalf("stripDSNPassword(%q) = %q, want %q", tc.dsn, got, tc.want)
			}
		})
	}
}

func TestSameSourceAndTargetDatabase(t *testing.T) {
	same := []struct {
		name string
		src  string
		dst  string
	}{
		{
			name: "identical URL DSNs",
			src:  "sqlserver://sa:pass@db.example.com:1433?database=MyDB",
			dst:  "sqlserver://sa:pass@db.example.com:1433?database=MyDB",
		},
		{
			name: "default port vs explicit 1433",
			src:  "sqlserver://sa:pass@db.example.com?database=MyDB",
			dst:  "sqlserver://sa:pass@db.example.com:1433?database=MyDB",
		},
		{
			name: "database name case insensitive",
			src:  "sqlserver://sa:pass@db.example.com?database=mydb",
			dst:  "sqlserver://sa:pass@db.example.com?database=MYDB",
		},
		{
			name: "server name case insensitive",
			src:  "sqlserver://sa:pass@DB.EXAMPLE.COM?database=MyDB",
			dst:  "sqlserver://sa:pass@db.example.com?database=MyDB",
		},
	}
	for _, tc := range same {
		t.Run("same/"+tc.name, func(t *testing.T) {
			if !sameSourceAndTargetDatabase(tc.src, tc.dst) {
				t.Fatalf("expected same, got different for src=%q dst=%q", tc.src, tc.dst)
			}
		})
	}

	different := []struct {
		name string
		src  string
		dst  string
	}{
		{
			name: "different databases",
			src:  "sqlserver://sa:pass@db.example.com?database=SourceDB",
			dst:  "sqlserver://sa:pass@db.example.com?database=TargetDB",
		},
		{
			name: "different servers",
			src:  "sqlserver://sa:pass@source.example.com?database=MyDB",
			dst:  "sqlserver://sa:pass@target.example.com?database=MyDB",
		},
		{
			name: "different ports",
			src:  "sqlserver://sa:pass@db.example.com:1433?database=MyDB",
			dst:  "sqlserver://sa:pass@db.example.com:1434?database=MyDB",
		},
		{
			name: "empty source",
			src:  "",
			dst:  "sqlserver://sa:pass@db.example.com?database=MyDB",
		},
	}
	for _, tc := range different {
		t.Run("different/"+tc.name, func(t *testing.T) {
			if sameSourceAndTargetDatabase(tc.src, tc.dst) {
				t.Fatalf("expected different, got same for src=%q dst=%q", tc.src, tc.dst)
			}
		})
	}
}

func TestConfigValidateSameSourceAndTarget(t *testing.T) {
	dsn := "sqlserver://sa:pass@db.example.com?database=MyDB"
	cfg := config{SourceDSN: dsn, TargetDSN: dsn}
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error when source and target are the same database, got nil")
	}
	want := "source and target DSNs must not refer to the same database"
	if err.Error() != want {
		t.Fatalf("validate() error = %q, want %q", err.Error(), want)
	}

	// export-ddl mode does not use the target, so same DSN should be allowed
	exportCfg := config{SourceDSN: dsn, TargetDSN: dsn, ExportDDLFile: "schema.sql"}
	if err := exportCfg.validate(); err != nil {
		t.Fatalf("expected no error for export-ddl mode with same DSN, got %v", err)
	}
}

func TestValidateCopyTargetDatabase(t *testing.T) {
	for _, databaseName := range []string{"master", "MODEL", "msdb", "TempDB"} {
		t.Run(databaseName, func(t *testing.T) {
			dsn := "server=localhost;database=" + databaseName
			if err := validateCopyTargetDatabase(dsn); err == nil {
				t.Fatalf("expected system database %q to be rejected", databaseName)
			}
		})
	}

	if err := validateCopyTargetDatabase("server=localhost"); err == nil {
		t.Fatal("expected target DSN without a database to be rejected")
	}
	if err := validateCopyTargetDatabase("server=localhost;database=testdb"); err != nil {
		t.Fatalf("expected application database to be accepted: %v", err)
	}
}

func TestConfigValidateExportDataRows(t *testing.T) {
	tests := []struct {
		name string
		cfg  config
		want string
	}{
		{
			name: "negative row limit",
			cfg:  config{ExportDataFile: "seed.sql", ExportDataRows: -1},
			want: "-export-data-rows must be greater than or equal to 0",
		},
		{
			name: "row limit without export-data",
			cfg:  config{ExportDataRows: 10},
			want: "-export-data-rows requires -export-data",
		},
		{
			name: "report markdown with plan",
			cfg:  config{Plan: true, ReportMDFile: "copy-report.md"},
			want: "-report-md cannot be combined with -plan",
		},
		{
			name: "report markdown with ddl export",
			cfg:  config{ExportDDLFile: "schema.sql", ReportMDFile: "copy-report.md"},
			want: "-report-md cannot be combined with -export-ddl",
		},
		{
			name: "report markdown with data export",
			cfg:  config{ExportDataFile: "seed.sql", ReportMDFile: "copy-report.md"},
			want: "-report-md cannot be combined with -export-data",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.validate()
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if err.Error() != tc.want {
				t.Fatalf("validate() error = %q, want %q", err.Error(), tc.want)
			}
		})
	}

	if err := (config{ExportDataFile: "seed.sql", ExportDataRows: 10}).validate(); err != nil {
		t.Fatalf("expected export-data row limit to validate, got %v", err)
	}
	if err := (config{ExportDDLFile: "schema.sql", ExportDataFile: "seed.sql", ExportDataRows: 10}).validate(); err != nil {
		t.Fatalf("expected combined ddl+data export to validate, got %v", err)
	}
	if err := (config{ReportMDFile: "copy-report.md"}).validate(); err != nil {
		t.Fatalf("expected report markdown config to validate, got %v", err)
	}
}

func TestConfigureUsageUsesDoubleDashFlags(t *testing.T) {
	fs := flag.NewFlagSet("mssql-copier", flag.ContinueOnError)
	buf := &bytes.Buffer{}
	fs.SetOutput(buf)

	var configPath string
	var workers int
	var verbose bool
	fs.StringVar(&configPath, "config", defaultConfigPath, "path to YAML config file")
	fs.IntVar(&workers, "workers", 4, "number of concurrent table copy workers")
	fs.BoolVar(&verbose, "verbose", true, "log per-table activity")

	configureUsage(fs, "mssql-copier")
	fs.Usage()

	output := buf.String()
	for _, want := range []string{
		"Usage of mssql-copier:",
		"  --config string",
		"  --workers int",
		"  --verbose",
		"(default \"mssql-copier.yml\")",
		"(default 4)",
		"(default true)",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("usage output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "\n  -config") || strings.Contains(output, "\n  -workers") || strings.Contains(output, "\n  -verbose") {
		t.Fatalf("usage output should not contain single-dash flags:\n%s", output)
	}
}

func TestYAMLApplyAndNormalizeList(t *testing.T) {
	workers := 6
	batchSize := 9000
	verbose := false
	plan := true
	dropExisting := true
	byAzure := true
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
		LLM: &yamlLLMConfig{
			Model:      " gpt-4o-mini ",
			BaseURL:    " https://example.invalid/v1 ",
			APIKeyEnv:  " OPENAI_API_KEY ",
			ByAzure:    &byAzure,
			APIVersion: " 2024-10-21 ",
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
	if cfg.LLM.Provider != "openai" || cfg.LLM.Model != "gpt-4o-mini" || cfg.LLM.BaseURL != "https://example.invalid/v1" || cfg.LLM.APIKeyEnv != "OPENAI_API_KEY" || !cfg.LLM.ByAzure || cfg.LLM.APIVersion != "2024-10-21" {
		t.Fatalf("llm = %#v, want normalized llm config", cfg.LLM)
	}
}

func TestIsLocalTargetDSN(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want bool
	}{
		{name: "localhost url", dsn: "sqlserver://user:pass@localhost:1433?database=db", want: true},
		{name: "ipv4 loopback url", dsn: "sqlserver://user:pass@127.0.0.1:1433?database=db", want: true},
		{name: "ipv6 loopback url", dsn: "sqlserver://user:pass@[::1]:1433?database=db", want: true},
		{name: "server equals localhost", dsn: "server=localhost;database=db", want: true},
		{name: "server dot alias", dsn: "server=.\\SQLEXPRESS;database=db", want: true},
		{name: "server local alias", dsn: "server=(local)\\SQLEXPRESS;database=db", want: true},
		{name: "server localdb alias", dsn: "server=(localdb)\\MSSQLLocalDB;database=db", want: true},
		{name: "server equals ipv4 loopback", dsn: "server=127.0.0.1,1433;database=db", want: true},
		{name: "server equals ipv6 loopback", dsn: "server=[::1]:1433;database=db", want: true},
		{name: "remote url", dsn: "sqlserver://user:pass@db.example.com:1433?database=db", want: false},
		{name: "remote server", dsn: "server=tcp:db.example.com,1433;database=db", want: false},
		{name: "empty", dsn: "", want: false},
	}

	if machineHost, err := os.Hostname(); err == nil && strings.TrimSpace(machineHost) != "" {
		tests = append(tests,
			struct {
				name string
				dsn  string
				want bool
			}{name: "server equals machine hostname", dsn: "server=" + machineHost + ";database=db", want: true},
		)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLocalTargetDSN(tc.dsn); got != tc.want {
				t.Fatalf("isLocalTargetDSN(%q) = %v, want %v", tc.dsn, got, tc.want)
			}
		})
	}
}

func TestConfirmTargetPermission(t *testing.T) {
	oldInput := confirmationInput
	oldOutput := confirmationOutput
	t.Cleanup(func() {
		confirmationInput = oldInput
		confirmationOutput = oldOutput
	})

	t.Run("skips prompt for local target", func(t *testing.T) {
		confirmationInput = strings.NewReader("")
		output := &bytes.Buffer{}
		confirmationOutput = output

		if err := confirmTargetPermission("sqlserver://user:pass@localhost:1433?database=db", true); err != nil {
			t.Fatalf("confirmTargetPermission returned error for local target: %v", err)
		}
		if output.Len() != 0 {
			t.Fatalf("expected no prompt for local target, got %q", output.String())
		}
	})

	t.Run("accepts explicit yes for remote target", func(t *testing.T) {
		confirmationInput = strings.NewReader("yes\n")
		output := &bytes.Buffer{}
		confirmationOutput = output

		if err := confirmTargetPermission("sqlserver://user:pass@db.example.com:1433?database=db", true); err != nil {
			t.Fatalf("confirmTargetPermission returned error: %v", err)
		}
		if !strings.Contains(output.String(), `Target host "db.example.com" is not local`) {
			t.Fatalf("expected prompt to mention remote host, got %q", output.String())
		}
	})

	t.Run("rejects non-yes for remote target", func(t *testing.T) {
		confirmationInput = strings.NewReader("no\n")
		confirmationOutput = io.Discard

		err := confirmTargetPermission("sqlserver://user:pass@db.example.com:1433?database=db", true)
		if err == nil {
			t.Fatal("expected error when remote target confirmation is denied")
		}
		if !strings.Contains(err.Error(), "aborted before connecting to non-local target") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("skips prompt when target is not required", func(t *testing.T) {
		confirmationInput = strings.NewReader("")
		output := &bytes.Buffer{}
		confirmationOutput = output

		if err := confirmTargetPermission("sqlserver://user:pass@db.example.com:1433?database=db", false); err != nil {
			t.Fatalf("confirmTargetPermission returned error when target is optional: %v", err)
		}
		if output.Len() != 0 {
			t.Fatalf("expected no prompt when target is optional, got %q", output.String())
		}
	})
}
