package copier

import (
	"fmt"
	"maps"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type persistedYAMLConfig struct {
	SourceDSN      string                 `yaml:"source,omitempty"`
	TargetDSN      string                 `yaml:"target,omitempty"`
	ReportMDFile   string                 `yaml:"report-md,omitempty"`
	Workers        int                    `yaml:"workers,omitempty"`
	BatchSize      int                    `yaml:"batch-size,omitempty"`
	Verbose        bool                   `yaml:"verbose,omitempty"`
	Plan           bool                   `yaml:"plan,omitempty"`
	DropExisting   bool                   `yaml:"drop-existing,omitempty"`
	IncludeSchemas []string               `yaml:"include-schemas,omitempty"`
	ExcludeSchemas []string               `yaml:"exclude-schemas,omitempty"`
	IncludeTables  []string               `yaml:"include-tables,omitempty"`
	ExcludeTables  []string               `yaml:"exclude-tables,omitempty"`
	LLM            *yamlLLMConfig         `yaml:"llm,omitempty"`
	Docker         *persistedDockerConfig `yaml:"docker,omitempty"`
	ExportDDLFile  string                 `yaml:"export-ddl,omitempty"`
	ExportDataFile string                 `yaml:"export-data,omitempty"`
	ExportDataRows int                    `yaml:"export-data-rows,omitempty"`
}

type persistedDockerConfig struct {
	Persistent bool   `yaml:"persistent,omitempty"`
	Portable   bool   `yaml:"portable,omitempty"`
	ComposeDir string `yaml:"compose-dir,omitempty"`
	BundleDir  string `yaml:"bundle-dir,omitempty"`
	Port       int    `yaml:"port,omitempty"`
}

func writePersistedConfig(path string, cfg config) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("config export path cannot be empty")
	}
	persisted := persistedConfigFromConfig(cfg)
	raw, err := yaml.Marshal(persisted)
	if err != nil {
		return fmt.Errorf("marshal config yaml: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write config file %q: %w", path, err)
	}
	return nil
}

func persistedConfigFromConfig(cfg config) persistedYAMLConfig {
	return persistedYAMLConfig{
		SourceDSN:      stripDSNPassword(strings.TrimSpace(cfg.SourceDSN)),
		TargetDSN:      stripDSNPassword(strings.TrimSpace(cfg.TargetDSN)),
		ReportMDFile:   strings.TrimSpace(cfg.ReportMDFile),
		Workers:        max(1, cfg.Workers),
		BatchSize:      max(1, cfg.BatchSize),
		Verbose:        cfg.Verbose,
		Plan:           cfg.Plan,
		DropExisting:   cfg.DropExisting,
		IncludeSchemas: append([]string(nil), cfg.IncludeSchemas...),
		ExcludeSchemas: append([]string(nil), cfg.ExcludeSchemas...),
		IncludeTables:  append([]string(nil), cfg.IncludeTables...),
		ExcludeTables:  append([]string(nil), cfg.ExcludeTables...),
		LLM:            persistedLLMConfig(cfg.LLM),
		Docker:         persistedDockerConfigFrom(cfg.Docker),
		ExportDDLFile:  strings.TrimSpace(cfg.ExportDDLFile),
		ExportDataFile: strings.TrimSpace(cfg.ExportDataFile),
		ExportDataRows: max(0, cfg.ExportDataRows),
	}
}

func persistedDockerConfigFrom(cfg dockerTargetConfig) *persistedDockerConfig {
	if !cfg.Enabled {
		return nil
	}
	return &persistedDockerConfig{
		Persistent: cfg.Persistent,
		Portable:   cfg.Portable,
		ComposeDir: cfg.ComposeDir,
		BundleDir:  cfg.BundleDir,
		Port:       cfg.Port,
	}
}

func persistedLLMConfig(cfg llmConfig) *yamlLLMConfig {
	if cfg.Provider == "" && cfg.Model == "" && cfg.BaseURL == "" && cfg.APIKey == "" && cfg.APIKeyEnv == "" && !cfg.ByAzure && cfg.APIVersion == "" {
		return nil
	}
	llm := &yamlLLMConfig{
		Provider:   cfg.Provider,
		Model:      cfg.Model,
		BaseURL:    cfg.BaseURL,
		APIKeyEnv:  cfg.APIKeyEnv,
		APIVersion: cfg.APIVersion,
	}
	if cfg.ByAzure {
		byAzure := true
		llm.ByAzure = &byAzure
	}
	return llm
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	maps.Copy(cloned, values)
	return cloned
}

func cloneStringBoolMap(values map[string]bool) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]bool, len(values))
	maps.Copy(cloned, values)
	return cloned
}

func stripDSNPassword(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return dsn
	}
	form := parseSQLServerDSNForm(dsn)
	if form.Password == "" {
		return dsn
	}
	// Preserve the original format: URL-style or key-value.
	if looksLikeSQLServerURL(dsn) {
		return stripURLPassword(dsn)
	}
	form.Password = ""
	stripped, err := buildSQLServerDSN(form)
	if err != nil || stripped == "" {
		return dsn
	}
	return stripped
}

func looksLikeSQLServerURL(dsn string) bool {
	u, err := url.Parse(dsn)
	return err == nil && u.Scheme != "" && u.Host != ""
}

func stripURLPassword(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		return dsn
	}
	if u.User == nil {
		return dsn
	}
	username := u.User.Username()
	if username == "" {
		u.User = nil
	} else {
		u.User = url.User(username)
	}
	return u.String()
}
