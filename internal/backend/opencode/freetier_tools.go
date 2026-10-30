package opencode

import (
	_ "embed"
	"encoding/json"
)

//go:embed freetier_tools.json
var freeTierToolsJSON string

var freeTierToolDefs map[string]json.RawMessage

func init() {
	var tools []json.RawMessage
	if err := json.Unmarshal([]byte(freeTierToolsJSON), &tools); err != nil {
		return
	}
	freeTierToolDefs = make(map[string]json.RawMessage, len(tools))
	for _, t := range tools {
		var probe struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		if json.Unmarshal(t, &probe) == nil && probe.Function.Name != "" {
			freeTierToolDefs[probe.Function.Name] = t
		}
	}
}
