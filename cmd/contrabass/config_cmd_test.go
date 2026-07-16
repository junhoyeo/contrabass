package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigEffectiveCommandPrintsRedactedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "WORKFLOW.md")
	require.NoError(t, os.WriteFile(path, []byte(`---
tracker:
  type: github
  token: super-secret
---
prompt
`), 0o644))

	cmd := newRootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"config", "effective", "--config", path, "--format", "json"})

	require.NoError(t, cmd.Execute())
	assert.Contains(t, stdout.String(), `"schema_version": 1`)
	assert.Contains(t, stdout.String(), `"token": "[REDACTED]"`)
	assert.NotContains(t, stdout.String(), "super-secret")
}

func TestConfigValidateCommandRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "WORKFLOW.md")
	require.NoError(t, os.WriteFile(path, []byte(`---
max_concurency: 4
---
prompt
`), 0o644))

	cmd := newRootCmd()
	cmd.SetArgs([]string{"config", "validate", "--config", path})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_concurency")
}

func TestConfigEffectiveCommandRejectsFormatBeforeReadingConfig(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"config", "effective", "--config", filepath.Join(t.TempDir(), "missing.md"), "--format", "xml"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported format")
	assert.NotContains(t, err.Error(), "parsing workflow config")
}
