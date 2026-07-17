package tracker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// fileLock provides cross-process advisory locking using flock(2). The local
// board is mutated by the daemon and the `contrabass board`/`team` CLIs from
// separate processes sharing one BoardDir; the in-process sync.Mutex alone
// cannot stop two processes from racing the manifest read-modify-write and
// minting duplicate issue IDs. Mirrors internal/team.FileLock, kept
// package-local to avoid a tracker→team dependency.
type fileLock struct {
	path string
	file *os.File
}

func newFileLock(path string) *fileLock {
	return &fileLock{path: path + ".lock"}
}

// Lock acquires an exclusive lock, blocking until available.
func (l *fileLock) Lock() error {
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}

	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open lock file %s: %w", l.path, err)
	}

	l.file = f
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		l.file = nil
		return fmt.Errorf("flock %s: %w", l.path, err)
	}

	return nil
}

// TryLock acquires an exclusive lock without waiting. It returns false, nil
// when another process currently holds the lock.
func (l *fileLock) TryLock() (bool, error) {
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("create lock dir: %w", err)
	}

	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return false, fmt.Errorf("open lock file %s: %w", l.path, err)
	}

	l.file = f
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		l.file = nil
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return false, nil
		}
		return false, fmt.Errorf("flock %s: %w", l.path, err)
	}

	return true, nil
}

// Unlock releases the lock.
func (l *fileLock) Unlock() error {
	if l.file == nil {
		return nil
	}

	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
		_ = l.file.Close()
		l.file = nil
		return fmt.Errorf("funlock %s: %w", l.path, err)
	}

	err := l.file.Close()
	l.file = nil
	if err != nil {
		return fmt.Errorf("close lock file %s: %w", l.path, err)
	}

	return nil
}
