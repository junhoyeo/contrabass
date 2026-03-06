package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOhMyOpenCodeRunner_CompileTimeCheck(t *testing.T) {
	var _ AgentRunner = (*OhMyOpenCodeRunner)(nil)
}

func TestOhMyOpenCodeRunner_DefaultConfigs(t *testing.T) {
	cfg := &config.WorkflowConfig{}
	runner, err := NewOhMyOpenCodeRunner(cfg, time.Second)
	require.NoError(t, err)
	defer runner.Close()

	confDir := runner.ConfigDir()
	require.DirExists(t, confDir)

	ohMyData, err := os.ReadFile(filepath.Join(confDir, "oh-my-opencode.json"))
	require.NoError(t, err)

	var ohMyDoc map[string]interface{}
	require.NoError(t, json.Unmarshal(ohMyData, &ohMyDoc))

	agents, ok := ohMyDoc["agents"].(map[string]interface{})
	require.True(t, ok)
	sisyphus, ok := agents["sisyphus"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "anthropic/claude-sonnet-4-6", sisyphus["model"])
	_, hasFallback := sisyphus["fallback"]
	assert.False(t, hasFallback)

	categories, ok := ohMyDoc["categories"].(map[string]interface{})
	require.True(t, ok)
	for name, expectedModel := range DefaultCategories {
		cat, exists := categories[name].(map[string]interface{})
		require.True(t, exists, "missing category %s", name)
		assert.Equal(t, expectedModel, cat["model"], "category %s model mismatch", name)
	}

	ocData, err := os.ReadFile(filepath.Join(confDir, "opencode.json"))
	require.NoError(t, err)

	var ocDoc map[string]interface{}
	require.NoError(t, json.Unmarshal(ocData, &ocDoc))

	assert.Equal(t, "https://opencode.ai/config.json", ocDoc["$schema"])
	assert.Equal(t, "anthropic/claude-sonnet-4-6", ocDoc["model"])

	plugins, ok := ocDoc["plugin"].([]interface{})
	require.True(t, ok)
	require.Len(t, plugins, 1)
	assert.Equal(t, "oh-my-opencode", plugins[0])
}

func TestOhMyOpenCodeRunner_CustomConfigs(t *testing.T) {
	cfg := &config.WorkflowConfig{
		OhMyOpenCode: config.OhMyOpenCodeConfig{
			PluginVersion: "oh-my-opencode@3.10.0",
			Plugins:       []string{"opencode-antigravity-auth@1.2.7-beta.3"},
			Agents: map[string]config.OhMyOpenCodeAgent{
				"sisyphus": {Model: "anthropic/claude-opus-4-5"},
				"builder":  {Model: "anthropic/claude-sonnet-4-6"},
			},
			Categories: map[string]config.OhMyOpenCodeCategory{
				"quick":   {Model: "anthropic/claude-haiku-4-5"},
				"writing": {Model: "anthropic/claude-sonnet-4-6"},
			},
			Provider: config.OhMyOpenCodeProvider{
				Name:    "anthropic",
				BaseURL: "https://proxy.example.com/v1",
				APIKey:  "sk-test-key",
			},
		},
	}

	runner, err := NewOhMyOpenCodeRunner(cfg, time.Second)
	require.NoError(t, err)
	defer runner.Close()

	ohMyData, err := os.ReadFile(filepath.Join(runner.ConfigDir(), "oh-my-opencode.json"))
	require.NoError(t, err)

	var ohMyDoc map[string]interface{}
	require.NoError(t, json.Unmarshal(ohMyData, &ohMyDoc))

	agents, ok := ohMyDoc["agents"].(map[string]interface{})
	require.True(t, ok)
	require.Len(t, agents, 2)

	sisyphus := agents["sisyphus"].(map[string]interface{})
	assert.Equal(t, "anthropic/claude-opus-4-5", sisyphus["model"])
	_, hasFallback := sisyphus["fallback"]
	assert.False(t, hasFallback)

	builder := agents["builder"].(map[string]interface{})
	assert.Equal(t, "anthropic/claude-sonnet-4-6", builder["model"])

	categories, ok := ohMyDoc["categories"].(map[string]interface{})
	require.True(t, ok)
	require.Len(t, categories, 2)
	quickCat := categories["quick"].(map[string]interface{})
	assert.Equal(t, "anthropic/claude-haiku-4-5", quickCat["model"])

	ocData, err := os.ReadFile(filepath.Join(runner.ConfigDir(), "opencode.json"))
	require.NoError(t, err)

	var ocDoc map[string]interface{}
	require.NoError(t, json.Unmarshal(ocData, &ocDoc))

	plugins, ok := ocDoc["plugin"].([]interface{})
	require.True(t, ok)
	require.Len(t, plugins, 2)
	assert.Equal(t, "oh-my-opencode@3.10.0", plugins[0])
	assert.Equal(t, "opencode-antigravity-auth@1.2.7-beta.3", plugins[1])

	assert.Equal(t, "anthropic/claude-opus-4-5", ocDoc["model"])

	provider, ok := ocDoc["provider"].(map[string]interface{})
	require.True(t, ok)
	anthropic, ok := provider["anthropic"].(map[string]interface{})
	require.True(t, ok)
	opts, ok := anthropic["options"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "https://proxy.example.com/v1", opts["baseURL"])
	assert.Equal(t, "sk-test-key", opts["apiKey"])
}

func TestOhMyOpenCodeRunner_Close(t *testing.T) {
	cfg := &config.WorkflowConfig{}
	runner, err := NewOhMyOpenCodeRunner(cfg, time.Second)
	require.NoError(t, err)

	confDir := runner.ConfigDir()
	require.DirExists(t, confDir)

	require.NoError(t, runner.Close())
	assert.NoDirExists(t, confDir)
}

func TestOhMyOpenCodeRunner_NoProviderOmitted(t *testing.T) {
	cfg := &config.WorkflowConfig{}
	runner, err := NewOhMyOpenCodeRunner(cfg, time.Second)
	require.NoError(t, err)
	defer runner.Close()

	ocData, err := os.ReadFile(filepath.Join(runner.ConfigDir(), "opencode.json"))
	require.NoError(t, err)

	var ocDoc map[string]interface{}
	require.NoError(t, json.Unmarshal(ocData, &ocDoc))

	_, hasProvider := ocDoc["provider"]
	assert.False(t, hasProvider)
}

func TestOhMyOpenCodeRunner_EnvVarsInjected(t *testing.T) {
	cfg := &config.WorkflowConfig{}
	runner, err := NewOhMyOpenCodeRunner(cfg, time.Second)
	require.NoError(t, err)
	defer runner.Close()

	confDir := runner.ConfigDir()
	env := runner.ExtraEnv()

	envMap := make(map[string]string)
	for _, kv := range env {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	assert.Equal(t, filepath.Join(confDir, "opencode.json"), envMap["OPENCODE_CONFIG"])
	assert.Equal(t, confDir, envMap["OPENCODE_CONFIG_DIR"])
	assert.Equal(t, "1", envMap["OPENCODE_DISABLE_PROJECT_CONFIG"])
	assert.Contains(t, envMap["NODE_PATH"], filepath.Join(confDir, "node_modules"))
}

func TestOhMyOpenCodeRunner_NodePathPreservesExisting(t *testing.T) {
	t.Setenv("NODE_PATH", "/existing/path")

	cfg := &config.WorkflowConfig{}
	runner, err := NewOhMyOpenCodeRunner(cfg, time.Second)
	require.NoError(t, err)
	defer runner.Close()

	env := runner.ExtraEnv()
	envMap := make(map[string]string)
	for _, kv := range env {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	nodePath := envMap["NODE_PATH"]
	assert.Contains(t, nodePath, filepath.Join(runner.ConfigDir(), "node_modules"))
	assert.Contains(t, nodePath, "/existing/path")
}

func TestOhMyOpenCodeRunner_UsesOpenCodeEnvOverrides(t *testing.T) {
	t.Setenv("OPENCODE_BINARY", "custom-opencode serve")
	t.Setenv("OPENCODE_SERVER_PASSWORD", "env-secret")
	t.Setenv("OPENCODE_SERVER_USERNAME", "env-user")

	cfg := &config.WorkflowConfig{
		OpenCode: config.OpenCodeConfig{
			BinaryPath: "config-opencode serve",
			Password:   "config-secret",
			Username:   "config-user",
		},
	}

	runner, err := NewOhMyOpenCodeRunner(cfg, time.Second)
	require.NoError(t, err)
	defer runner.Close()

	assert.Equal(t, "custom-opencode serve", runner.inner.binaryPath)
	assert.Equal(t, "env-secret", runner.inner.password)
	assert.Equal(t, "env-user", runner.inner.username)
}
