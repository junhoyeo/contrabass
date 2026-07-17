package tmux

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCLIRegistry_DefaultAgentTypesRegistered(t *testing.T) {
	r := NewCLIRegistry()

	agentTypes := []string{"codex", "opencode", "omx", "omc", "oh-my-opencode"}
	for _, agentType := range agentTypes {
		cfg, err := r.Get(agentType)
		require.NoError(t, err)
		require.NotNil(t, cfg)
	}

	assert.Len(t, r.List(), 5)
}

func TestCLIRegistry_Get(t *testing.T) {
	testCases := []struct {
		name       string
		agentType  string
		binaryPath string
		promptMode string
		wantArgs   []string
	}{
		{name: "codex", agentType: "codex", binaryPath: "codex", promptMode: "stdin", wantArgs: []string{"exec", "-"}},
		{name: "opencode", agentType: "opencode", binaryPath: "opencode", promptMode: "file", wantArgs: []string{"run"}},
		{name: "omx", agentType: "omx", binaryPath: "omx", promptMode: "arg", wantArgs: []string{"team"}},
		{name: "omc", agentType: "omc", binaryPath: "omc", promptMode: "arg", wantArgs: []string{"team"}},
		{name: "oh-my-opencode", agentType: "oh-my-opencode", binaryPath: "oh-my-opencode", promptMode: "file", wantArgs: []string{"run"}},
	}

	r := NewCLIRegistry()
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg, err := r.Get(testCase.agentType)
			require.NoError(t, err)
			require.NotNil(t, cfg)
			assert.Equal(t, testCase.agentType, cfg.AgentType)
			assert.Equal(t, testCase.binaryPath, cfg.BinaryPath)
			assert.Equal(t, testCase.promptMode, cfg.PromptMode)
			assert.Equal(t, testCase.wantArgs, cfg.BuildArgs("/tmp/workspace", "prompt"))
		})
	}
}

func TestCLIRegistry_GetUnknownTypeReturnsError(t *testing.T) {
	r := NewCLIRegistry()

	cfg, err := r.Get("unknown")
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.EqualError(t, err, "unknown agent type \"unknown\"")
}

func TestCLIRegistry_RegisterAddsNewConfig(t *testing.T) {
	r := NewCLIRegistry()

	err := r.Register(CLIConfig{
		AgentType:  "gemini",
		BinaryPath: "gemini",
		BuildArgs: func(_, _ string) []string {
			return []string{"run", "--mode", "team"}
		},
		Env:        map[string]string{"GEMINI_API_KEY": "test-key"},
		PromptMode: "arg",
	})
	require.NoError(t, err)

	cfg, err := r.Get("gemini")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "gemini", cfg.BinaryPath)
	assert.Equal(t, []string{"run", "--mode", "team"}, cfg.BuildArgs("", ""))
	assert.Equal(t, map[string]string{"GEMINI_API_KEY": "test-key"}, cfg.Env)
	assert.Equal(t, "arg", cfg.PromptMode)
}

func TestCLIRegistry_RegisterOverridesExistingConfig(t *testing.T) {
	r := NewCLIRegistry()

	err := r.Register(CLIConfig{
		AgentType:  "codex",
		BinaryPath: "codex-custom",
		BuildArgs: func(_, _ string) []string {
			return []string{"serve"}
		},
		Env:        map[string]string{"CODEX_BINARY": "codex-custom"},
		PromptMode: "arg",
	})
	require.NoError(t, err)

	cfg, err := r.Get("codex")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "codex-custom", cfg.BinaryPath)
	assert.Equal(t, []string{"serve"}, cfg.BuildArgs("", ""))
	assert.Equal(t, map[string]string{"CODEX_BINARY": "codex-custom"}, cfg.Env)
	assert.Equal(t, "arg", cfg.PromptMode)
}

func TestCLIRegistry_ListReturnsAllConfigs(t *testing.T) {
	r := NewCLIRegistry()

	configs := r.List()
	assert.Len(t, configs, 5)

	seen := make(map[string]bool, len(configs))
	for _, cfg := range configs {
		seen[cfg.AgentType] = true
	}

	assert.True(t, seen["codex"])
	assert.True(t, seen["opencode"])
	assert.True(t, seen["omx"])
	assert.True(t, seen["omc"])
	assert.True(t, seen["oh-my-opencode"])
}

func TestCLIRegistry_BuildArgsForEachAgentType(t *testing.T) {
	r := NewCLIRegistry()

	testCases := []struct {
		name      string
		agentType string
		want      []string
	}{
		// Every entry must be a run-to-completion command: pane-based workers
		// detect completion from the command's exit, so a server entrypoint
		// (e.g. "opencode serve") would hang the pipeline forever.
		{name: "codex exec", agentType: "codex", want: []string{"exec", "-"}},
		{name: "opencode run", agentType: "opencode", want: []string{"run"}},
		{name: "omx team", agentType: "omx", want: []string{"team"}},
		{name: "omc team", agentType: "omc", want: []string{"team"}},
		{name: "oh-my-opencode run", agentType: "oh-my-opencode", want: []string{"run"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg, err := r.Get(testCase.agentType)
			require.NoError(t, err)
			require.NotNil(t, cfg)
			assert.Equal(t, testCase.want, cfg.BuildArgs("/workspace", "test prompt"))
		})
	}
}
