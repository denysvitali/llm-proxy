// Package all blank-imports every backend implementation so their init()
// functions register into the backend registry. Binaries and tests import
// this single package instead of listing each provider; adding a backend
// means writing its package and one line here.
package all

import (
	_ "github.com/denysvitali/llm-proxy/internal/backend/abliteration"
	_ "github.com/denysvitali/llm-proxy/internal/backend/apodex"
	_ "github.com/denysvitali/llm-proxy/internal/backend/codex"
	_ "github.com/denysvitali/llm-proxy/internal/backend/grok"
	_ "github.com/denysvitali/llm-proxy/internal/backend/nous"
	_ "github.com/denysvitali/llm-proxy/internal/backend/opencode"
	_ "github.com/denysvitali/llm-proxy/internal/backend/opencodego"
	_ "github.com/denysvitali/llm-proxy/internal/backend/openrouter"
	_ "github.com/denysvitali/llm-proxy/internal/backend/venice"
	_ "github.com/denysvitali/llm-proxy/internal/backend/workbuddy"
)
