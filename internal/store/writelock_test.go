package store

import (
	"bytes"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
)

var _ = Describe("withWriteLock", func() {
	It("runs fn and releases the lock", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())

		ran := false
		Expect(s.withWriteLock(func() error { ran = true; return nil })).To(Succeed())
		Expect(ran).To(BeTrue())

		// Released: an external handle can take it immediately.
		fl := flock.New(s.writeLockPath())
		ok, err := fl.TryLock()
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(fl.Unlock()).To(Succeed())
	})

	It("retries then errors when the lock is held, emitting a user-facing notice", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())

		// Shrink the retry policy for a fast test; restore after.
		origR, origD := writeLockRetries, writeLockRetryDelay
		writeLockRetries, writeLockRetryDelay = 1, time.Millisecond
		DeferCleanup(func() { writeLockRetries, writeLockRetryDelay = origR, origD })

		var notices bytes.Buffer
		s.lockNotify = &notices

		// Ensure the lockfile directory exists before the external handle tries to open it.
		Expect(os.MkdirAll(filepath.Dir(s.writeLockPath()), 0o755)).To(Succeed())

		// Hold the lock from an external handle.
		held := flock.New(s.writeLockPath())
		ok, err := held.TryLock()
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		DeferCleanup(func() { _ = held.Unlock() })

		err = s.withWriteLock(func() error { return nil })
		Expect(err).To(MatchError(ContainSubstring("could not acquire the write lock")))
		Expect(notices.String()).To(ContainSubstring("waiting… (1/1)"))
	})

	It("routes entity writes through the lock (AddComment errors when held)", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierShared, "note", "n", "b")
		Expect(err).NotTo(HaveOccurred())

		origR, origD := writeLockRetries, writeLockRetryDelay
		writeLockRetries, writeLockRetryDelay = 0, time.Millisecond
		DeferCleanup(func() { writeLockRetries, writeLockRetryDelay = origR, origD })

		// Ensure the lockfile dir exists before the external handle opens it.
		Expect(os.MkdirAll(filepath.Dir(s.writeLockPath()), 0o755)).To(Succeed())
		held := flock.New(s.writeLockPath())
		ok, err := held.TryLock()
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		DeferCleanup(func() { _ = held.Unlock() })

		_, err = s.AddComment(id, "human", "hi", false, "")
		Expect(err).To(MatchError(ContainSubstring("could not acquire the write lock")))
	})

	It("routes Pull through the lock", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())

		origR, origD := writeLockRetries, writeLockRetryDelay
		writeLockRetries, writeLockRetryDelay = 0, time.Millisecond
		DeferCleanup(func() { writeLockRetries, writeLockRetryDelay = origR, origD })

		// Ensure the lockfile dir exists before the external handle opens it.
		Expect(os.MkdirAll(filepath.Dir(s.writeLockPath()), 0o755)).To(Succeed())
		held := flock.New(s.writeLockPath())
		ok, err := held.TryLock()
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		DeferCleanup(func() { _ = held.Unlock() })

		// Pull wraps its whole body in the lock, so a held lock is reported
		// before the tier/remote checks — even a remoteless tier surfaces it.
		err = s.Pull(entry.TierShared)
		Expect(err).To(MatchError(ContainSubstring("could not acquire the write lock")))
	})

	It("keeps concurrent writes from losing an op", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		id, err := s.Add(entry.TierShared, "note", "n", "b")
		Expect(err).NotTo(HaveOccurred())

		done := make(chan error, 2)
		for range 2 {
			go func() { _, e := s.AddComment(id, "human", "c", false, ""); done <- e }()
		}
		Expect(<-done).NotTo(HaveOccurred())
		Expect(<-done).NotTo(HaveOccurred())

		snap, err := s.Get(id)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Comments).To(HaveLen(2))
	})
})
