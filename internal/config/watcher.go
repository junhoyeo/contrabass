package config

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/fsnotify/fsnotify"
)

// Watcher monitors a WORKFLOW.md file for changes and dynamically reloads the
// parsed WorkflowConfig. On parse errors or file deletion, the last known good
// configuration is retained.
type Watcher struct {
	filePath string
	mu       sync.RWMutex
	config   *WorkflowConfig
	fsw      *fsnotify.Watcher
	stopOnce sync.Once

	// debounce timer and mutex for coalescing rapid events
	debounceTimer *time.Timer
	debounceMu    sync.Mutex
}

// NewWatcher creates a Watcher for the given file path. It performs an initial
// parse and returns an error if the file cannot be read or parsed.
func NewWatcher(filePath string) (*Watcher, error) {
	cfg, err := ParseWorkflow(filePath)
	if err != nil {
		return nil, err
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		filePath: filePath,
		config:   cfg,
		fsw:      fsw,
	}, nil
}

// GetConfig returns a defensive copy of the current WorkflowConfig in a
// thread-safe manner. The returned config is independent and safe to mutate
// without affecting the internal state.
func (w *Watcher) GetConfig() *WorkflowConfig {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.config == nil {
		return nil
	}
	cfg := *w.config // shallow copy (WorkflowConfig has no pointer/slice/map fields)
	return &cfg
}

// Watch starts watching the file for changes. It blocks until the context is
// cancelled or the watcher is stopped. On file write events, the config is
// re-parsed; if parsing fails, the previous valid config is kept and a warning
// is logged.
func (w *Watcher) Watch(ctx context.Context) error {
	// Watch the parent directory so we still get events if the file is
	// deleted and recreated (editors often do atomic saves this way).
	dir := filepath.Dir(w.filePath)
	if err := w.fsw.Add(dir); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-w.fsw.Events:
			if !ok {
				return nil
			}
			w.handleEvent(event)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return nil
			}
			log.Warn("file watcher error", "err", err)
		}
	}
}

// handleEvent processes an fsnotify event. Reload is triggered on Write, Create,
// Rename, and Remove events for the watched file. Events are debounced to prevent
// rapid reload thrashing from atomic-save patterns (e.g., vim's rename-based saves).
func (w *Watcher) handleEvent(event fsnotify.Event) {
	// Normalise both paths to compare reliably across platforms.
	eventPath, err1 := filepath.Abs(event.Name)
	watchPath, err2 := filepath.Abs(w.filePath)
	if err1 != nil || err2 != nil || eventPath != watchPath {
		return
	}

	// Trigger reload on Write, Create, Rename, or Remove events.
	if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) &&
		!event.Has(fsnotify.Rename) && !event.Has(fsnotify.Remove) {
		return
	}

	w.scheduleReload()
}

// scheduleReload debounces reload requests. Multiple rapid events (e.g., from
// atomic-save patterns) are coalesced into a single reload after 100ms of quiet.
// For Remove events, a retry window is added to account for editor replace timing.
func (w *Watcher) scheduleReload() {
	w.debounceMu.Lock()
	defer w.debounceMu.Unlock()

	// Cancel any pending reload.
	if w.debounceTimer != nil {
		w.debounceTimer.Stop()
	}

	// Schedule a new reload after debounce period.
	w.debounceTimer = time.AfterFunc(100*time.Millisecond, func() {
		w.reloadWithRetry()
	})
}

// reloadWithRetry attempts to reload the config, with a retry window for Remove
// events. If the file is missing (Remove event), it retries for up to 50ms to
// account for editor replace timing (delete + recreate).
func (w *Watcher) reloadWithRetry() {
	cfg, err := ParseWorkflow(w.filePath)

	// If file is missing, retry briefly to handle atomic-save patterns.
	if err != nil && !fileExists(w.filePath) {
		time.Sleep(50 * time.Millisecond)
		cfg, err = ParseWorkflow(w.filePath)
	}

	if err != nil {
		log.Warn("failed to reload workflow config, keeping previous", "path", w.filePath, "err", err)
		return
	}

	w.mu.Lock()
	w.config = cfg
	w.mu.Unlock()

	log.Info("workflow config reloaded", "path", w.filePath)
}

// fileExists checks if a file exists without returning an error.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Stop closes the underlying fsnotify watcher. It is safe to call multiple
// times; subsequent calls are no-ops.
func (w *Watcher) Stop() error {
	var err error
	w.stopOnce.Do(func() {
		err = w.fsw.Close()
	})
	return err
}
