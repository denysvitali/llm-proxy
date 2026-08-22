package openai

import "encoding/json"

type ResponsesRequest struct {
	Model             string          `json:"model"`
	Instructions      string          `json:"instructions,omitempty"`
	Input             json.RawMessage `json:"input"`
	Tools             json.RawMessage `json:"tools,omitempty"`
	ToolChoice        json.RawMessage `json:"tool_choice,omitempty"`
	ParallelToolCalls bool            `json:"parallel_tool_calls,omitempty"`
	Reasoning         *Reasoning      `json:"reasoning,omitempty"`
	Store             bool            `json:"store"`
	Stream            bool            `json:"stream"`
	Include           []string        `json:"include,omitempty"`
	PromptCacheKey    string          `json:"prompt_cache_key,omitempty"`
	Text              *TextControls   `json:"text,omitempty"`
	MaxOutputTokens   int             `json:"max_output_tokens,omitempty"`
	Temperature       *float64        `json:"temperature,omitempty"`
	TopP              *float64        `json:"top_p,omitempty"`
}

type Reasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type TextControls struct {
	Verbosity string          `json:"verbosity,omitempty"`
	Format    json.RawMessage `json:"format,omitempty"`
}

type InputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
	Output    string          `json:"output,omitempty"`
}

type InputContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type FunctionTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

type FunctionToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type Response struct {
	ID                string          `json:"id"`
	Object            string          `json:"object,omitempty"`
	Status            string          `json:"status,omitempty"`
	Model             string          `json:"model,omitempty"`
	Output            []OutputItem    `json:"output"`
	Usage             Usage           `json:"usage"`
	IncompleteDetails json.RawMessage `json:"incomplete_details,omitempty"`
}

type OutputItem struct {
	ID        string          `json:"id,omitempty"`
	Type      string          `json:"type"`
	Role      string          `json:"role,omitempty"`
	Status    string          `json:"status,omitempty"`
	Content   []OutputContent `json:"content,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
}

type OutputContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens,omitempty"`
}

type StreamEvent struct {
	Type        string      `json:"type"`
	Delta       string      `json:"delta,omitempty"`
	OutputIndex int         `json:"output_index,omitempty"`
	Item        *OutputItem `json:"item,omitempty"`
	Response    *Response   `json:"response,omitempty"`
	Error       *ErrorBody  `json:"error,omitempty"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
