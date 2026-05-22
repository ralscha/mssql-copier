package copier

import (
	"fmt"
	"maps"
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
	ComposeDir string `yaml:"compose-dir,omitempty"`
	Port       int    `yaml:"port,omitempty"`
	SAPassword string `yaml:"sa-password,omitempty"`
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
		SourceDSN:      strings.TrimSpace(cfg.SourceDSN),
		TargetDSN:      strings.TrimSpace(cfg.TargetDSN),
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
		ComposeDir: cfg.ComposeDir,
		Port:       cfg.Port,
		SAPassword: cfg.SAPassword,
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
		APIKey:     cfg.APIKey,
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
