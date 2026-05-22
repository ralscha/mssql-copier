package copier

import "testing"

func TestLLMConfigConfigurationErrorForMissingEnvVar(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	cfg := llmConfig{
		Provider:  "openai",
		Model:     "gpt-4o-mini",
		APIKeyEnv: "OPENAI_API_KEY",
	}

	err := cfg.configurationError()
	if err == nil {
		t.Fatal("configurationError() = nil, want missing env var error")
	}
	if got := err.Error(); got != "llm auto-select is not configured: environment variable \"OPENAI_API_KEY\" is empty; use llm.api-key-env for an environment variable name or llm.api-key for a literal key" {
		t.Fatalf("configurationError() = %q", got)
	}
}

func TestLLMConfigConfigurationErrorAllowsLiteralAPIKey(t *testing.T) {
	cfg := llmConfig{
		Provider: "openai",
		Model:    "gpt-4o-mini",
		APIKey:   "secret",
	}

	if err := cfg.configurationError(); err != nil {
		t.Fatalf("configurationError() = %v, want nil", err)
	}
}
