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
}

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
	{
		"type":          "text",
		"text":          zcodeDesktopContext,
		"cache_control": map[string]any{"type": "ephemeral"},
	},
}

// zcodeDesktopContext is the "ZCode Desktop Context" system section the
// official desktop client injects (buildDesktopContextSection) when the
// server-side desktopContextPrompt rollout is enabled — which
// /api/v1/client/configs currently reports for this account
// ({"config_version":"desktop-sp-v1","enabled":true}). The proxy claims the
// desktop persona (X-Title "Z Code@electron"), so it must carry the section
// too; verbatim text, joined with single newlines ("" array entries become
// blank lines), positioned before the Environment block like the official
// section ordering.
const zcodeDesktopContext = "# ZCode Desktop Context\n\n### Files & URLs\n- Return local web URLs as Markdown links (e.g., [label](http://127.0.0.1:8080)).\n- File should be an absolute path or include the workspace folder segment so it can be resolved relative to the workspace.\n- Unless otherwise specified, return local file references as Markdown links (e.g., [name.md](/absolute/path/to/name.md)).\n\n### Inline Code Comments\n- Use the ::code-comment{...} directive when you need to attach feedback directly to specific code lines.\n- Emit one directive per inline comment; emit none when there are no actionable inline comments.\n- Required attributes: title (short label), body (one-paragraph explanation), file (path to the file).\n- Optional attributes: start, end (1-based line numbers), priority (0-3).\n- file should be an absolute path or include the workspace folder segment so it can be resolved relative to the workspace.\n- Keep line ranges tight; end defaults to start.\n- Example: ::code-comment{title=\"[P2] Off-by-one\" body=\"Loop iterates past the end when length is 0.\" file=\"/path/to/foo.ts\" start=10 end=11 priority=2}"

// zcodeEnvironmentBlock mirrors the official client's buildEnvInfoSection:
// the "- You are powered by the model named X." line is the last line of the
// Environment block itself, not a separate system block.
func zcodeEnvironmentBlock(model string) map[string]any {
	text := "# Environment\nYou have been invoked in the following environment:\n- Primary working directory: unknown\n- Is a git repository: no\n- Platform: unknown\n- Shell: unknown\n- OS Version: unknown"
	if model != "" {
		text += "\n- You are powered by the model named " + model + "."
	}
	return map[string]any{
		"type":          "text",
		"text":          text,
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
// absent. Parse failures remain untouched so upstream can return its own error.
func transformStartPlanRequest(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		return body
	}

	system := make([]any, 0, len(zcodeSystemBlocks)+2)
	for _, block := range zcodeSystemBlocks {
		system = append(system, cloneBlock(block))
	}
	model, _ := request["model"].(string)
	if model != "" {
		canonical := canonicalZCodeModel(model)
		request["model"] = canonical
		model = canonical
	}
	system = append(system, zcodeEnvironmentBlock(model))
	switch existing := request["system"].(type) {
	case string:
		if existing != "" {
			system = append(system, map[string]any{"type": "text", "text": existing})
		}
	case []any:
		system = append(system, existing...)
	}
	request["system"] = system
	applyLatestMessageCacheControl(request)

	transformed, err := json.Marshal(request)
	if err != nil {
		return body
	}
	return transformed
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
