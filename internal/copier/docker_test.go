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

func writeFile(path string, content []byte) error {
	return os.WriteFile(path, content, 0o600)
}
