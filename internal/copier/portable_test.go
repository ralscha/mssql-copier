package copier

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidatePortableBundleDestination(t *testing.T) {
	parent := t.TempDir()
	composeDir := filepath.Join(parent, "compose")
	bundleDir := filepath.Join(parent, "bundle")
	cfg := dockerTargetConfig{ComposeDir: composeDir, BundleDir: bundleDir, Portable: true}

	if err := validatePortableBundleDestination(cfg); err != nil {
		t.Fatalf("validate missing bundle destination: %v", err)
	}
	if err := os.Mkdir(bundleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validatePortableBundleDestination(cfg); err != nil {
		t.Fatalf("validate empty bundle destination: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "old.tar.gz"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validatePortableBundleDestination(cfg); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("non-empty destination error = %v", err)
	}
}

func TestValidatePortableBundleDestinationRejectsComposeDirectory(t *testing.T) {
	dir := t.TempDir()
	cfg := dockerTargetConfig{ComposeDir: dir, BundleDir: dir, Portable: true}
	if err := validatePortableBundleDestination(cfg); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("same directory error = %v", err)
	}
}

func TestValidatePortableBundleDestinationRejectsNestedDirectories(t *testing.T) {
	parent := t.TempDir()
	composeDir := filepath.Join(parent, "docker")
	bundleDir := filepath.Join(composeDir, "bundle")
	cfg := dockerTargetConfig{ComposeDir: composeDir, BundleDir: bundleDir, Portable: true}
	if err := validatePortableBundleDestination(cfg); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("nested directory error = %v", err)
	}
}

func TestNamedVolumeAt(t *testing.T) {
	var info dockerContainerInfo
	info.Mounts = append(info.Mounts, struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Destination string `json:"Destination"`
	}{Type: "volume", Name: "project_mssql_data", Destination: "/var/opt/mssql"})

	got, err := namedVolumeAt(info, "/var/opt/mssql/")
	if err != nil {
		t.Fatal(err)
	}
	if got != "project_mssql_data" {
		t.Fatalf("volume = %q", got)
	}
}

func TestNamedVolumeAtRejectsBindMount(t *testing.T) {
	var info dockerContainerInfo
	info.Mounts = append(info.Mounts, struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Destination string `json:"Destination"`
	}{Type: "bind", Destination: "/var/opt/mssql"})
	if _, err := namedVolumeAt(info, "/var/opt/mssql"); err == nil {
		t.Fatal("expected bind mount to be rejected")
	}
}

func TestSafePortableBundlePath(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, portableArchiveName)
	if err := os.WriteFile(archive, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := safePortableBundlePath(dir, portableArchiveName)
	if err != nil {
		t.Fatal(err)
	}
	if got != archive {
		t.Fatalf("safe path = %q, want %q", got, archive)
	}
	if _, err := safePortableBundlePath(dir, filepath.Join("..", "outside")); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
	if _, err := safePortableBundlePath(dir, dir); err == nil {
		t.Fatal("expected absolute path to be rejected")
	}
}

func TestPortableFileSHA256(t *testing.T) {
	file := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(file, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := portableFileSHA256(file)
	if err != nil {
		t.Fatal(err)
	}
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Fatalf("checksum = %q, want %q", got, want)
	}
}

func TestPortableComposeArgsKeepProjectDirectory(t *testing.T) {
	compose := filepath.Join("some", "bundle", "docker-compose.yml")
	got := portableComposeArgs(compose, "up", "-d")
	wantDir := filepath.Dir(compose)
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "--project-directory "+wantDir) || !strings.HasSuffix(joined, "up -d") {
		t.Fatalf("Compose args = %#v", got)
	}
}

func TestSameLocalPath(t *testing.T) {
	dir := filepath.Join("one", "two")
	if !sameLocalPath(dir, filepath.Clean(dir)) {
		t.Fatal("clean equivalent paths should match")
	}
	if runtime.GOOS == "windows" && !sameLocalPath(strings.ToUpper(dir), strings.ToLower(dir)) {
		t.Fatal("Windows paths should compare case-insensitively")
	}
}

func TestPortableRestoreHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runPortableRestoreCLI(context.Background(), []string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "mssql-copier restore") {
		t.Fatalf("restore help = %q", stderr.String())
	}
}
