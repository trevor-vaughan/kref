package mcpserver

import (
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// modelParam is embedded in every write tool's params. MCP carries no model
// identity — the initialize handshake describes the CLIENT (clientInfo), and the
// only Model field in the protocol is on a sampling RESULT, which is the server
// asking the client to generate, not the client telling us who it is. So the
// caller has to declare it, and the field is required rather than optional
// because an attribution that writes are free to omit is one that will be.
//
// The declaration is self-reported and unverifiable: a model states its own
// name. That is why the recorded actor keeps the model and the client distinct
// (see agentActor) — the client half comes from the protocol and is not the
// model's to choose.
type modelParam struct {
	Model string `json:"model"`
}

// unknownModel is the documented escape for a caller that genuinely cannot name
// itself. It exists so the honest answer is available: without it, a required
// field pressures a caller into inventing something plausible, and a fabricated
// model name is worse than a recorded absence.
const unknownModel = "unknown"

// agentActor composes the provenance actor for an MCP write: the caller's
// self-reported model, and the client the protocol reported, kept visibly
// separate ("claude-opus-5 via claude-code/2.1.4").
func agentActor(req *mcp.CallToolRequest, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		model = unknownModel
	}
	client := ""
	if req != nil && req.Session != nil {
		if ip := req.Session.InitializeParams(); ip != nil && ip.ClientInfo != nil {
			client = ip.ClientInfo.Name
			if v := ip.ClientInfo.Version; v != "" && client != "" {
				client += "/" + v
			}
		}
	}
	if client == "" {
		return model
	}
	return model + entry.ActorVia + client
}

// modelNote is appended to every write tool's description. MCP gives a server
// no way to learn which model is calling it, so the tool has to ask, and the
// answer is only as good as the instruction: name yourself, and say so plainly
// when you cannot, rather than producing a plausible-looking guess.
const modelNote = "REQUIRED: set model to your own model identifier (e.g. " +
	"\"claude-opus-5\") so the write is attributed to you rather than to the " +
	"repository owner. If you do not know it, pass \"" + unknownModel + "\" — " +
	"never invent or guess a model name."
