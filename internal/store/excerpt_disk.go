package store

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"syscall"

	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/repository"

	"github.com/trevor-vaughan/kref/internal/entry"
)

const excerptCacheVersion uint = 2

// diskCache is one tier's persisted state: the lean excerpts plus the ref map
// they were built from. Both live in one file so a single atomic rename swaps
// them together — "Refs present => Excerpts complete and consistent" holds by
// construction, and a killed writer leaves the prior file intact.
type diskCache struct {
	Version  uint
	Excerpts map[entity.Id]Excerpt
	Refs     refMap
}

func cacheFile(t entry.Tier) string { return path.Join("cache", "excerpt-"+string(t)) }

func loadDiskCache(ls repository.LocalStorage, t entry.Tier) (*diskCache, error) {
	f, err := ls.Open(cacheFile(t))
	if err != nil {
		return nil, err // includes not-exist -> caller rebuilds
	}
	defer func() { _ = f.Close() }()
	var dc diskCache
	if err := gob.NewDecoder(f).Decode(&dc); err != nil {
		return nil, err // torn/corrupt -> caller rebuilds
	}
	if dc.Version != excerptCacheVersion {
		return nil, fmt.Errorf("excerpt cache version %d != %d", dc.Version, excerptCacheVersion)
	}
	return &dc, nil
}

// saveDiskCache stamps the current version and writes atomically.
func saveDiskCache(ls repository.LocalStorage, t entry.Tier, dc *diskCache) error {
	dc.Version = excerptCacheVersion
	return writeDiskCacheRaw(ls, t, dc)
}

// writeDiskCacheRaw serializes dc as-is (version not touched) via temp+rename.
// Rename on the same filesystem is atomic; durability across power loss is
// best-effort, which is acceptable because the cache is fully rebuildable.
func writeDiskCacheRaw(ls repository.LocalStorage, t entry.Tier, dc *diskCache) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(dc); err != nil {
		return err
	}
	if err := ls.MkdirAll("cache", 0o755); err != nil {
		return err
	}
	final := cacheFile(t)
	tmp := final + ".tmp"
	f, err := ls.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		_ = f.Close()
		_ = ls.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = ls.Remove(tmp)
		return err
	}
	return ls.Rename(tmp, final)
}

func lockFile(t entry.Tier) string { return cacheFile(t) + ".lock" }

func writeLockPID(ls repository.LocalStorage, t entry.Tier, pid int) error {
	if err := ls.MkdirAll("cache", 0o755); err != nil {
		return err
	}
	f, err := ls.Create(lockFile(t))
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte(strconv.Itoa(pid))); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// pidAlive reports whether a process with pid exists. Signal 0 performs error
// checking without delivering a signal (ESRCH => gone).
func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// acquireBuildLock returns true if this process now holds the tier's build
// lock. A lock owned by a dead pid is reclaimed. Reads never call this; only
// rebuild/refresh writers do, so at most one writer touches a tier at a time.
func acquireBuildLock(ls repository.LocalStorage, t entry.Tier) (bool, error) {
	f, err := ls.Open(lockFile(t))
	if err == nil {
		buf, rerr := io.ReadAll(io.LimitReader(f, 32))
		_ = f.Close()
		if rerr != nil {
			return false, rerr
		}
		if pid, perr := strconv.Atoi(string(buf)); perr == nil && pidAlive(pid) {
			return false, nil // live holder
		}
		// stale (unparseable or dead pid) -> reclaim
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := writeLockPID(ls, t, os.Getpid()); err != nil {
		return false, err
	}
	return true, nil
}

func releaseBuildLock(ls repository.LocalStorage, t entry.Tier) error {
	err := ls.Remove(lockFile(t))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
