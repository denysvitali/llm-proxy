package translate

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

// responseNamespaceTool records the exact client identity behind a flat wire
// name, including top-level tools that would otherwise collide with namespaces.
type responseNamespaceTool struct {
	Qualified string
	Namespace string
	Name      string
}

// responsesTools assigns deterministic, collision-free names. Every request and
// response path uses this same map; unqualified response names are never guessed.
func responsesTools(body []byte) ([]responsesFunctionTool, map[string]responseNamespaceTool, error) {
	var envelope struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, nil, err
	}
	var tools []responsesFunctionTool
	var identities []responseNamespaceTool
	seen := make(map[[2]string]bool)
	preferred := make(map[string]int)
	add := func(raw json.RawMessage, namespace string) error {
		var tool responsesFunctionTool
		if err := decodeContentPart(raw, &tool); err != nil {
			return fmt.Errorf("unsupported function tool fields: %w", err)
		}
		if tool.Type != "function" || tool.Name == "" {
			return fmt.Errorf("translated tools require type function and a non-empty name; custom and server-executed tools require a native route")
		}
		identity := [2]string{namespace, tool.Name}
		if seen[identity] {
			return fmt.Errorf("duplicate tool identity %q in namespace %q", tool.Name, namespace)
		}
		seen[identity] = true
		name := tool.Name
		if namespace != "" {
			name = qualifyResponsesToolName(namespace, name)
		}
		preferred[name]++
		identities = append(identities, responseNamespaceTool{Qualified: name, Namespace: namespace, Name: tool.Name})
		tools = append(tools, tool)
		return nil
	}
	for _, raw := range envelope.Tools {
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			return nil, nil, fmt.Errorf("decode tool: %w", err)
		}
		if header.Type != "namespace" {
			if err := add(raw, ""); err != nil {
				return nil, nil, err
			}
			continue
		}
		var namespace struct {
			Type  string            `json:"type"`
			Name  string            `json:"name"`
			Tools []json.RawMessage `json:"tools"`
		}
		if err := decodeContentPart(raw, &namespace); err != nil || namespace.Name == "" || len(namespace.Tools) == 0 {
			return nil, nil, fmt.Errorf("namespace requires name, non-empty tools, and no unsupported fields")
		}
		for _, raw := range namespace.Tools {
			if err := add(raw, namespace.Name); err != nil {
				return nil, nil, err
			}
		}
	}
	mapped := make(map[string]responseNamespaceTool, len(tools))
	for i, identity := range identities {
		name := identity.Qualified
		if preferred[name] != 1 || !validFlatToolName(name) {
			for salt := 0; ; salt++ {
				digest := sha256.Sum256(mustMarshal([]any{identity.Namespace, identity.Name, salt}))
				name = fmt.Sprintf("llmp_tool_%x", digest[:16])
				if _, exists := mapped[name]; !exists && preferred[name] == 0 {
					break
				}
			}
		}
		identity.Qualified = name
		mapped[name] = identity
		tools[i].Name = name
	}
	return tools, mapped, nil
}

func validFlatToolName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, c := range name {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}

func qualifyResponsesToolName(namespace, name string) string {
	if strings.HasSuffix(namespace, "__") {
		return namespace + name
	}
	return namespace + "__" + name
}

func responseNamespaceMap(body []byte) (map[string]responseNamespaceTool, error) {
	_, identities, err := responsesTools(body)
	return identities, err
}

func responsesWireToolName(name, namespace string, identities map[string]responseNamespaceTool) (string, error) {
	for wire, identity := range identities {
		if identity.Name == name && identity.Namespace == namespace {
			return wire, nil
		}
	}
	// Historical top-level calls need not still be offered as tools, but may not
	// impersonate an alias assigned to a different identity in this request.
	if namespace == "" && name != "" {
		if _, collision := identities[name]; !collision {
			return name, nil
		}
	}
	return "", fmt.Errorf("tool %q in namespace %q has no unambiguous declaration", name, namespace)
}

func responsesWireToolChoice(choice any, identities map[string]responseNamespaceTool) (any, error) {
	value, ok := choice.(map[string]any)
	if !ok {
		return choice, nil
	}
	name, _ := value["name"].(string)
	namespace, _ := value["namespace"].(string)
	wire, err := responsesWireToolName(name, namespace, identities)
	if err != nil {
		return nil, err
	}
	return map[string]any{"type": "function", "name": wire}, nil
}

func restoreResponsesToolName(name string, identities map[string]responseNamespaceTool) (string, string) {
	if tool, ok := identities[name]; ok {
		return tool.Name, tool.Namespace
	}
	return name, ""
}
