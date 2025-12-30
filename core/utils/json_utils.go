package utils

// SanitizeJSONSchema 递归清洗 JSON Schema，移除 Google Gemini 不支持的字段
func SanitizeJSONSchema(schema map[string]interface{}) {
	if schema == nil {
		return
	}

	// 移除不支持的字段
	delete(schema, "default")
	delete(schema, "minLength")
	delete(schema, "maxLength")
	delete(schema, "additionalProperties")
	delete(schema, "title")
	delete(schema, "examples")
	delete(schema, "$schema")

	// 修正 type 字段 (Gemini 不支持数组类型的 type，如 ["string", "null"])
	if typeVal, ok := schema["type"]; ok {
		if typeArr, ok := typeVal.([]interface{}); ok {
			// 简单策略：取第一个非 null 的类型，通常是 string/number 等
			// 如果是 ["string", "null"] -> "string"
			for _, t := range typeArr {
				if s, ok := t.(string); ok && s != "null" {
					schema["type"] = s
					break
				}
			}
		}
	}

	// 递归处理 properties
	if props, ok := schema["properties"].(map[string]interface{}); ok {
		for _, v := range props {
			if child, ok := v.(map[string]interface{}); ok {
				SanitizeJSONSchema(child)
			}
		}
	}

	// 递归处理 items (数组)
	if items, ok := schema["items"].(map[string]interface{}); ok {
		SanitizeJSONSchema(items)
	}
}
