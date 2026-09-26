package entry

// SigState is the signature verdict for an entry, derived from its ref tip.
//
// The vocabulary lives here rather than in internal/store because two packages
// need it and only one of them may import the store: internal/render decides
// what to show for each state, and internal/render must not depend on
// internal/store. Sharing the constants is what makes an unhandled state a
// rename the compiler catches, rather than a warning that silently stops
// appearing.
type SigState string

const (
	// SigUnresolved is the zero value: nobody asked for the verdict. It is a
	// distinct state from SigUnsigned — "we did not look" is not the same claim
	// as "there is nothing there". Resolution is opt-in (see
	// store.ListFilter.WithSigState), so most entries carry this.
	SigUnresolved SigState = ""
	// SigUnsigned means the commit carries no signature at all.
	SigUnsigned SigState = "unsigned"
	// SigGood means the signature verifies against a trusted key.
	SigGood SigState = "good"
	// SigBad means a signature is present but does not cover the content —
	// the commit was altered after signing.
	SigBad SigState = "bad"
	// SigUntrusted means a signature is present but cannot be vouched for:
	// an unknown, expired or revoked key, or verification kref could not run
	// at all. A missing allowed-signers file is by far the most common cause,
	// but an unknown or revoked key lands here too, so the state must be
	// reported as-is rather than explained away as setup.
	SigUntrusted SigState = "untrusted"
)

// SigReason is WHY a present signature could not be vouched for — the actionable
// half of an untrusted verdict. The state alone cannot carry it: "I do not have
// your key", "I have not vouched for your key", "my verification is not
// configured", "that key had expired" and "that key was revoked" all arrive as
// `untrusted`, and they ask for five different things. Only the last is a
// warning rather than a chore.
//
// Empty for good, unsigned and bad, where the state says everything there is to
// say.
type SigReason string

const (
	// SigReasonNone is the zero value: nothing to add to the state.
	SigReasonNone SigReason = ""
	// SigReasonKeyUnavailable means git has no copy of the signer's key at all
	// (git's %G? "E"). Openpgp only — import the key.
	SigReasonKeyUnavailable SigReason = "key-unavailable"
	// SigReasonKeyUntrusted means the key is known but nobody has vouched for it
	// (%G? "U"): absent from ssh allowed_signers, or holding no openpgp
	// ownertrust. This is what a new collaborator's entries look like.
	SigReasonKeyUntrusted SigReason = "key-untrusted"
	// SigReasonKeyExpired means the key's validity had lapsed (%G? "X"/"Y").
	SigReasonKeyExpired SigReason = "key-expired"
	// SigReasonKeyRevoked means the key was deliberately revoked (%G? "R") — the
	// one reason here that is a warning rather than a setup step. Note ssh
	// revocation surfaces as a BAD signature instead, so this is openpgp's path.
	SigReasonKeyRevoked SigReason = "key-revoked"
	// SigReasonUnverifiable means verification could not run at all: %G? "N" on
	// a commit we know carries a signature, most often ssh with no
	// gpg.ssh.allowedSignersFile set, or a gpg.format/gpg.program git cannot use.
	SigReasonUnverifiable SigReason = "unverifiable"
)

// Signed reports whether the state describes a commit that carries a signature,
// verifiable or not.
func (s SigState) Signed() bool { return s != SigUnresolved && s != SigUnsigned }

// Unsigned reports whether the state is a resolved verdict of "carries no
// signature".
//
// It is deliberately NOT the complement of Signed: SigUnresolved is neither. A
// filter that folded the two together would put entries nobody has verified into
// `kref list --unsigned`, where every row is a claim that the entry has no
// signature.
func (s SigState) Unsigned() bool { return s == SigUnsigned }
