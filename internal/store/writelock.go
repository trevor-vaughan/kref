package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// Write-lock retry policy. Package-level so tests can shrink them.
var (
	writeLockRetries    = 3
	writeLockRetryDelay = time.Second
)

// writeLockPath is the single per-repo lockfile, under .git/kref. One path for
// the whole store (all tiers) so every write serializes on the same lock.
func (s *Store) writeLockPath() string {
	return filepath.Join(s.repo.LocalStorage().Root(), "write.lock")
}

// lockNotifyWriter is where "waiting for the write lock" notices go.
func (s *Store) lockNotifyWriter() io.Writer {
	if s.lockNotify != nil {
		return s.lockNotify
	}
	return os.Stderr
}

// withWriteLock runs fn while holding the repo's exclusive write lock. The lock
// spans the whole read-modify-write (the lost-update window opens at Read, not
// at Commit). A fresh handle per call serializes both other processes and other
// in-process goroutines (each gets its own fd; the OS flock is exclusive), and
// releases on return and on process exit (advisory, fd-scoped) — no stale-lock
// bookkeeping.
//
// NOT reentrant: never call from within another withWriteLock on the same
// goroutine or it self-deadlocks. Guard: wrap only LEAF writes, never composites
// (Add -> AddWithContentType, Supersede -> AddLink+SetStatus,
// MigrateConfig -> Update) which call leaves sequentially.
func (s *Store) withWriteLock(fn func() error) error {
	path := s.writeLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("prepare write-lock dir: %w", err)
	}
	fl := flock.New(path)
	if err := s.acquireWriteLock(fl); err != nil {
		return err
	}
	defer func() { _ = fl.Unlock() }()
	return fn()
}

// acquireWriteLock tries to take the lock, retrying up to writeLockRetries times
// with a user-facing notice per wait, then returns an error.
func (s *Store) acquireWriteLock(fl *flock.Flock) error {
	for attempt := 0; ; attempt++ {
		ok, err := fl.TryLock()
		if err != nil {
			return fmt.Errorf("acquire write lock: %w", err)
		}
		if ok {
			return nil
		}
		if attempt >= writeLockRetries {
			return fmt.Errorf("could not acquire the write lock after %d attempts; "+
				"another kref process may be writing or stuck (lock: %s)", writeLockRetries, fl.Path())
		}
		fmt.Fprintf(s.lockNotifyWriter(),
			"kref: another process is writing; waiting… (%d/%d)\n", attempt+1, writeLockRetries)
		time.Sleep(writeLockRetryDelay)
	}
}
