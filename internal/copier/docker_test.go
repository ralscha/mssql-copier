package copier

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReconcileDockerTargetConfigUsesExistingPersistentComposePassword(t *testing.T) {
	tmp := t.TempDir()
	composePath := filepath.Join(tmp, "docker-compose.yml")
	if err := writeFile(composePath, []byte("services:\n  mssql:\n    image: mcr.microsoft.com/mssql/server:2022-latest\n    environment:\n      - ACCEPT_EULA=Y\n      - MSSQL_SA_PASSWORD=old-password\n")); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	got, err := reconcileDockerTargetConfig(dockerTargetConfig{
		Enabled:    true,
		Persistent: true,
		ComposeDir: tmp,
		Port:       1433,
		SAPassword: "new-password",
	})
	if err != nil {
		t.Fatalf("reconcileDockerTargetConfig() error = %v", err)
	}
	if got.SAPassword != "old-password" {
		t.Fatalf("sa password = %q, want old-password", got.SAPassword)
	}
}

func TestReconcileDockerTargetConfigKeepsConfiguredPasswordWithoutComposeFile(t *testing.T) {
	tmp := t.TempDir()
	got, err := reconcileDockerTargetConfig(dockerTargetConfig{
		Enabled:    true,
		Persistent: true,
		ComposeDir: tmp,
		Port:       1433,
		SAPassword: "configured-password",
	})
	if err != nil {
		t.Fatalf("reconcileDockerTargetConfig() error = %v", err)
	}
	if got.SAPassword != "configured-password" {
		t.Fatalf("sa password = %q, want configured-password", got.SAPassword)
	}
}

func TestDockerTargetDSNUsesSourceDatabase(t *testing.T) {
	adminDSN := "sqlserver://sa:secret@localhost:1435?database=master&encrypt=disable"
	tests := []struct {
		name      string
		sourceDSN string
	}{
		{name: "URL DSN", sourceDSN: "sqlserver://user:pass@source:1433?database=testdb"},
		{name: "ADO DSN", sourceDSN: "server=source;database=testdb;user id=user"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := dockerTargetDSN(adminDSN, tc.sourceDSN)
			if err != nil {
				t.Fatalf("dockerTargetDSN() error = %v", err)
			}
			if gotName := sqlServerDSNDatabaseName(got); gotName != "testdb" {
				t.Fatalf("target database = %q, want testdb", gotName)
			}
		})
	}
}

func TestDockerTargetDSNRequiresSourceDatabase(t *testing.T) {
	_, err := dockerTargetDSN(
		"sqlserver://sa:secret@localhost:1435?database=master&encrypt=disable",
		"server=source;user id=user",
	)
	if err == nil {
		t.Fatal("expected source DSN without a database to be rejected")
	}
}

func TestPortableDockerConfigUsesNamedVolume(t *testing.T) {
	portable := true
	cfg := normalizeDockerConfig(&yamlDockerConfig{Portable: &portable})
	if !cfg.Portable || !cfg.Persistent || !cfg.usesNamedVolume() {
		t.Fatalf("portable config = %#v, want portable persistent storage", cfg)
	}

	compose := buildDockerCompose(cfg)
	service := compose.Services[portableServiceName]
	if len(service.Volumes) != 1 || service.Volumes[0] != "mssql_data:/var/opt/mssql" {
		t.Fatalf("portable service volumes = %#v", service.Volumes)
	}
	if _, ok := compose.Volumes["mssql_data"]; !ok {
		t.Fatalf("portable Compose volumes = %#v", compose.Volumes)
	}
}

func TestDockerStorageCycle(t *testing.T) {
	var cfg dockerTargetConfig
	want := []string{"local volume", "portable bundle", "temporary"}
	for _, label := range want {
		cfg.cycleStorage()
		if got := cfg.storageLabel(); got != label {
			t.Fatalf("storage label = %q, want %q", got, label)
		}
	}
}

func writeFile(path string, content []byte) error {
	return os.WriteFile(path, content, 0o600)
}
