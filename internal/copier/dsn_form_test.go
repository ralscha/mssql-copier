package copier

import (
	"path/filepath"
	"testing"
)

func TestParseSQLServerDSNFormFromURL(t *testing.T) {
	got := parseSQLServerDSNForm("sqlserver://alice:secret@db.example.com:1444?database=Northwind&encrypt=disable&trustservercertificate=true&app+name=mssql-copier")

	if got.Server != "db.example.com" || got.Port != "1444" || got.Database != "Northwind" {
		t.Fatalf("parsed server/port/database = %#v", got)
	}
	if got.Username != "alice" || got.Password != "secret" {
		t.Fatalf("parsed credentials = %#v", got)
	}
	if got.Encrypt != "disable" || got.TrustServerCertificate != "true" {
		t.Fatalf("parsed tls fields = %#v", got)
	}
	if got.Options != "app name=mssql-copier" {
		t.Fatalf("parsed extra options = %q, want app name=mssql-copier", got.Options)
	}
}

func TestBuildSQLServerDSN(t *testing.T) {
	got, err := buildSQLServerDSN(sqlServerDSNForm{
		Server:                 "db.example.com",
		Port:                   "1444",
		Database:               "Northwind",
		Username:               "alice",
		Password:               "secret",
		Encrypt:                "disable",
		TrustServerCertificate: "true",
		Options:                "app name=mssql-copier;connection timeout=30",
	})
	if err != nil {
		t.Fatalf("buildSQLServerDSN() error = %v", err)
	}
	want := "server=db.example.com;port=1444;database=Northwind;user id=alice;password=secret;encrypt=disable;trustservercertificate=true;app name=mssql-copier;connection timeout=30"
	if got != want {
		t.Fatalf("buildSQLServerDSN() = %q, want %q", got, want)
	}
}

func TestTUIConfigFromFormBuildsStructuredDSNs(t *testing.T) {
	m := newTUIModel(config{Workers: 2, BatchSize: 5000, Verbose: true})
	m.form.Source = sqlServerDSNForm{
		Server:   "source-host",
		Port:     "1433",
		Database: "SourceDB",
		Username: "sa",
		Password: "pw1",
		Encrypt:  "disable",
	}
	m.form.Target = sqlServerDSNForm{
		Server:   "localhost",
		Port:     "1434",
		Database: "TargetDB",
		Username: "sa",
		Password: "pw2",
		Encrypt:  "disable",
	}
	m.form.ExportDataRows = "25"

	cfg, err := m.configFromForm(true, true)
	if err != nil {
		t.Fatalf("configFromForm() error = %v", err)
	}
	if cfg.SourceDSN != "server=source-host;port=1433;database=SourceDB;user id=sa;password=pw1;encrypt=disable" {
		t.Fatalf("source dsn = %q", cfg.SourceDSN)
	}
	if cfg.TargetDSN != "server=localhost;port=1434;database=TargetDB;user id=sa;password=pw2;encrypt=disable" {
		t.Fatalf("target dsn = %q", cfg.TargetDSN)
	}
	if cfg.ExportDataRows != 0 {
		t.Fatalf("export data rows = %d, want 0 outside ddl+data mode", cfg.ExportDataRows)
	}
}

func TestTUIConfigFromFormRejectsNegativeExportDataRows(t *testing.T) {
	m := newTUIModel(config{Workers: 2, BatchSize: 5000, Verbose: true})
	m.form.Source = sqlServerDSNForm{Server: "source-host"}
	m.form.Target = sqlServerDSNForm{Server: "target-host"}
	m.form.ExportDataRows = "-1"

	_, err := m.configFromForm(true, true)
	if err == nil {
		t.Fatal("expected negative export data rows to fail")
	}
	if err.Error() != "export data rows: must be greater than or equal to 0" {
		t.Fatalf("configFromForm() error = %q", err.Error())
	}
}

func TestTUIConfigFromFormLoadsCachedFakeDataBySourceDSN(t *testing.T) {
	tmp := t.TempDir()
	previousExecutablePath := executablePath
	executablePath = func() (string, error) {
		return filepath.Join(tmp, "bin", "mssql-copier.exe"), nil
	}
	defer func() {
		executablePath = previousExecutablePath
	}()

	if err := saveCachedFakeDataEntries("server=source-host;port=1433;database=SourceDB;user id=sa;password=pw1;encrypt=disable", []tuiFakeDataEntry{{
		Selector:        "dbo.users.email",
		Display:         "[dbo].[users].[email]",
		TypeName:        "nvarchar(255)",
		FunctionName:    "email",
		FunctionDisplay: "Email",
	}}); err != nil {
		t.Fatalf("saveCachedFakeDataEntries() error = %v", err)
	}

	m := newTUIModel(config{Workers: 2, BatchSize: 5000, Verbose: true})
	m.form.Source = sqlServerDSNForm{
		Server:   "source-host",
		Port:     "1433",
		Database: "SourceDB",
		Username: "sa",
		Password: "pw1",
		Encrypt:  "disable",
	}
	m.form.Target = sqlServerDSNForm{
		Server:   "localhost",
		Port:     "1434",
		Database: "TargetDB",
		Username: "sa",
		Password: "pw2",
		Encrypt:  "disable",
	}

	cfg, err := m.configFromForm(true, true)
	if err != nil {
		t.Fatalf("configFromForm() error = %v", err)
	}
	if got := cfg.FakeData["dbo.users.email"]; got != "email" {
		t.Fatalf("cached fake-data mapping = %#v", cfg.FakeData)
	}
}

func TestTUIConfigFromFormRejectsRemoteLocalTarget(t *testing.T) {
	m := newTUIModel(config{Workers: 2, BatchSize: 5000, Verbose: true})
	m.form.Source = sqlServerDSNForm{Server: "source-host"}
	m.form.Target = sqlServerDSNForm{Server: "db.example.com", Database: "TargetDB"}

	_, err := m.configFromForm(true, true)
	if err == nil {
		t.Fatal("expected remote local target to fail")
	}
	if err.Error() != "target DSN must point to a local address when target type is local" {
		t.Fatalf("configFromForm() error = %q", err.Error())
	}
}

func TestTUIExecutionConfigsForDDLDataMode(t *testing.T) {
	m := newTUIModel(config{Workers: 2, BatchSize: 5000, Verbose: true})
	m.form.RunMode = tuiRunModeExportDDLData
	m.form.Source = sqlServerDSNForm{
		Server:   "source-host",
		Port:     "1433",
		Database: "SourceDB",
		Username: "sa",
		Password: "pw1",
		Encrypt:  "disable",
	}
	m.form.ExportDDLPath = "./export/schema.sql"
	m.form.ExportDataPath = "./export/data.sql"
	m.form.ExportDataRows = "25"

	configs, err := m.executionConfigs()
	if err != nil {
		t.Fatalf("executionConfigs() error = %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("executionConfigs() length = %d, want 2", len(configs))
	}
	if configs[0].ExportDDLFile != "./export/schema.sql" || configs[0].ExportDataFile != "" || configs[0].ExportDataRows != 0 {
		t.Fatalf("ddl config = %#v", configs[0])
	}
	if configs[1].ExportDDLFile != "" || configs[1].ExportDataFile != "./export/data.sql" || configs[1].ExportDataRows != 25 {
		t.Fatalf("data config = %#v", configs[1])
	}
}

func TestTUIExecutionConfigsForDDLMode(t *testing.T) {
	m := newTUIModel(config{Workers: 2, BatchSize: 5000, Verbose: true})
	m.form.RunMode = tuiRunModeExportDDL
	m.form.Source = sqlServerDSNForm{Server: "source-host", Database: "SourceDB"}
	m.form.ExportDDLPath = "./export/schema.sql"
	m.form.ExportDataRows = "25"

	configs, err := m.executionConfigs()
	if err != nil {
		t.Fatalf("executionConfigs() error = %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("executionConfigs() length = %d, want 1", len(configs))
	}
	if configs[0].ExportDDLFile != "./export/schema.sql" || configs[0].ExportDataFile != "" || configs[0].ExportDataRows != 0 {
		t.Fatalf("ddl config = %#v", configs[0])
	}
}

func TestTUIConfigFromFormSetsPlanAndReportByMode(t *testing.T) {
	m := newTUIModel(config{Workers: 2, BatchSize: 5000, Verbose: true, DropExisting: true})
	m.form.Source = sqlServerDSNForm{Server: "source-host", Database: "SourceDB"}
	m.form.Target = sqlServerDSNForm{Server: "localhost", Database: "TargetDB"}
	m.form.ReportPath = "./export/copy-report.md"

	copyCfg, err := m.configFromForm(true, true)
	if err != nil {
		t.Fatalf("copy configFromForm() error = %v", err)
	}
	if copyCfg.Plan {
		t.Fatal("copy config unexpectedly enabled plan")
	}
	if copyCfg.ReportMDFile != "./export/copy-report.md" {
		t.Fatalf("copy report path = %q", copyCfg.ReportMDFile)
	}
	if !copyCfg.DropExisting {
		t.Fatal("copy config lost drop-existing")
	}

	m.form.RunMode = tuiRunModePlan
	planCfg, err := m.configFromForm(true, false)
	if err != nil {
		t.Fatalf("plan configFromForm() error = %v", err)
	}
	if !planCfg.Plan {
		t.Fatal("plan config did not enable plan")
	}
	if planCfg.ReportMDFile != "" {
		t.Fatalf("plan report path = %q, want empty", planCfg.ReportMDFile)
	}
	if planCfg.TargetDSN != "" {
		t.Fatalf("plan target dsn = %q, want empty", planCfg.TargetDSN)
	}
	if !planCfg.DropExisting {
		t.Fatal("plan config lost drop-existing")
	}

	m.form.RunMode = tuiRunModeExportDDL
	exportCfg, err := m.configFromForm(true, false)
	if err != nil {
		t.Fatalf("export configFromForm() error = %v", err)
	}
	if exportCfg.DropExisting {
		t.Fatal("ddl export config should clear drop-existing")
	}
	if exportCfg.ReportMDFile != "" {
		t.Fatalf("ddl export report path = %q, want empty", exportCfg.ReportMDFile)
	}
}

func TestTUIExecutionConfigsForPlanMode(t *testing.T) {
	m := newTUIModel(config{Workers: 2, BatchSize: 5000, Verbose: true, DropExisting: true})
	m.form.RunMode = tuiRunModePlan
	m.form.Source = sqlServerDSNForm{Server: "source-host", Database: "SourceDB"}

	configs, err := m.executionConfigs()
	if err != nil {
		t.Fatalf("executionConfigs() error = %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("executionConfigs() length = %d, want 1", len(configs))
	}
	if !configs[0].Plan {
		t.Fatal("plan execution config did not enable plan")
	}
	if configs[0].TargetDSN != "" {
		t.Fatalf("plan target dsn = %q, want empty", configs[0].TargetDSN)
	}
}

func TestBuildSQLServerDSNRejectsMalformedOptions(t *testing.T) {
	_, err := buildSQLServerDSN(sqlServerDSNForm{Server: "db.example.com", Options: "bad-option"})
	if err == nil {
		t.Fatal("expected malformed options to fail")
	}
}

func TestSQLServerDSNWithDatabase(t *testing.T) {
	updated, err := sqlServerDSNWithDatabase("sqlserver://alice:secret@db.example.com:1444?database=Northwind&encrypt=disable&trustservercertificate=true&app+name=mssql-copier", "TargetDB")
	if err != nil {
		t.Fatalf("sqlServerDSNWithDatabase() error = %v", err)
	}
	got := parseSQLServerDSNForm(updated)
	if got.Server != "db.example.com" || got.Port != "1444" || got.Database != "TargetDB" {
		t.Fatalf("updated dsn form = %#v", got)
	}
	if got.Username != "alice" || got.Password != "secret" || got.Encrypt != "disable" || got.TrustServerCertificate != "true" {
		t.Fatalf("updated dsn lost connection properties: %#v", got)
	}
	if got.Options != "app name=mssql-copier" {
		t.Fatalf("updated dsn options = %q, want app name=mssql-copier", got.Options)
	}
	if gotName := sqlServerDSNDatabaseName(updated); gotName != "TargetDB" {
		t.Fatalf("sqlServerDSNDatabaseName() = %q, want TargetDB", gotName)
	}
	if gotName := sqlServerDSNDatabaseName("server=db.example.com;database=Warehouse;user id=sa"); gotName != "Warehouse" {
		t.Fatalf("sqlServerDSNDatabaseName(key-value) = %q, want Warehouse", gotName)
	}
}

func TestParseSQLServerDSNFormFromKeyValue(t *testing.T) {
	got := parseSQLServerDSNForm("server=tcp:db.example.com,1444;database=Northwind;user id=alice;password=secret;encrypt=disable")
	if got.Server != "db.example.com" || got.Port != "1444" {
		t.Fatalf("parsed server/port = %#v", got)
	}
	if got.Database != "Northwind" || got.Username != "alice" || got.Password != "secret" {
		t.Fatalf("parsed key-value dsn = %#v", got)
	}
}
