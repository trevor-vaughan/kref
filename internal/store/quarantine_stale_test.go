package store

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestQuarantineStale(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	fresh := QuarantineItem{CreatedAt: now.Add(-1 * time.Hour)}
	old := QuarantineItem{CreatedAt: now.Add(-8 * 24 * time.Hour)}
	if QuarantineStale(fresh, now) {
		t.Fatal("a 1h-old item must not be stale")
	}
	if !QuarantineStale(old, now) {
		t.Fatal("an 8d-old item must be stale")
	}
	// exact boundary: >= threshold is stale
	edge := QuarantineItem{CreatedAt: now.Add(-QuarantineStaleAfter)}
	if !QuarantineStale(edge, now) {
		t.Fatal("an item exactly at the threshold must be stale")
	}
}

var _ = Describe("WarnQuarantineDue", func() {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	It("stays quiet with an empty queue", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		due, err := s.WarnQuarantineDue(now, 24*time.Hour)
		Expect(err).NotTo(HaveOccurred())
		Expect(due).To(BeFalse(), "empty queue must not be due")
	})

	It("stays quiet right after a warning was recorded (throttle)", func() {
		s, err := Init(gitRepo(), "T", "t@e.com")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		Expect(s.MarkQuarantineWarned(now)).To(Succeed())
		due, err := s.WarnQuarantineDue(now, 24*time.Hour)
		Expect(err).NotTo(HaveOccurred())
		Expect(due).To(BeFalse(), "recently warned must not be due")
	})
})
