package config

import (
	"context"
	"path/filepath"
	"sync"

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

// handleEvent processes an fsnotify event. Only write and create events for
// the watched file trigger a reload.
func (w *Watcher) handleEvent(event fsnotify.Event) {
	// Normalise both paths to compare reliably across platforms.
	eventPath, err1 := filepath.Abs(event.Name)
	watchPath, err2 := filepath.Abs(w.filePath)
	if err1 != nil || err2 != nil || eventPath != watchPath {
		return
	}

	if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
		return
	}

	w.reload()
}

// reload re-parses the workflow file. On success the config is atomically
// swapped; on failure the previous config is retained and a warning is logged.
func (w *Watcher) reload() {
	cfg, err := ParseWorkflow(w.filePath)
	if err != nil {
		log.Warn("failed to reload workflow config, keeping previous", "path", w.filePath, "err", err)
		return
	}

	w.mu.Lock()
	w.config = cfg
	w.mu.Unlock()

	log.Info("workflow config reloaded", "path", w.filePath)
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
