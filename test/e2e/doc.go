// Package e2e contains end-to-end tests that drive the compiled `kref` binary as
// a subprocess against a fully isolated environment (private HOME / keyring /
// git config, plus bare-repo remotes for sync).
//
// The tests are gated behind the `e2e` build tag so the fast unit loop
// (`task test`) stays quick. Run them with `task test:e2e`.
package e2e
