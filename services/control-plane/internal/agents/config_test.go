package agents

import (
	"strings"
	"testing"
)

func validTestConfig() Configuration {
	temp := 0.7
	maxTokens := 2048
	return Configuration{
		Instructions: "Be concise and cite sources.",
		Model: ModelConfig{
			ProviderType: ProviderMock,
			ModelID:      "mock-mini",
			Temperature:  &temp,
			MaxTokens:    &maxTokens,
		},
		Tools: []ToolBinding{{Name: "calculator"}, {Name: "document.read"}},
	}
}

func TestConfigurationValidateDefaults(t *testing.T) {
	c := validTestConfig()
	c.Limits = ExecutionLimits{} // all defaults apply
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.Limits.MaxSteps != DefaultMaxSteps {
		t.Errorf("MaxSteps = %d, want default %d", c.Limits.MaxSteps, DefaultMaxSteps)
	}
	if c.Limits.TimeoutMs != DefaultTimeoutMs {
		t.Errorf("TimeoutMs = %d, want default %d", c.Limits.TimeoutMs, DefaultTimeoutMs)
	}
}

func TestConfigurationValidateRejections(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Configuration)
		frag   string
	}{
		{"empty instructions", func(c *Configuration) { c.Instructions = "  " }, "instructions is required"},
		{"bad provider", func(c *Configuration) { c.Model.ProviderType = "skynet" }, "provider_type"},
		{"missing model id", func(c *Configuration) { c.Model.ModelID = "" }, "model_id is required"},
		{"temperature range", func(c *Configuration) { v := 2.5; c.Model.Temperature = &v }, "temperature"},
		{"top_p range", func(c *Configuration) { v := 0; c.Model.TopP = &v }, "top_p"},
		{"max_tokens range", func(c *Configuration) { v := 0; c.Model.MaxTokens = &v }, "max_tokens"},
		{"duplicate tools", func(c *Configuration) {
			c.Tools = []ToolBinding{{Name: "calculator"}, {Name: " calculator "}}
		}, "duplicated"},
		{"empty tool name", func(c *Configuration) {
			c.Tools = []ToolBinding{{Name: ""}}
		}, "tools[0].name is required"},
		{"steps over hard limit", func(c *Configuration) { c.Limits.MaxSteps = 1000 }, "max_steps"},
		{"timeout under floor", func(c *Configuration) { c.Limits.TimeoutMs = 5 }, "timeout_ms"},
	}
	for _, tc := range cases {
		c := validTestConfig()
		tc.mutate(&c)
		err := c.Validate()
		if err == nil {
			t.Errorf("%s: Validate accepted an invalid configuration", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.frag) {
			t.Errorf("%s: error %q does not mention %q", tc.name, err.Error(), tc.frag)
		}
	}
}

func TestParseConfiguration(t *testing.T) {
	raw := []byte(`{
		"instructions": "test",
		"model": {"provider_type": "openai", "model_id": "gpt-x"},
		"tools": [],
		"limits": {"max_steps": 5}
	}`)
	c, err := ParseConfiguration(raw)
	if err != nil {
		t.Fatalf("ParseConfiguration: %v", err)
	}
	if c.Limits.MaxSteps != 5 || c.Limits.MaxToolCalls != DefaultMaxToolCalls {
		t.Errorf("parsed limits = %+v", c.Limits)
	}
	if _, err := ParseConfiguration([]byte(`{"instructions": 42}`)); err == nil {
		t.Error("ParseConfiguration accepted a type-invalid payload")
	}
}
