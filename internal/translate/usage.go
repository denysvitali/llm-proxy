package translate

import (
	"errors"
	"math"
)

func validateResponsesUsage(usage responsesUsage) error {
	cached, created := usage.InputTokensDetails.CachedTokens, usage.InputTokensDetails.CacheCreationTokens
	reasoning := usage.OutputTokensDetails.ReasoningTokens
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.TotalTokens < 0 || cached < 0 || created < 0 || reasoning < 0 ||
		cached > usage.InputTokens || created > usage.InputTokens-cached || reasoning > usage.OutputTokens ||
		usage.InputTokens > math.MaxInt64-usage.OutputTokens || usage.InputTokens > math.MaxInt || usage.OutputTokens > math.MaxInt {
		return errors.New("invalid upstream token usage")
	}
	return nil
}

func validateChatUsage(usage chatUsageOut) error {
	// Validate before filling an omitted total, which requires addition.
	canonical := responsesUsage{InputTokens: usage.PromptTokens, OutputTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens}
	if usage.PromptTokensDetails != nil {
		canonical.InputTokensDetails = *usage.PromptTokensDetails
	}
	if usage.CompletionTokensDetails != nil {
		canonical.OutputTokensDetails = *usage.CompletionTokensDetails
	}
	return validateResponsesUsage(canonical)
}

func validateAnthropicUsage(usage AnthropicUsage) error {
	counts := []int{usage.InputTokens, usage.CacheReadInputTokens, usage.CacheCreationInputTokens, usage.OutputTokens}
	var total int64
	for _, count := range counts {
		if count < 0 || int64(count) > math.MaxInt64-total {
			return errors.New("invalid upstream token usage")
		}
		total += int64(count)
	}
	if usage.OutputTokensDetails != nil && (usage.OutputTokensDetails.ReasoningTokens < 0 || usage.OutputTokensDetails.ReasoningTokens > int64(usage.OutputTokens)) {
		return errors.New("invalid upstream token usage")
	}
	return nil
}

func (event anthropicEvent) validateUsage(previous AnthropicUsage) error {
	switch event.Type {
	case "message_start":
		if event.Message != nil {
			return validateAnthropicUsage(event.Message.Usage)
		}
	case "message_delta":
		if event.Usage != nil {
			event.Usage.apply(&previous)
			return validateAnthropicUsage(previous)
		}
	}
	return nil
}

// Responses usage is the common accounting shape. OpenAI input totals include
// cache reads/writes; Messages input_tokens excludes both cache categories.
func anthropicUsageToResponses(usage AnthropicUsage) responsesUsage {
	input := int64(usage.InputTokens) + int64(usage.CacheReadInputTokens) + int64(usage.CacheCreationInputTokens)
	out := responsesUsage{
		InputTokens:  input,
		OutputTokens: int64(usage.OutputTokens),
		TotalTokens:  input + int64(usage.OutputTokens),
		InputTokensDetails: cachedTokensDetail{
			CachedTokens:        int64(usage.CacheReadInputTokens),
			CacheCreationTokens: int64(usage.CacheCreationInputTokens),
		},
	}
	if usage.OutputTokensDetails != nil {
		out.OutputTokensDetails = *usage.OutputTokensDetails
	}
	return out
}

func responsesUsageToAnthropic(usage responsesUsage) AnthropicUsage {
	out := AnthropicUsage{
		InputTokens:              int(usage.InputTokens - usage.InputTokensDetails.CachedTokens - usage.InputTokensDetails.CacheCreationTokens),
		OutputTokens:             int(usage.OutputTokens),
		CacheReadInputTokens:     int(usage.InputTokensDetails.CachedTokens),
		CacheCreationInputTokens: int(usage.InputTokensDetails.CacheCreationTokens),
	}
	if usage.OutputTokensDetails.ReasoningTokens != 0 {
		detail := usage.OutputTokensDetails
		out.OutputTokensDetails = &detail
	}
	return out
}

func chatUsageToResponses(usage chatUsageOut) responsesUsage {
	out := responsesUsage{
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
		TotalTokens:  usage.TotalTokens,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.InputTokens + out.OutputTokens
	}
	if usage.PromptTokensDetails != nil {
		out.InputTokensDetails = *usage.PromptTokensDetails
	}
	if usage.CompletionTokensDetails != nil {
		out.OutputTokensDetails = *usage.CompletionTokensDetails
	}
	return out
}

func responsesUsageToChat(usage responsesUsage) chatUsageOut {
	out := chatUsageOut{
		PromptTokens:            usage.InputTokens,
		CompletionTokens:        usage.OutputTokens,
		TotalTokens:             usage.TotalTokens,
		PromptTokensDetails:     &usage.InputTokensDetails,
		CompletionTokensDetails: &usage.OutputTokensDetails,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	return out
}

// Message deltas contain cumulative counts, not increments. Pointer fields
// distinguish an omitted counter from an explicit reset to zero.
type anthropicUsageDelta struct {
	InputTokens              *int                   `json:"input_tokens"`
	OutputTokens             *int                   `json:"output_tokens"`
	CacheReadInputTokens     *int                   `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int                   `json:"cache_creation_input_tokens"`
	OutputTokensDetails      *reasoningTokensDetail `json:"output_tokens_details"`
}

func (delta anthropicUsageDelta) apply(usage *AnthropicUsage) {
	if delta.InputTokens != nil {
		usage.InputTokens = *delta.InputTokens
	}
	if delta.OutputTokens != nil {
		usage.OutputTokens = *delta.OutputTokens
	}
	if delta.CacheReadInputTokens != nil {
		usage.CacheReadInputTokens = *delta.CacheReadInputTokens
	}
	if delta.CacheCreationInputTokens != nil {
		usage.CacheCreationInputTokens = *delta.CacheCreationInputTokens
	}
	if delta.OutputTokensDetails != nil {
		usage.OutputTokensDetails = delta.OutputTokensDetails
	}
}
