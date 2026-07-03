package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// env builds a getenv lookup backed by a fixed map, so path tests need no real
// environment.
func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

var _ = Describe("completionInstallPath", func() {
	bash := completionShell{name: "bash", filename: "kref"}
	zsh := completionShell{name: "zsh", filename: "_kref"}
	fish := completionShell{name: "fish", filename: "kref.fish"}
	ps := completionShell{name: "powershell", filename: ""}

	It("uses XDG_DATA_HOME for bash when set", func() {
		got, err := completionInstallPath(bash, "", env(map[string]string{"XDG_DATA_HOME": "/x"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("/x/bash-completion/completions/kref"))
	})

	It("falls back to HOME/.local/share for bash", func() {
		got, err := completionInstallPath(bash, "", env(map[string]string{"HOME": "/h"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("/h/.local/share/bash-completion/completions/kref"))
	})

	It("resolves zsh site-functions under XDG_DATA_HOME fallback", func() {
		got, err := completionInstallPath(zsh, "", env(map[string]string{"HOME": "/h"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("/h/.local/share/zsh/site-functions/_kref"))
	})

	It("uses XDG_CONFIG_HOME for fish when set", func() {
		got, err := completionInstallPath(fish, "", env(map[string]string{"XDG_CONFIG_HOME": "/c"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("/c/fish/completions/kref.fish"))
	})

	It("falls back to HOME/.config for fish", func() {
		got, err := completionInstallPath(fish, "", env(map[string]string{"HOME": "/h"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("/h/.config/fish/completions/kref.fish"))
	})

	It("honors an absolute dir override", func() {
		got, err := completionInstallPath(zsh, "/tmp/z", env(map[string]string{"HOME": "/h"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("/tmp/z/_kref"))
	})

	It("expands a leading tilde in the override", func() {
		got, err := completionInstallPath(bash, "~/c", env(map[string]string{"HOME": "/h"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("/h/c/kref"))
	})

	It("keeps a relative override relative", func() {
		got, err := completionInstallPath(fish, "rel/dir", env(map[string]string{"HOME": "/h"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("rel/dir/kref.fish"))
	})

	It("errors for a shell with no standard directory", func() {
		_, err := completionInstallPath(ps, "", env(map[string]string{"HOME": "/h"}))
		Expect(err).To(HaveOccurred())
	})

	It("errors when neither XDG nor HOME is set", func() {
		_, err := completionInstallPath(bash, "", env(map[string]string{}))
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("kref completion (print)", func() {
	It("prints a bash V2 script", func() {
		Expect(run("completion", "bash")).To(ContainSubstring("# bash completion V2 for kref"))
	})

	It("prints a zsh script", func() {
		Expect(run("completion", "zsh")).To(ContainSubstring("#compdef kref"))
	})

	It("prints a fish script", func() {
		Expect(run("completion", "fish")).To(ContainSubstring("# fish completion for kref"))
	})

	It("prints a powershell script", func() {
		Expect(run("completion", "powershell")).To(ContainSubstring("# powershell completion for kref"))
	})

	It("accepts --no-descriptions on a shell sub-command", func() {
		Expect(run("completion", "fish", "--no-descriptions")).To(ContainSubstring("# fish completion for kref"))
	})

	It("keeps dynamic completion working (__complete unaffected)", func() {
		Expect(run("__complete", "ad")).To(ContainSubstring("add"))
	})

	It("surfaces --install with a concrete example in the parent help", func() {
		out := run("completion", "-h")
		Expect(out).To(ContainSubstring("Examples:"))
		Expect(out).To(ContainSubstring("kref completion zsh --install"))
	})
})

var _ = Describe("kref show id completion", func() {
	// setup returns a repo holding one entry and its full id, so tests can assert
	// on the 12-char short id the completion offers.
	setup := func() (string, string) {
		GinkgoHelper()
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--kind", "note", "--title", "Auth Design", "--body", "x", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		return dir, added.ID
	}

	It("offers the short id with the updated date and title as a description", func() {
		dir, id := setup()
		out := run("--dir", dir, "__complete", "show", "")
		Expect(out).To(MatchRegexp(id[:12] + `\t\d{4}-\d{2}-\d{2}  Auth Design`))
		Expect(out).To(ContainSubstring("ShellCompDirectiveNoFileComp"))
		Expect(out).To(ContainSubstring("ShellCompDirectiveKeepOrder"))
	})

	It("filters offered ids by the typed prefix", func() {
		dir, id := setup()
		Expect(run("--dir", dir, "__complete", "show", id[:4])).To(ContainSubstring(id[:12]))
		Expect(run("--dir", dir, "__complete", "show", "zzzzzz")).NotTo(ContainSubstring(id[:12]))
	})

	It("defers to file completion when the word looks like a path", func() {
		dir, id := setup()
		out := run("--dir", dir, "__complete", "show", "./")
		Expect(out).NotTo(ContainSubstring(id[:12]))
		Expect(out).To(ContainSubstring(":0")) // ShellCompDirectiveDefault → shell file completion
	})
})

var _ = Describe("kref completion --install", func() {
	It("writes the bash script to --dir and reports the path", func() {
		dir := GinkgoT().TempDir()
		out := run("completion", "bash", "--install", "--dir", dir)
		Expect(out).To(ContainSubstring("Installed completion to"))
		data, err := os.ReadFile(filepath.Join(dir, "kref"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("# bash completion V2 for kref"))
	})

	It("writes the zsh script as _kref and prints the fpath note", func() {
		dir := GinkgoT().TempDir()
		out := run("completion", "zsh", "--install", "--dir", dir)
		Expect(out).To(ContainSubstring("fpath"))
		_, err := os.Stat(filepath.Join(dir, "_kref"))
		Expect(err).NotTo(HaveOccurred())
	})

	It("writes the fish script as kref.fish", func() {
		dir := GinkgoT().TempDir()
		run("completion", "fish", "--install", "--dir", dir)
		_, err := os.Stat(filepath.Join(dir, "kref.fish"))
		Expect(err).NotTo(HaveOccurred())
	})

	It("creates a missing parent directory", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "nested", "deep")
		run("completion", "fish", "--install", "--dir", dir)
		_, err := os.Stat(filepath.Join(dir, "kref.fish"))
		Expect(err).NotTo(HaveOccurred())
	})

	It("is idempotent (second run produces an identical file)", func() {
		dir := GinkgoT().TempDir()
		run("completion", "bash", "--install", "--dir", dir)
		first, err := os.ReadFile(filepath.Join(dir, "kref"))
		Expect(err).NotTo(HaveOccurred())
		run("completion", "bash", "--install", "--dir", dir)
		second, err := os.ReadFile(filepath.Join(dir, "kref"))
		Expect(err).NotTo(HaveOccurred())
		Expect(second).To(Equal(first))
	})

	It("returns a JSON result under --json", func() {
		dir := GinkgoT().TempDir()
		out := run("completion", "fish", "--install", "--dir", dir, "--json")
		var got struct {
			Shell     string `json:"shell"`
			Path      string `json:"path"`
			Installed bool   `json:"installed"`
		}
		Expect(json.Unmarshal([]byte(out), &got)).To(Succeed())
		Expect(got.Shell).To(Equal("fish"))
		Expect(got.Installed).To(BeTrue())
		Expect(got.Path).To(HaveSuffix("kref.fish"))
	})

	It("rejects --dir without --install", func() {
		var out bytes.Buffer
		c := newRootCmd()
		c.SetOut(&out)
		c.SetErr(&out)
		c.SetArgs([]string{"completion", "bash", "--dir", "/tmp/x"})
		Expect(c.Execute()).To(HaveOccurred())
	})

	It("does not offer --install on powershell", func() {
		var out bytes.Buffer
		c := newRootCmd()
		c.SetOut(&out)
		c.SetErr(&out)
		c.SetArgs([]string{"completion", "powershell", "--install"})
		Expect(c.Execute()).To(HaveOccurred())
	})
})

var _ = Describe("kref fav rm completion", func() {
	// Favorites live in the user config, so isolate XDG_CONFIG_HOME/HOME.
	isolate := func() string {
		home := GinkgoT().TempDir()
		GinkgoT().Setenv("HOME", home)
		GinkgoT().Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "T", "--email", "t@e.com")
		return dir
	}

	It("suggests a helpful hint when there are no favorites to remove", func() {
		dir := isolate()
		out := run("--dir", dir, "__complete", "fav", "rm", "")
		Expect(out).To(ContainSubstring("no favorites yet"))
	})

	It("offers the favorite names to remove", func() {
		dir := isolate()
		created := run("--dir", dir, "new", "--title", "Runbook", "--body", "b", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(created), &a)).To(Succeed())
		run("--dir", dir, "fav", "add", a.ID, "todo")

		out := run("--dir", dir, "__complete", "fav", "rm", "")
		Expect(out).To(ContainSubstring("todo"))
		Expect(out).To(ContainSubstring("ShellCompDirectiveNoFileComp"))
	})
})
