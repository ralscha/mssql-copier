package copier

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"log"
	"math/big"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	mssqlDockerImage  = "mcr.microsoft.com/mssql/server:2022-latest"
	defaultDockerPort = 1433
	defaultDockerDir  = "docker"
)

type dockerTargetConfig struct {
	Enabled    bool
	Persistent bool
	ComposeDir string
	Port       int
	SAPassword string
}

func (cfg *dockerTargetConfig) ensurePassword() error {
	if cfg.SAPassword != "" {
		return nil
	}
	pw, err := randomSAPassword()
	if err != nil {
		return err
	}
	cfg.SAPassword = pw
	return nil
}

type yamlDockerConfig struct {
	Persistent *bool  `yaml:"persistent"`
	ComposeDir string `yaml:"compose-dir"`
	Port       *int   `yaml:"port"`
	SAPassword string `yaml:"sa-password"`
}

func normalizeDockerConfig(yamlCfg *yamlDockerConfig) dockerTargetConfig {
	if yamlCfg == nil {
		return dockerTargetConfig{}
	}
	cfg := dockerTargetConfig{
		Enabled:    true,
		ComposeDir: strings.TrimSpace(yamlCfg.ComposeDir),
		SAPassword: strings.TrimSpace(yamlCfg.SAPassword),
	}
	if yamlCfg.Persistent != nil {
		cfg.Persistent = *yamlCfg.Persistent
	}
	if yamlCfg.Port != nil {
		cfg.Port = *yamlCfg.Port
	}
	return cfg
}

type dockerComposeFile struct {
	Services map[string]dockerComposeService `yaml:"services"`
	Volumes  map[string]any                  `yaml:"volumes,omitempty"`
}

type dockerComposeService struct {
	Image       string   `yaml:"image"`
	Environment []string `yaml:"environment"`
	Ports       []string `yaml:"ports"`
	Volumes     []string `yaml:"volumes,omitempty"`
}

func buildDockerCompose(cfg dockerTargetConfig) dockerComposeFile {
	port := cfg.Port
	if port <= 0 {
		port = defaultDockerPort
	}
	service := dockerComposeService{
		Image: mssqlDockerImage,
		Environment: []string{
			"ACCEPT_EULA=Y",
			"MSSQL_SA_PASSWORD=" + cfg.SAPassword,
		},
		Ports: []string{fmt.Sprintf("%d:1433", port)},
	}
	compose := dockerComposeFile{
		Services: map[string]dockerComposeService{"mssql": service},
	}
	if cfg.Persistent {
		service.Volumes = []string{"mssql_data:/var/opt/mssql"}
		compose.Services["mssql"] = service
		compose.Volumes = map[string]any{"mssql_data": nil}
	}
	return compose
}

func dockerComposePath(composeDir string) string {
	dir := strings.TrimSpace(composeDir)
	if dir == "" {
		dir = defaultDockerDir
	}
	return filepath.Join(dir, "docker-compose.yml")
}

func existingDockerComposeSAPassword(composePath string) (string, bool, error) {
	// #nosec G304 -- composePath is derived from the configured local compose directory.
	raw, err := os.ReadFile(composePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read docker compose file %q: %w", composePath, err)
	}
	var compose dockerComposeFile
	if err := yaml.Unmarshal(raw, &compose); err != nil {
		return "", false, fmt.Errorf("parse docker compose file %q: %w", composePath, err)
	}
	service, ok := compose.Services["mssql"]
	if !ok {
		return "", false, nil
	}
	for _, entry := range service.Environment {
		if password, ok := strings.CutPrefix(entry, "MSSQL_SA_PASSWORD="); ok {
			password = strings.TrimSpace(password)
			if password == "" {
				return "", false, nil
			}
			return password, true, nil
		}
	}
	return "", false, nil
}

func reconcileDockerTargetConfig(cfg dockerTargetConfig) (dockerTargetConfig, error) {
	if !cfg.Persistent {
		return cfg, nil
	}
	composePath := dockerComposePath(cfg.ComposeDir)
	password, found, err := existingDockerComposeSAPassword(composePath)
	if err != nil {
		return dockerTargetConfig{}, err
	}
	if !found || password == cfg.SAPassword {
		return cfg, nil
	}
	log.Printf("docker: reusing existing SA password from %s for persistent target", composePath)
	cfg.SAPassword = password
	return cfg, nil
}

func writeDockerComposeFile(cfg dockerTargetConfig) (string, error) {
	dir := filepath.Dir(dockerComposePath(cfg.ComposeDir))
	// #nosec G301 -- compose directory is a user-selected local path; group read is intentional.
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create docker compose directory %q: %w", dir, err)
	}
	compose := buildDockerCompose(cfg)
	raw, err := yaml.Marshal(compose)
	if err != nil {
		return "", fmt.Errorf("marshal docker compose: %w", err)
	}
	path := dockerComposePath(cfg.ComposeDir)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", fmt.Errorf("write docker compose file %q: %w", path, err)
	}
	return path, nil
}

func dockerComposeDSN(cfg dockerTargetConfig) string {
	port := cfg.Port
	if port <= 0 {
		port = defaultDockerPort
	}
	u := &url.URL{
		Scheme: "sqlserver",
		User:   url.UserPassword("sa", cfg.SAPassword),
		Host:   fmt.Sprintf("localhost:%d", port),
	}
	q := u.Query()
	q.Set("database", "master")
	q.Set("encrypt", "disable")
	u.RawQuery = q.Encode()
	return u.String()
}

func setupDockerTarget(cfg dockerTargetConfig) (string, error) {
	resolvedCfg, err := reconcileDockerTargetConfig(cfg)
	if err != nil {
		return "", err
	}
	composePath, err := writeDockerComposeFile(resolvedCfg)
	if err != nil {
		return "", err
	}
	log.Printf("docker: wrote %s", composePath)
	log.Printf("docker: starting SQL Server container (port %d, persistent=%v)...", resolvedCfg.Port, resolvedCfg.Persistent)
	if err := runDockerComposeUp(composePath); err != nil {
		return "", err
	}
	dsn := dockerComposeDSN(resolvedCfg)
	log.Printf("docker: waiting for SQL Server to accept connections (up to 90s)...")
	if err := waitForSQLServer(dsn, 90*time.Second); err != nil {
		return "", err
	}
	log.Printf("docker: SQL Server is ready (SA password: %s)", resolvedCfg.SAPassword)
	return dsn, nil
}

func runDockerComposeUp(composePath string) error {
	dir := filepath.Dir(composePath)
	// #nosec G204 -- composePath is derived from a user-supplied directory, not arbitrary external input.
	cmd := exec.Command("docker", "compose", "up", "-d")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose up: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func waitForSQLServer(dsn string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		ready := func() bool {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			db, err := sql.Open("sqlserver", dsn)
			if err != nil {
				return false
			}
			defer closeAndLog(db, "docker sql server probe")
			return db.PingContext(ctx) == nil
		}()
		if ready {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("SQL Server did not become ready within %s", timeout)
		}
		time.Sleep(2 * time.Second)
	}
}

func randomSAPassword() (string, error) {
	const (
		upper   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
		lower   = "abcdefghijklmnopqrstuvwxyz"
		digits  = "0123456789"
		special = "!@#$%^&*"
		all     = upper + lower + digits + special
	)
	const length = 24
	buf := make([]byte, length)

	pick := func(charset string) (byte, error) {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return 0, err
		}
		return charset[n.Int64()], nil
	}

	for i, charset := range []string{upper, lower, digits, special} {
		b, err := pick(charset)
		if err != nil {
			return "", fmt.Errorf("generate password: %w", err)
		}
		buf[i] = b
	}
	for i := 4; i < length; i++ {
		b, err := pick(all)
		if err != nil {
			return "", fmt.Errorf("generate password: %w", err)
		}
		buf[i] = b
	}

	for i := length - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", fmt.Errorf("shuffle password: %w", err)
		}
		buf[i], buf[j.Int64()] = buf[j.Int64()], buf[i]
	}

	return string(buf), nil
}
