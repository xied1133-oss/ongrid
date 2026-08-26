package skill

import "encoding/json"

// BuildSchema returns the JSON Schema describing an Executor's params
// envelope. If the Executor implements RawSchemaProvider that schema
// wins; otherwise the framework derives one from Metadata.Params.
//
// Returns the raw schema bytes; callers can json.Unmarshal into a map
// to add extra fields (e.g. skill_bridge prepends device_id for
// edge-scoped skills).
func BuildSchema(e Executor) (json.RawMessage, error) {
	if rp, ok := e.(RawSchemaProvider); ok {
		raw := rp.JSONSchema()
		if len(raw) > 0 {
			return raw, nil
		}
	}
	return json.Marshal(e.Metadata().Params.ToJSONSchema())
}

// ToJSONSchema converts a ParamSchema into the JSON Schema shape
// OpenAI's function-calling tools use. The output is a "object" schema
// with a "properties" map and a "required" list — the same shape
// internal/pkg/llm.ToolSchema expects.
//
// Type mapping:
//   string  -> "string"
//   int     -> "integer"
//   float   -> "number"
//   bool    -> "boolean"
//   duration-> "string" (caller serialises Go time.Duration via Duration.String())
//   enum    -> "string" with "enum" array
func (s ParamSchema) ToJSONSchema() map[string]any {
	props := map[string]any{}
	required := []string{}
	for _, p := range s {
		entry := map[string]any{
			"description": p.Desc,
		}
		switch p.Type {
		case "string", "duration":
			entry["type"] = "string"
		case "int":
			entry["type"] = "integer"
		case "float":
			entry["type"] = "number"
		case "bool":
			entry["type"] = "boolean"
		case "enum":
			entry["type"] = "string"
			if len(p.Enum) > 0 {
				entry["enum"] = p.Enum
			}
		case "array":
			entry["type"] = "array"
			items := map[string]any{}
			switch p.ItemType {
			case "string", "duration":
				items["type"] = "string"
			case "int":
				items["type"] = "integer"
			case "float":
				items["type"] = "number"
			case "bool":
				items["type"] = "boolean"
			case "enum":
				items["type"] = "string"
				if len(p.Enum) > 0 {
					items["enum"] = p.Enum
				}
			default:
				items["type"] = "string"
			}
			entry["items"] = items
		default:
			entry["type"] = "string"
		}
		if p.Default != nil {
			entry["default"] = p.Default
		}
		props[p.Name] = entry
		if p.Required {
			required = append(required, p.Name)
		}
	}
	out := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}
