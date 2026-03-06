package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/types"
)

// DefaultCategories defines the standard oh-my-opencode category-to-model
// mapping used when the user does not override categories in the workflow config.
var DefaultCategories = map[string]string{
	"visual-engineering": "anthropic/claude-sonnet-4-6",
	"ultrabrain":         "anthropic/claude-sonnet-4-6",
	"deep":               "anthropic/claude-sonnet-4-6",
	"artistry":           "anthropic/claude-sonnet-4-6",
	"quick":              "anthropic/claude-haiku-4-5",
	"unspecified-low":    "anthropic/claude-haiku-4-5",
	"unspecified-high":   "anthropic/claude-sonnet-4-6",
	"writing":            "anthropic/claude-sonnet-4-6",
	"git":                "anthropic/claude-haiku-4-5",
	"free":               "anthropic/claude-haiku-4-5",
}

type OhMyOpenCodeRunner struct {
	inner   *OpenCodeRunner
	cfg     *config.WorkflowConfig
	confDir string
}

var _ AgentRunner = (*OhMyOpenCodeRunner)(nil)

type ohMyOpenCodeJSON struct {
	Agents     map[string]ohMyAgentJSON    `json:"agents"`
	Categories map[string]ohMyCategoryJSON `json:"categories"`
}

type ohMyAgentJSON struct {
	Model string `json:"model"`
}

type ohMyCategoryJSON struct {
	Model string `json:"model"`
}

type opencodeJSON struct {
	Schema     string                          `json:"$schema,omitempty"`
	Model      string                          `json:"model,omitempty"`
	Plugin     []string                        `json:"plugin,omitempty"`
	Permission map[string]interface{}          `json:"permission,omitempty"`
	Provider   map[string]opencodeProviderJSON `json:"provider,omitempty"`
}

type opencodeProviderJSON struct {
	Name    string               `json:"name,omitempty"`
	NPM     string               `json:"npm,omitempty"`
	Options opencodeProviderOpts `json:"options"`
}

type opencodeProviderOpts struct {
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey"`
}

func NewOhMyOpenCodeRunner(cfg *config.WorkflowConfig, timeout time.Duration) (*OhMyOpenCodeRunner, error) {
	confDir, err := os.MkdirTemp("", "contrabass-ohmyopencode-*")
	if err != nil {
		return nil, fmt.Errorf("create oh-my-opencode config dir: %w", err)
	}

	runner := &OhMyOpenCodeRunner{
		cfg:     cfg,
		confDir: confDir,
	}

	if err := runner.writeConfigs(); err != nil {
		_ = os.RemoveAll(confDir)
		return nil, err
	}

	binaryPath := getenvOrDefault("OPENCODE_BINARY", cfg.OpenCodeBinaryPath())
	password := getenvOrDefault("OPENCODE_SERVER_PASSWORD", cfg.OpenCodePassword())
	username := getenvOrDefault("OPENCODE_SERVER_USERNAME", cfg.OpenCodeUsername())

	inner := NewOpenCodeRunner(binaryPath, cfg.OpenCodePort(), password, username, timeout)
	inner.SetExtraEnv(runner.buildEnv())
	runner.inner = inner

	return runner, nil
}

func (r *OhMyOpenCodeRunner) buildEnv() []string {
	env := []string{
		"OPENCODE_CONFIG=" + filepath.Join(r.confDir, "opencode.json"),
		"OPENCODE_CONFIG_DIR=" + r.confDir,
		"OPENCODE_DISABLE_PROJECT_CONFIG=1",
	}

	nodePath := filepath.Join(r.confDir, "node_modules")
	if existing := os.Getenv("NODE_PATH"); existing != "" {
		nodePath = nodePath + string(os.PathListSeparator) + existing
	}
	env = append(env, "NODE_PATH="+nodePath)

	return env
}

func (r *OhMyOpenCodeRunner) Start(ctx context.Context, issue types.Issue, workspace string, prompt string) (*AgentProcess, error) {
	return r.inner.Start(ctx, issue, workspace, prompt)
}

func (r *OhMyOpenCodeRunner) Stop(proc *AgentProcess) error {
	return r.inner.Stop(proc)
}

func (r *OhMyOpenCodeRunner) Close() error {
	innerErr := r.inner.Close()
	removeErr := os.RemoveAll(r.confDir)
	if innerErr != nil {
		return innerErr
	}
	return removeErr
}

func (r *OhMyOpenCodeRunner) ConfigDir() string {
	return r.confDir
}

func (r *OhMyOpenCodeRunner) ExtraEnv() []string {
	return r.inner.ExtraEnv()
}

func (r *OhMyOpenCodeRunner) writeConfigs() error {
	if err := r.writeOhMyOpenCodeJSON(); err != nil {
		return err
	}
	return r.writeOpencodeJSON()
}

func (r *OhMyOpenCodeRunner) writeOhMyOpenCodeJSON() error {
	agents := make(map[string]ohMyAgentJSON)
	cfgAgents := r.cfg.OhMyOpenCodeAgents()

	if len(cfgAgents) == 0 {
		agents["sisyphus"] = ohMyAgentJSON{
			Model: "anthropic/claude-sonnet-4-6",
		}
	} else {
		for name, a := range cfgAgents {
			agents[name] = ohMyAgentJSON{
				Model: a.Model,
			}
		}
	}

	categories := make(map[string]ohMyCategoryJSON)
	cfgCategories := r.cfg.OhMyOpenCodeCategories()

	if len(cfgCategories) == 0 {
		for name, model := range DefaultCategories {
			categories[name] = ohMyCategoryJSON{Model: model}
		}
	} else {
		for name, c := range cfgCategories {
			categories[name] = ohMyCategoryJSON{Model: c.Model}
		}
	}

	doc := ohMyOpenCodeJSON{
		Agents:     agents,
		Categories: categories,
	}

	return r.writeJSON(filepath.Join(r.confDir, "oh-my-opencode.json"), doc)
}

func (r *OhMyOpenCodeRunner) writeOpencodeJSON() error {
	plugins := []string{r.cfg.OhMyOpenCodePluginVersion()}
	plugins = append(plugins, r.cfg.OhMyOpenCodePlugins()...)

	doc := opencodeJSON{
		Schema: "https://opencode.ai/config.json",
		Plugin: plugins,
		Permission: map[string]interface{}{
			"read": map[string]string{
				".env":    "allow",
				"*.env.*": "allow",
				"*.env":   "allow",
			},
			"edit":     "allow",
			"bash":     "allow",
			"webfetch": "allow",
		},
	}

	agentModel := ""
	cfgAgents := r.cfg.OhMyOpenCodeAgents()
	if a, ok := cfgAgents["sisyphus"]; ok && a.Model != "" {
		agentModel = a.Model
	} else if len(cfgAgents) == 0 {
		agentModel = "anthropic/claude-sonnet-4-6"
	}
	if agentModel != "" {
		doc.Model = agentModel
	}

	providerName := r.cfg.OhMyOpenCodeProviderName()
	baseURL := r.cfg.OhMyOpenCodeProviderBaseURL()
	apiKey := r.cfg.OhMyOpenCodeProviderAPIKey()

	if providerName != "" && baseURL != "" {
		doc.Provider = map[string]opencodeProviderJSON{
			providerName: {
				Options: opencodeProviderOpts{
					BaseURL: baseURL,
					APIKey:  apiKey,
				},
			},
		}
	}

	return r.writeJSON(filepath.Join(r.confDir, "opencode.json"), doc)
}

func (r *OhMyOpenCodeRunner) writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

func getenvOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
