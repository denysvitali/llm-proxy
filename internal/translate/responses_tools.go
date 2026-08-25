package translate

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// responseNamespaceTool records the client-facing identity of a function
// nested inside a Responses namespace. Chat and Anthropic APIs only accept a
// flat tool list, so the proxy qualifies the wire name and restores this
// metadata when it builds the Responses response again.
type responseNamespaceTool struct {
	Qualified string
	Namespace string
	Name      string
}

// responsesTools decodes the Responses tool extension used by Codex. Ordinary
// function tools pass through unchanged; namespace tools are flattened and
// their child names are qualified so they remain unique on flat APIs.
func responsesTools(body []byte) ([]responsesFunctionTool, map[string]responseNamespaceTool, error) {
	var envelope struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, nil, err
	}

	tools := make([]responsesFunctionTool, 0, len(envelope.Tools))
	namespaces := make(map[string]responseNamespaceTool)
	for _, raw := range envelope.Tools {
		var header struct {
			Type  string            `json:"type"`
			Name  string            `json:"name"`
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			return nil, nil, fmt.Errorf("decode tool: %w", err)
		}
		if header.Type != "namespace" {
			var tool responsesFunctionTool
			if err := json.Unmarshal(raw, &tool); err != nil {
				return nil, nil, fmt.Errorf("decode function tool: %w", err)
			}
			tools = append(tools, tool)
			continue
		}
		if header.Name == "" {
			return nil, nil, errors.New("namespace has no name")
		}
		for _, childRaw := range header.Tools {
			var child responsesFunctionTool
			if err := json.Unmarshal(childRaw, &child); err != nil {
				return nil, nil, fmt.Errorf("decode namespace child: %w", err)
			}
			if child.Name == "" {
				return nil, nil, fmt.Errorf("namespace %q has a child without a valid name", header.Name)
			}
			childName := child.Name
			qualified := qualifyResponsesToolName(header.Name, child.Name)
			child.Name = qualified
			tools = append(tools, child)
			namespaces[qualified] = responseNamespaceTool{
				Qualified: qualified,
				Namespace: header.Name,
				Name:      childName,
			}
		}
	}
	return tools, namespaces, nil
}

func qualifyResponsesToolName(namespace, name string) string {
	if strings.HasSuffix(namespace, "__") {
		return namespace + name
	}
	return namespace + "__" + name
}

func responseNamespaceMap(body []byte) (map[string]responseNamespaceTool, error) {
	_, namespaces, err := responsesTools(body)
	return namespaces, err
}

func restoreResponsesToolName(name string, namespaces map[string]responseNamespaceTool) (string, string) {
	if tool, ok := namespaces[name]; ok {
		return tool.Name, tool.Namespace
	}
	var match responseNamespaceTool
	for _, tool := range namespaces {
		if tool.Name != name {
			continue
		}
		if match.Name != "" {
			// The same child name can exist in multiple namespaces; without
			// the qualified wire name there is no safe restoration.
			return name, ""
		}
		match = tool
	}
	if match.Name != "" {
		return match.Name, match.Namespace
	}
	return name, ""
}
