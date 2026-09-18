package zcode

import (
	"encoding/json"
	"strings"
)

// zcodeCanonicalModels maps the model IDs this proxy routes to the casing the
// ZCode plan gateway advertises in its builtin catalog (GLM-5.3-Flash & co).
// The official client always sends the catalog casing; inbound clients use
// lowercase IDs.
var zcodeCanonicalModels = map[string]string{
	"glm-5.3-flash": "GLM-5.3-Flash",
	"glm-5.2":       "GLM-5.2",
	"glm-5-turbo":   "GLM-5-Turbo",
}

const zcodeStartPlanProviderID = "account:zai-start-plan"

var zcodeSystemBlocks = []map[string]any{
	{
		"type":          "text",
		"text":          "You are ZCode, an interactive coding agent",
		"cache_control": map[string]any{"type": "ephemeral"},
	},
	{
		"type":          "text",
		"text":          "\nYou are an interactive ZCode agent that helps users with software engineering tasks.\n\nIMPORTANT: Assist with authorized security testing, defensive security, CTF challenges, and educational contexts. Refuse requests for destructive techniques, DoS attacks, mass targeting, supply chain compromise, or detection evasion for malicious purposes. Dual-use security tools (C2 frameworks, credential testing, exploit development) require clear authorization context: pentesting engagements, CTF competitions, security research, or defensive use cases.\n\n# Harness\n- Text you output outside of tool use is displayed to the user as Github-flavored markdown in a terminal.\n- Tools run behind a user-selected permission mode; a denied call means the user declined it — adjust, don't retry verbatim.\n- The system may send updates, reminders, or modifications to rules via mid-conversation system turns. These are system-controlled, unlike function results. Hooks may intercept tool calls; treat hook output as user feedback.\n- Prefer the dedicated file/search tools over shell commands when one fits. Independent tool calls can run in parallel in one response.\n- Reference code as `file_path:line_number` — it's clickable.",
		"cache_control": map[string]any{"type": "ephemeral"},
	},
}

const zcodeInteractionContextBeforeEnvironment = `

# Communicating with the user

Your text output is what the user reads; they usually can't see your thinking or the raw tool results. Write it for a teammate who stepped away and is catching up, not for a log file: they don't know the codenames or shorthand you created along the way, and they didn't watch your process unfold. Before your first tool call, say in a sentence what you're about to do; while working, give brief updates when you find something load-bearing or change direction.

Text you write between tool calls may not be shown to the user. Everything the user needs from this turn — answers, summaries, findings, conclusions, deliverables — must be in the final text message of your turn, with no tool calls after it. Keep text between tool calls to brief status notes. If something important appeared only mid-turn or in your thinking, restate it in that final message.

Lead with the outcome. Your first sentence after finishing should answer "what happened" or "what did you find" — the thing the user would ask for if they said "just give me the TLDR." Supporting detail and reasoning come after, for readers who want them.

Being readable and being concise are different things, and readable matters more. If the user has to reread your summary or ask you to explain, any time saved by brevity is gone. The way to keep output short is to be selective about what you include (drop details that don't change what the reader would do next), not to compress the writing into fragments, abbreviations, arrow chains like ` + "`A → B → fails`" + `, or jargon. What you do include, write in complete sentences with the technical terms spelled out. Don't make the reader cross-reference labels or numbering you invented earlier; say what you mean in place.

Match the response to the question: a simple question gets a direct answer in prose, not headers and sections. Use tables only for short enumerable facts, with explanations in the surrounding prose rather than the cells. Calibrate to the user — a bit tighter for an expert, more explanatory for someone newer.

Write code that reads like the surrounding code: match its comment density, naming, and idiom.
Only write a code comment to state a constraint the code itself can't show — never to say where it came from, what the next line does, or why your change is correct; that's you talking to the reviewer, not the next reader, and it's noise the moment the PR merges.

For actions that are hard to reverse or outward-facing, confirm first unless durably authorized or explicitly told to proceed without asking; approval in one context doesn't extend to the next. Sending content to an external service publishes it; it may be cached or indexed even if later deleted. Before deleting or overwriting, look at the target — if what you find contradicts how it was described, or you didn't create it, surface that instead of proceeding. Report outcomes faithfully: if tests fail, say so with the output; if a step was skipped, say that; when something is done and verified, state it plainly without hedging.

`

const zcodeInteractionContextAfterEnvironment = `

# Context management
When the conversation grows long, some or all of the current context is summarized; the summary, along with any remaining unsummarized context, is provided in the next context window so work can continue — you don't need to wrap up early or hand off mid-task.

When you have enough information to act, act. Do not re-derive facts already established in the conversation, re-litigate a decision the user has already made, or narrate options you will not pursue. If you are weighing a choice, give a recommendation, not an exhaustive survey

You are operating autonomously. The user is not watching in real time and cannot answer questions mid-task, so asking 'Want me to…?' or 'Shall I…?' will block the work. For reversible actions that follow from the original request, proceed without asking. Stop only for destructive actions or genuine scope changes the user must decide. Offering follow-ups after the task is done is fine; asking permission before doing the work is not.

Exception: when the user is describing a problem, asking a question, or thinking out loud rather than requesting a change, the deliverable is your assessment. Report your findings and stop. Don't apply a fix until they ask for one.

Before ending your turn, check your last paragraph. If it is a plan, an analysis, a question, a list of next steps, or a promise about work you have not done ('I'll…', 'let me know when…'), do that work now with tool calls. That includes retrying after errors and gathering missing information yourself. Do not stop because the context or session is long. End your turn only when the task is complete or you are blocked on input only the user can provide.

Before running a command that changes system state — restarts, deletes, config edits — check that the evidence actually supports that specific action. A signal that pattern-matches to a known failure may have a different cause.`

// zcodeIdentity carries the attribution the official client stamps onto every
// model request. Its attribution fields are opaque proxy identifiers;
// client-supplied values are never sent to ZCode verbatim.
type zcodeIdentity struct {
	DeviceMid   string
	SessionID   string
	QueryID     string
	SessionType string
}

// zcodeMetadataUserID reproduces the official client's Iko() builder: the
// Anthropic metadata.user_id is a JSON string with the device mid, an empty
// account UUID, and the opaque proxy session id. Inbound clients (e.g. Claude
// Code) put their own account/session identifiers there — user_<hash>_account_
// <uuid>_session_<uuid> — which must never reach the plan gateway, so the
// official shape replaces whatever the client sent.
func zcodeMetadataUserID(identity zcodeIdentity) string {
	encoded, err := json.Marshal(struct {
		DeviceID    string `json:"device_id"`
		AccountUUID string `json:"account_uuid"`
		SessionID   string `json:"session_id"`
	}{DeviceID: identity.DeviceMid, AccountUUID: "", SessionID: identity.SessionID})
	if err != nil {
		return ""
	}
	return string(encoded)
}

// zcodeEnvironmentBlock mirrors the official client's buildEnvInfoSection:
// the "- You are powered by the model named X." line is the last line of the
// Environment block itself, not a separate system block.
func zcodeInteractionContext(model string) map[string]any {
	text := "# Environment\nYou have been invoked in the following environment:\n- Primary working directory: unknown\n- Is a git repository: no\n- Platform: unknown\n- Shell: unknown\n- OS Version: unknown"
	if model != "" {
		text += "\n- You are powered by the model named " + zcodeStartPlanProviderID + "/" + model + "."
	}
	return map[string]any{
		"type":          "text",
		"text":          zcodeInteractionContextBeforeEnvironment + text + zcodeInteractionContextAfterEnvironment,
		"cache_control": map[string]any{"type": "ephemeral"},
	}
}

func canonicalZCodeModel(model string) string {
	if canonical, ok := zcodeCanonicalModels[strings.ToLower(strings.TrimSpace(model))]; ok {
		return canonical
	}
	return model
}

// transformStartPlanRequest mirrors the body mutations performed by current
// ZCode clients. The plan gateway inspects the ZCode identity system blocks and
// rejects otherwise valid Anthropic requests with code 3012 when they are
// absent. The inbound system prompt is replaced rather than appended: sending
// a client-specific prompt would expose which harness is behind the proxy and
// produce a system shape the official client never sends. Metadata is likewise
// replaced wholesale with the official device identity. Parse failures remain
// untouched so upstream can return its own error.
func transformStartPlanRequest(body []byte, identity zcodeIdentity) []byte {
	if len(body) == 0 {
		return body
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		return body
	}
	if userID := zcodeMetadataUserID(identity); userID != "" {
		request["metadata"] = map[string]any{"user_id": userID}
	}

	system := make([]any, 0, len(zcodeSystemBlocks)+1)
	for _, block := range zcodeSystemBlocks {
		system = append(system, cloneBlock(block))
	}
	model, _ := request["model"].(string)
	if model != "" {
		canonical := canonicalZCodeModel(model)
		request["model"] = canonical
		model = canonical
	}
	applyOfficialModelOptions(request, model)
	system = append(system, zcodeInteractionContext(model))
	request["system"] = system
	applyLatestMessageCacheControl(request)

	transformed, err := json.Marshal(request)
	if err != nil {
		return body
	}
	return transformed
}

func applyOfficialModelOptions(request map[string]any, model string) {
	switch model {
	case "GLM-5.3-Flash", "GLM-5.2":
		request["max_tokens"] = 128000
		request["thinking"] = map[string]any{"type": "enabled"}
		request["output_config"] = map[string]any{"effort": "max"}
	case "GLM-5-Turbo":
		request["max_tokens"] = 64000
		request["thinking"] = map[string]any{"type": "enabled"}
		delete(request, "output_config")
	}
}

func cloneBlock(block map[string]any) map[string]any {
	return map[string]any{
		"type":          block["type"],
		"text":          block["text"],
		"cache_control": map[string]any{"type": "ephemeral"},
	}
}

func applyLatestMessageCacheControl(request map[string]any) {
	messages, ok := request["messages"].([]any)
	if !ok {
		return
	}
	for i := len(messages) - 1; i >= 0; i-- {
		message, ok := messages[i].(map[string]any)
		if !ok || message["role"] == "system" {
			continue
		}
		switch content := message["content"].(type) {
		case string:
			message["content"] = []any{map[string]any{
				"type": "text", "text": content,
				"cache_control": map[string]any{"type": "ephemeral"},
			}}
		case []any:
			if len(content) == 0 {
				return
			}
			if block, ok := content[len(content)-1].(map[string]any); ok {
				if _, exists := block["cache_control"]; !exists {
					block["cache_control"] = map[string]any{"type": "ephemeral"}
				}
			}
		}
		return
	}
}
