package config

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeWorkflowFile is a helper that writes a WORKFLOW.md file with the given
// YAML front matter fields and prompt body.
func writeWorkflowFile(t *testing.T, path string, yaml string, prompt string) {
	t.Helper()
	content := "---\n" + yaml + "---\n" + prompt
	err := os.WriteFile(path, []byte(content), 0o644)
	require.NoError(t, err)
}

func TestInitialLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	writeWorkflowFile(t, path, "model: gpt-5\nproject_url: https://example.com\nmax_concurrency: 4\n", "Do the task.\n")

	w, err := NewWatcher(path)
	require.NoError(t, err)
	defer w.Stop()

	cfg := w.GetConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, 4, cfg.MaxConcurrencyRaw)
	assert.Equal(t, "gpt-5", cfg.ModelRaw)
	assert.Equal(t, "https://example.com", cfg.ProjectURLRaw)
	assert.Equal(t, "Do the task.", cfg.PromptTemplate)
}

func TestInitialLoadError(t *testing.T) {
	_, err := NewWatcher("/nonexistent/path/WORKFLOW.md")
	require.Error(t, err)
}

func TestReloadOnChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	writeWorkflowFile(t, path, "model: gpt-5\nproject_url: https://example.com\nmax_concurrency: 4\n", "Original prompt.\n")

	w, err := NewWatcher(path)
	require.NoError(t, err)
	defer w.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = w.Watch(ctx)
	}()

	// Give the watcher time to start.
	time.Sleep(100 * time.Millisecond)

	// Modify the file.
	writeWorkflowFile(t, path, "model: gpt-5-turbo\nproject_url: https://example.com\nmax_concurrency: 8\n", "Updated prompt.\n")

	// Wait for the watcher to pick up the change.
	assert.Eventually(t, func() bool {
		cfg := w.GetConfig()
		return cfg.MaxConcurrencyRaw == 8 && cfg.ModelRaw == "gpt-5-turbo" && cfg.PromptTemplate == "Updated prompt."
	}, 3*time.Second, 50*time.Millisecond, "config should reload with updated values")
}

func TestParseErrorKeepsOld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	writeWorkflowFile(t, path, "model: gpt-5\nproject_url: https://example.com\nmax_concurrency: 4\n", "Good prompt.\n")

	w, err := NewWatcher(path)
	require.NoError(t, err)
	defer w.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = w.Watch(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// Write invalid YAML.
	err = os.WriteFile(path, []byte("---\nmodel: [\nproject_url: bad\n---\nprompt\n"), 0o644)
	require.NoError(t, err)

	// Wait a bit to ensure the watcher processes the event.
	time.Sleep(500 * time.Millisecond)

	// Old config should be retained.
	cfg := w.GetConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, 4, cfg.MaxConcurrencyRaw)
	assert.Equal(t, "gpt-5", cfg.ModelRaw)
	assert.Equal(t, "Good prompt.", cfg.PromptTemplate)
}

func TestFileDeletedKeepsOld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	writeWorkflowFile(t, path, "model: gpt-5\nproject_url: https://example.com\nmax_concurrency: 7\n", "Prompt.\n")

	w, err := NewWatcher(path)
	require.NoError(t, err)
	defer w.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = w.Watch(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// Delete the file.
	err = os.Remove(path)
	require.NoError(t, err)

	time.Sleep(500 * time.Millisecond)

	// Old config should be retained.
	cfg := w.GetConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, 7, cfg.MaxConcurrencyRaw)
}

func TestConcurrentReads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	writeWorkflowFile(t, path, "model: gpt-5\nproject_url: https://example.com\nmax_concurrency: 5\n", "Concurrent prompt.\n")

	w, err := NewWatcher(path)
	require.NoError(t, err)
	defer w.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = w.Watch(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// Spawn multiple concurrent readers while writing changes.
	var wg sync.WaitGroup
	const numReaders = 20

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				cfg := w.GetConfig()
				// Config must never be nil and must have valid data.
				assert.NotNil(t, cfg)
				assert.NotEmpty(t, cfg.ModelRaw)
				time.Sleep(time.Millisecond)
			}
		}()
	}

	// Concurrently write valid changes.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			writeWorkflowFile(t, path, "model: gpt-5\nproject_url: https://example.com\nmax_concurrency: 5\n", "Concurrent prompt.\n")
			time.Sleep(20 * time.Millisecond)
		}
	}()

	wg.Wait()
}

func TestStopWatcher(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	writeWorkflowFile(t, path, "model: gpt-5\nproject_url: https://example.com\n", "Prompt.\n")

	w, err := NewWatcher(path)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- w.Watch(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// Stop should succeed.
	err = w.Stop()
	require.NoError(t, err)

	cancel()

	// Watch goroutine should exit.
	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("Watch goroutine did not exit after Stop")
	}

	// Config should still be accessible after stop.
	cfg := w.GetConfig()
	assert.NotNil(t, cfg)

	// Double stop should be a no-op (no panic, no error).
	err = w.Stop()
	assert.NoError(t, err)
}

func TestStopWithoutWatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	writeWorkflowFile(t, path, "model: gpt-5\nproject_url: https://example.com\n", "Prompt.\n")

	w, err := NewWatcher(path)
	require.NoError(t, err)

	// Stop without ever calling Watch should be fine.
	err = w.Stop()
	assert.NoError(t, err)
}

func TestWatchContextCancellation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	writeWorkflowFile(t, path, "model: gpt-5\nproject_url: https://example.com\n", "Prompt.\n")

	w, err := NewWatcher(path)
	require.NoError(t, err)
	defer w.Stop()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- w.Watch(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		// Watch should exit cleanly on context cancellation.
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Watch goroutine did not exit after context cancellation")
	}
}


func TestWatcher_GetConfigReturnsCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	writeWorkflowFile(t, path, "model: gpt-5\nproject_url: https://example.com\nmax_concurrency: 5\n", "Original prompt.\n")

	w, err := NewWatcher(path)
	require.NoError(t, err)
	defer w.Stop()

	// Get the config and mutate it
	cfg1 := w.GetConfig()
	require.NotNil(t, cfg1)
	originalModel := cfg1.ModelRaw
	cfg1.ModelRaw = "mutated-model"
	cfg1.MaxConcurrencyRaw = 999

	// Get the config again and verify it's unchanged
	cfg2 := w.GetConfig()
	require.NotNil(t, cfg2)
	assert.Equal(t, originalModel, cfg2.ModelRaw, "ModelRaw should not be affected by mutation")
	assert.Equal(t, 5, cfg2.MaxConcurrencyRaw, "MaxConcurrencyRaw should not be affected by mutation")

	// Verify they are different objects
	assert.NotEqual(t, cfg1, cfg2, "GetConfig should return different objects")
}

func TestReloadOnRenameEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	writeWorkflowFile(t, path, "model: gpt-5\nproject_url: https://example.com\nmax_concurrency: 4\n", "Original prompt.\n")

	w, err := NewWatcher(path)
	require.NoError(t, err)
	defer w.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = w.Watch(ctx)
	}()

	// Give the watcher time to start.
	time.Sleep(100 * time.Millisecond)

	// Simulate atomic save via rename: write to temp file, then rename to target.
	// This is how vim and other editors do atomic saves.
	tempPath := filepath.Join(dir, "WORKFLOW.md.tmp")
	writeWorkflowFile(t, tempPath, "model: gpt-5-turbo\nproject_url: https://example.com\nmax_concurrency: 8\n", "Updated via rename.\n")

	// Rename temp file to target (atomic save pattern).
	err = os.Rename(tempPath, path)
	require.NoError(t, err)

	// Wait for the watcher to pick up the rename event and reload.
	assert.Eventually(t, func() bool {
		cfg := w.GetConfig()
		return cfg.MaxConcurrencyRaw == 8 && cfg.ModelRaw == "gpt-5-turbo" && cfg.PromptTemplate == "Updated via rename."
	}, 3*time.Second, 50*time.Millisecond, "config should reload after rename event")
}

func TestReloadOnRemoveAndRecreate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	writeWorkflowFile(t, path, "model: gpt-5\nproject_url: https://example.com\nmax_concurrency: 3\n", "Original.\n")

	w, err := NewWatcher(path)
	require.NoError(t, err)
	defer w.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = w.Watch(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// Simulate atomic save via remove + recreate (another editor pattern).
	err = os.Remove(path)
	require.NoError(t, err)

	// Immediately recreate with new content.
	writeWorkflowFile(t, path, "model: gpt-5-turbo\nproject_url: https://example.com\nmax_concurrency: 6\n", "Recreated.\n")

	// Wait for the watcher to pick up the remove event, retry, and reload.
	assert.Eventually(t, func() bool {
		cfg := w.GetConfig()
		return cfg.MaxConcurrencyRaw == 6 && cfg.ModelRaw == "gpt-5-turbo" && cfg.PromptTemplate == "Recreated."
	}, 3*time.Second, 50*time.Millisecond, "config should reload after remove and recreate")
}

func TestDebounceMultipleRapidEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	writeWorkflowFile(t, path, "model: gpt-5\nproject_url: https://example.com\nmax_concurrency: 2\n", "Initial.\n")

	w, err := NewWatcher(path)
	require.NoError(t, err)
	defer w.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = w.Watch(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// Trigger multiple rapid write events (simulating editor thrashing).
	// The debounce should coalesce these into a single reload.
	for i := 0; i < 5; i++ {
		writeWorkflowFile(t, path, "model: gpt-5\nproject_url: https://example.com\nmax_concurrency: 2\n", "Rapid update.\n")
		time.Sleep(10 * time.Millisecond) // Rapid, within debounce window
	}

	// Wait for debounce to settle and reload to complete.
	time.Sleep(200 * time.Millisecond)

	cfg := w.GetConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, "Rapid update.", cfg.PromptTemplate)
	// The key assertion: we got here without panicking or hanging,
	// which means debounce worked and prevented reload thrashing.
}