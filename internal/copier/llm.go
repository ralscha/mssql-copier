package copier

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	openaiModel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
)

type llmConfig struct {
	Provider   string
	Model      string
	BaseURL    string
	APIKey     string
	APIKeyEnv  string
	ByAzure    bool
	APIVersion string
}

type yamlLLMConfig struct {
	Provider   string `yaml:"provider"`
	Model      string `yaml:"model"`
	BaseURL    string `yaml:"base-url"`
	APIKey     string `yaml:"api-key"`
	APIKeyEnv  string `yaml:"api-key-env"`
	ByAzure    *bool  `yaml:"by-azure"`
	APIVersion string `yaml:"api-version"`
}

func normalizeLLMConfig(yamlCfg *yamlLLMConfig) llmConfig {
	if yamlCfg == nil {
		return llmConfig{}
	}
	cfg := llmConfig{
		Provider:   strings.ToLower(strings.TrimSpace(yamlCfg.Provider)),
		Model:      strings.TrimSpace(yamlCfg.Model),
		BaseURL:    strings.TrimSpace(yamlCfg.BaseURL),
		APIKey:     strings.TrimSpace(yamlCfg.APIKey),
		APIKeyEnv:  strings.TrimSpace(yamlCfg.APIKeyEnv),
		APIVersion: strings.TrimSpace(yamlCfg.APIVersion),
	}
	if yamlCfg.ByAzure != nil {
		cfg.ByAzure = *yamlCfg.ByAzure
	}
	if cfg.Provider == "" && (cfg.Model != "" || cfg.BaseURL != "" || cfg.APIKey != "" || cfg.APIKeyEnv != "") {
		cfg.Provider = "openai"
	}
	if cfg.Provider == "openai" && cfg.APIKeyEnv == "" && cfg.APIKey == "" {
		cfg.APIKeyEnv = "OPENAI_API_KEY"
	}
	return cfg
}

func (cfg llmConfig) apiKey() string {
	if cfg.APIKey != "" {
		return cfg.APIKey
	}
	if cfg.APIKeyEnv == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(cfg.APIKeyEnv))
}

func (cfg llmConfig) isConfigured() bool {
	if cfg.Provider != "openai" {
		return false
	}
	if cfg.Model == "" {
		return false
	}
	return cfg.apiKey() != ""
}

func autoSelectFakeDataWithLLM(ctx context.Context, llm llmConfig, entries []tuiFakeDataEntry, options []fakeFunctionOption) (map[string]string, error) {
	if !llm.isConfigured() {
		return nil, fmt.Errorf("llm auto-select is not configured")
	}

	allowed := preferredSensitiveFakeFunctions(options)
	if len(allowed) == 0 {
		return nil, fmt.Errorf("no supported gofakeit functions are available for auto-selection")
	}

	chatModel, err := openaiModel.NewChatModel(ctx, &openaiModel.ChatModelConfig{
		APIKey:     llm.apiKey(),
		Model:      llm.Model,
		BaseURL:    llm.BaseURL,
		ByAzure:    llm.ByAzure,
		APIVersion: llm.APIVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("create llm client: %w", err)
	}

	response, err := chatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: autoSelectSystemPrompt()},
		{Role: schema.User, Content: autoSelectUserPrompt(entries, allowed)},
	})
	if err != nil {
		return nil, fmt.Errorf("generate faker suggestions: %w", err)
	}

	raw := strings.TrimSpace(response.Content)
	body := extractJSONObject(raw)
	if body == "" {
		return nil, fmt.Errorf("llm did not return a JSON object")
	}

	decoded := map[string]string{}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return nil, fmt.Errorf("parse llm faker suggestions: %w", err)
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, option := range allowed {
		allowedSet[option.LookupName] = struct{}{}
	}
	entrySet := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		entrySet[entry.Selector] = struct{}{}
	}

	filtered := make(map[string]string, len(decoded))
	for selector, functionName := range decoded {
		selector = normalizeFilterName(selector)
		functionName = normalizeFakeFunctionName(functionName)
		if selector == "" || functionName == "" {
			continue
		}
		if _, ok := entrySet[selector]; !ok {
			continue
		}
		if _, ok := allowedSet[functionName]; !ok {
			continue
		}
		filtered[selector] = functionName
	}

	return filtered, nil
}

func preferredSensitiveFakeFunctions(options []fakeFunctionOption) []fakeFunctionOption {
	preferred := []string{
		"name",
		"firstname",
		"lastname",
		"email",
		"username",
		"password",
		"phonenumber",
		"street",
		"city",
		"state",
		"zip",
		"country",
		"address",
		"ssn",
		"company",
		"jobtitle",
		"url",
		"domainname",
		"ipv4address",
		"ipv6address",
		"uuid",
		"creditcardnumber",
		"creditcardcvv",
		"creditcardexp",
		"date",
		"dateofbirth",
	}
	byName := make(map[string]fakeFunctionOption, len(options))
	for _, option := range options {
		byName[option.LookupName] = option
	}
	selected := make([]fakeFunctionOption, 0, len(preferred))
	seen := make(map[string]struct{}, len(preferred))
	for _, name := range preferred {
		option, ok := byName[name]
		if !ok {
			continue
		}
		selected = append(selected, option)
		seen[name] = struct{}{}
	}
	for _, option := range options {
		if len(selected) >= 40 {
			break
		}
		if _, ok := seen[option.LookupName]; ok {
			continue
		}
		blob := strings.ToLower(option.Category + " " + option.Description + " " + option.Display)
		if strings.Contains(blob, "name") || strings.Contains(blob, "email") || strings.Contains(blob, "phone") || strings.Contains(blob, "address") || strings.Contains(blob, "user") || strings.Contains(blob, "credit") || strings.Contains(blob, "internet") || strings.Contains(blob, "person") || strings.Contains(blob, "company") {
			selected = append(selected, option)
		}
	}
	return selected
}

func autoSelectSystemPrompt() string {
	return "You assign gofakeit functions to SQL Server columns that likely contain sensitive or identifying data. Return only a JSON object whose keys are fully qualified column names (schema.table.column) and whose values are allowed gofakeit function names. Omit columns that should remain unchanged. Never invent function names."
}

func autoSelectUserPrompt(entries []tuiFakeDataEntry, options []fakeFunctionOption) string {
	var builder strings.Builder
	builder.WriteString("Choose fake-data functions for sensitive columns only.\n")
	builder.WriteString("Allowed gofakeit functions:\n")
	for _, option := range options {
		builder.WriteString("- ")
		builder.WriteString(option.LookupName)
		builder.WriteString(": ")
		builder.WriteString(option.Display)
		if option.Category != "" {
			builder.WriteString(" (")
			builder.WriteString(option.Category)
			builder.WriteString(")")
		}
		if option.Description != "" {
			builder.WriteString(" - ")
			builder.WriteString(option.Description)
		}
		builder.WriteString("\n")
	}
	builder.WriteString("\nColumns:\n")
	for _, entry := range entries {
		builder.WriteString("- ")
		builder.WriteString(entry.Selector)
		if entry.TypeName != "" {
			builder.WriteString(" : ")
			builder.WriteString(entry.TypeName)
		}
		if entry.FunctionName != "" {
			builder.WriteString(" current=")
			builder.WriteString(entry.FunctionName)
		}
		builder.WriteString("\n")
	}
	builder.WriteString("\nReturn only JSON, for example: {\"dbo.users.email\":\"email\"}\n")
	return builder.String()
}

func extractJSONObject(raw string) string {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return ""
	}
	return raw[start : end+1]
}
