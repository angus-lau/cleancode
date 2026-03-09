package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type CustomAgent struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

type Config struct {
	BaseBranch   string            `json:"baseBranch"`
	Agents       map[string]bool   `json:"agents"`
	CustomAgents []CustomAgent     `json:"customAgents,omitempty"`
	Schema       *SchemaConfig     `json:"schema,omitempty"`
	Ignore       []string          `json:"ignore"`
}

type SchemaConfig struct {
	Provider string `json:"provider"` // "postgres" or "supabase"
	URL      string `json:"url"`      // connection string or env var like "$DATABASE_URL"
}

func DefaultConfig() *Config {
	return &Config{
		BaseBranch: "main",
		Agents: map[string]bool{
			"correctness":  true,
			"performance":  true,
			"api-contract": true,
			"security":     false,
		},
		Ignore: []string{
			"**/*.test.ts",
			"**/*.spec.ts",
			"**/fixtures/**",
		},
	}
}

// Load reads .cleancode.json from the project root.
// Returns default config if file doesn't exist.
func Load(rootPath string) (*Config, error) {
	configPath := filepath.Join(rootPath, ".cleancode.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, err
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// Resolve env vars in schema URL
	if cfg.Schema != nil && len(cfg.Schema.URL) > 0 && cfg.Schema.URL[0] == '$' {
		envVal := os.Getenv(cfg.Schema.URL[1:])
		if envVal != "" {
			cfg.Schema.URL = envVal
		}
	}

	return cfg, nil
}

// Save writes the config to .cleancode.json.
func Save(rootPath string, cfg *Config) error {
	configPath := filepath.Join(rootPath, ".cleancode.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0644)
}
