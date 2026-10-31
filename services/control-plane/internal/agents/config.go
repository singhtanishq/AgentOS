// Package agents implements the agent registry: CRUD, immutable versions,
// configuration validation and the status lifecycle.
package agents

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ProviderType enumerates supported model providers.
type ProviderType string

const (
	ProviderOpenAI     ProviderType = "openai"
	ProviderAnthropic  ProviderType = "anthropic"
	ProviderGoogle     ProviderType = "google"
	ProviderCompatible ProviderType = "compatible"
	ProviderMock       ProviderType = "mock"
)

// ValidProvider reports whether the provider type is supported.
func ValidProvider(p ProviderType) bool {
	switch p {
	case ProviderOpenAI, ProviderAnthropic, ProviderGoogle, ProviderCompatible, ProviderMock:
		return true
	}
	return false
}

// ModelConfig describes the model invocation parameters for an agent.
type ModelConfig struct {
	ProviderType ProviderType `json:"provider_type"`
	ModelID      string       `json:"model_id"`
	Temperature  *float64     `json:"temperature,omitempty"`
	TopP         *float64     `json:"top_p,omitempty"`
	MaxTokens    *int         `json:"max_tokens,omitempty"`
}

// ToolBinding binds a registry tool to the agent.
type ToolBinding struct {
	Name string `json:"name"`
}

// ExecutionLimits bounds agent runs. Zero values inherit platform defaults.
type ExecutionLimits struct {
	MaxSteps       int `json:"max_steps"`
	MaxToolCalls   int `json:"max_tool_calls"`
	MaxTotalTokens int `json:"max_total_tokens"`
	TimeoutMs      int `json:"timeout_ms"`
}

// Defaults applied when a limit is unset or invalid.
const (
	DefaultMaxSteps       = 12
	DefaultMaxToolCalls   = 25
	DefaultMaxTotalTokens = 120_000
	DefaultTimeoutMs      = 300_000
	HardMaxSteps          = 50
	HardMaxToolCalls      = 100
	HardMaxTotalTokens    = 1_000_000
	HardTimeoutMs         = 900_000 // 15 minutes
)

// Configuration is the immutable per-version agent contract shared by the
// control plane and the Python runtime.
type Configuration struct {
	Instructions string          `json:"instructions"`
	Model        ModelConfig     `json:"model"`
	Tools        []ToolBinding   `json:"tools"`
	Limits       ExecutionLimits `json:"limits"`
}

// ValidationError lists every schema problem found in a configuration.
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	return "invalid configuration: " + strings.Join(e.Errors, "; ")
}

func (e *ValidationError) add(format string, args ...any) {
	e.Errors = append(e.Errors, fmt.Sprintf(format, args...))
}

// Validate checks the configuration against the platform schema and
// applies defaults for unset execution limits.
func (c *Configuration) Validate() error {
	var errs ValidationError

	instructions := strings.TrimSpace(c.Instructions)
	if instructions == "" {
		errs.add("instructions is required")
	} else if len(instructions) > 32_000 {
		errs.add("instructions must be at most 32000 characters")
	}
	c.Instructions = instructions

	if !ValidProvider(c.Model.ProviderType) {
		errs.add("model.provider_type must be one of openai, anthropic, google, compatible, mock")
	}
	if strings.TrimSpace(c.Model.ModelID) == "" {
		errs.add("model.model_id is required")
	} else if len(c.Model.ModelID) > 200 {
		errs.add("model.model_id must be at most 200 characters")
	}
	if c.Model.Temperature != nil && (*c.Model.Temperature < 0 || *c.Model.Temperature > 2) {
		errs.add("model.temperature must be between 0 and 2")
	}
	if c.Model.TopP != nil && (*c.Model.TopP <= 0 || *c.Model.TopP > 1) {
		errs.add("model.top_p must be between 0 (exclusive) and 1")
	}
	if c.Model.MaxTokens != nil && (*c.Model.MaxTokens < 1 || *c.Model.MaxTokens > 200_000) {
		errs.add("model.max_tokens must be between 1 and 200000")
	}

	seen := make(map[string]struct{}, len(c.Tools))
	for i, t := range c.Tools {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			errs.add("tools[%d].name is required", i)
			continue
		}
		if _, dup := seen[name]; dup {
			errs.add("tools[%d].name %q is duplicated", i, name)
			continue
		}
		seen[name] = struct{}{}
		c.Tools[i].Name = name
	}

	if c.Limits.MaxSteps == 0 {
		c.Limits.MaxSteps = DefaultMaxSteps
	} else if c.Limits.MaxSteps < 1 || c.Limits.MaxSteps > HardMaxSteps {
		errs.add("limits.max_steps must be between 1 and %d", HardMaxSteps)
	}
	if c.Limits.MaxToolCalls == 0 {
		c.Limits.MaxToolCalls = DefaultMaxToolCalls
	} else if c.Limits.MaxToolCalls < 1 || c.Limits.MaxToolCalls > HardMaxToolCalls {
		errs.add("limits.max_tool_calls must be between 1 and %d", HardMaxToolCalls)
	}
	if c.Limits.MaxTotalTokens == 0 {
		c.Limits.MaxTotalTokens = DefaultMaxTotalTokens
	} else if c.Limits.MaxTotalTokens < 1 || c.Limits.MaxTotalTokens > HardMaxTotalTokens {
		errs.add("limits.max_total_tokens must be between 1 and %d", HardMaxTotalTokens)
	}
	if c.Limits.TimeoutMs == 0 {
		c.Limits.TimeoutMs = DefaultTimeoutMs
	} else if c.Limits.TimeoutMs < 1000 || c.Limits.TimeoutMs > HardTimeoutMs {
		errs.add("limits.timeout_ms must be between 1000 and %d", HardTimeoutMs)
	}

	if len(errs.Errors) > 0 {
		return &errs
	}
	return nil
}

// ParseConfiguration decodes and validates a stored configuration JSON.
func ParseConfiguration(raw []byte) (*Configuration, error) {
	var c Configuration
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("decode configuration: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}
