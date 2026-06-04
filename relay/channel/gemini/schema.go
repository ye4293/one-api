package gemini

import "strings"

// Gemini 的 functionDeclarations[].parameters 仅接受 OpenAPI 3.0 Schema 子集，
// 而 OpenAI 兼容客户端发来的是完整 JSON Schema。本文件负责把后者清洗成前者，
// 剔除 Gemini 不认识的字段（如 $schema、additionalProperties、exclusiveMinimum），
// 并对少数构造做等价降级（const→enum、oneOf→anyOf 等），避免上游返回 400。

const geminiFunctionSchemaMaxDepth = 64

// geminiOpenAPISchemaAllowedFields 是 Gemini Schema 支持的字段白名单，
// 不在表内的键一律丢弃。
var geminiOpenAPISchemaAllowedFields = map[string]struct{}{
	"anyOf":            {},
	"default":          {},
	"description":      {},
	"enum":             {},
	"example":          {},
	"format":           {},
	"items":            {},
	"maxItems":         {},
	"maxLength":        {},
	"maxProperties":    {},
	"maximum":          {},
	"minItems":         {},
	"minLength":        {},
	"minProperties":    {},
	"minimum":          {},
	"nullable":         {},
	"pattern":          {},
	"properties":       {},
	"propertyOrdering": {},
	"required":         {},
	"title":            {},
	"type":             {},
}

// cleanFunctionParameters 递归清洗 OpenAI JSON Schema，输出 Gemini 可接受的 Schema。
func cleanFunctionParameters(params any) any {
	return cleanFunctionParametersWithDepth(params, 0)
}

func cleanFunctionParametersWithDepth(params any, depth int) any {
	if params == nil {
		return nil
	}
	if depth >= geminiFunctionSchemaMaxDepth {
		return cleanFunctionParametersShallow(params)
	}

	switch v := params.(type) {
	case map[string]any:
		cleaned := make(map[string]any, len(v))
		// 1. 白名单过滤：仅保留 Gemini 支持的字段
		for k, val := range v {
			if _, ok := geminiOpenAPISchemaAllowedFields[k]; ok {
				cleaned[k] = val
			}
		}
		// 2. 降级转换：把 Gemini 不支持但有等价表达的构造补进白名单字段
		applyGeminiSchemaDowngrades(v, cleaned)
		// 3. 类型规整：小写 type → 大写，联合类型 null → nullable
		normalizeGeminiSchemaTypeAndNullable(cleaned)
		// 4. format 过滤：删除当前 type 不支持的 format 值
		normalizeGeminiSchemaFormat(cleaned)

		// 5. 递归处理嵌套结构
		if props, ok := cleaned["properties"].(map[string]any); ok && props != nil {
			cleanedProps := make(map[string]any, len(props))
			for name, val := range props {
				cleanedProps[name] = cleanFunctionParametersWithDepth(val, depth+1)
			}
			cleaned["properties"] = cleanedProps
		}
		if items, ok := cleaned["items"].(map[string]any); ok && items != nil {
			cleaned["items"] = cleanFunctionParametersWithDepth(items, depth+1)
		}
		// OpenAPI 元组式 items（数组）Gemini 不支持，取首个避免被拒
		if itemsArr, ok := cleaned["items"].([]any); ok && len(itemsArr) > 0 {
			cleaned["items"] = cleanFunctionParametersWithDepth(itemsArr[0], depth+1)
		}
		// anyOf 特殊处理：OpenAI 常用 anyOf:[实际类型, {type:null}] 表达可空字段，
		// 但 Gemini 要求每个 schema 节点都有 type，纯 anyOf 节点会被拒。
		// 因此剥离 null 分支、转为 nullable，并在仅剩单个分支时上提合并。
		if rawAnyOf, ok := cleaned["anyOf"].([]any); ok && len(rawAnyOf) > 0 {
			nullable := false
			branches := make([]any, 0, len(rawAnyOf))
			for _, b := range rawAnyOf {
				if bm, ok := b.(map[string]any); ok && isNullSchemaBranch(bm) {
					nullable = true
					continue
				}
				branches = append(branches, cleanFunctionParametersWithDepth(b, depth+1))
			}
			if nullable {
				cleaned["nullable"] = true
			}
			switch len(branches) {
			case 0:
				// 只有 null 分支，anyOf 已无意义
				delete(cleaned, "anyOf")
			case 1:
				// 唯一实际分支上提到当前节点，使其携带 type
				delete(cleaned, "anyOf")
				if only, ok := branches[0].(map[string]any); ok {
					for k, v := range only {
						if _, exists := cleaned[k]; !exists {
							cleaned[k] = v
						}
					}
				} else {
					cleaned["anyOf"] = branches
				}
			default:
				cleaned["anyOf"] = branches
			}
		}
		return cleaned

	case []any:
		cleanedArr := make([]any, len(v))
		for i, item := range v {
			cleanedArr[i] = cleanFunctionParametersWithDepth(item, depth+1)
		}
		return cleanedArr

	default:
		return params
	}
}

// cleanFunctionParametersShallow 在超过深度上限时调用：仅保留白名单标量字段，
// 截断 properties/items/anyOf，防止深层嵌套耗尽栈。
func cleanFunctionParametersShallow(params any) any {
	switch v := params.(type) {
	case map[string]any:
		cleaned := make(map[string]any, len(v))
		for k, val := range v {
			if _, ok := geminiOpenAPISchemaAllowedFields[k]; ok {
				cleaned[k] = val
			}
		}
		applyGeminiSchemaDowngrades(v, cleaned)
		normalizeGeminiSchemaTypeAndNullable(cleaned)
		normalizeGeminiSchemaFormat(cleaned)
		delete(cleaned, "properties")
		delete(cleaned, "items")
		delete(cleaned, "anyOf")
		return cleaned
	case []any:
		return []any{}
	default:
		return params
	}
}

// applyGeminiSchemaDowngrades 读取原始节点 src 中 Gemini 不支持但可等价表达的构造，
// 写入清洗后节点 dst 的白名单字段。仅在 dst 尚无对应字段时补写，避免覆盖原值。
func applyGeminiSchemaDowngrades(src, dst map[string]any) {
	// const → enum:[const]
	if cv, ok := src["const"]; ok {
		if _, has := dst["enum"]; !has {
			dst["enum"] = []any{cv}
		}
	}
	// exclusiveMinimum(数值形式) → minimum；draft-04 的 bool 形式无等价，丢弃
	if ev, ok := src["exclusiveMinimum"]; ok && isNumber(ev) {
		if _, has := dst["minimum"]; !has {
			dst["minimum"] = ev
		}
	}
	// exclusiveMaximum(数值形式) → maximum
	if ev, ok := src["exclusiveMaximum"]; ok && isNumber(ev) {
		if _, has := dst["maximum"]; !has {
			dst["maximum"] = ev
		}
	}
	// oneOf → anyOf（损失排他性，Gemini 仅支持 anyOf）
	if ov, ok := src["oneOf"].([]any); ok && len(ov) > 0 {
		if _, has := dst["anyOf"]; !has {
			dst["anyOf"] = ov
		}
	}
}

func isNumber(v any) bool {
	switch v.(type) {
	case float64, float32, int, int64, int32:
		return true
	default:
		return false
	}
}

// isNullSchemaBranch 判断一个 anyOf 分支是否为纯 null 类型（{type:"null"} 或 {type:["null"]}），
// 这类分支用于表达可空，应折叠为父节点的 nullable:true。
func isNullSchemaBranch(m map[string]any) bool {
	t, ok := m["type"]
	if !ok {
		return false
	}
	switch tv := t.(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(tv), "null")
	case []any:
		if len(tv) == 0 {
			return false
		}
		for _, x := range tv {
			s, ok := x.(string)
			if !ok || !strings.EqualFold(strings.TrimSpace(s), "null") {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// normalizeGeminiSchemaTypeAndNullable 将 JSON Schema 的小写 type 规整为 Gemini 大写枚举，
// 并把联合类型中的 "null" 拆成 nullable:true。
func normalizeGeminiSchemaTypeAndNullable(schema map[string]any) {
	rawType, ok := schema["type"]
	if !ok || rawType == nil {
		return
	}

	normalize := func(t string) (norm string, isNull bool) {
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "object":
			return "OBJECT", false
		case "array":
			return "ARRAY", false
		case "string":
			return "STRING", false
		case "integer":
			return "INTEGER", false
		case "number":
			return "NUMBER", false
		case "boolean":
			return "BOOLEAN", false
		case "null":
			return "", true
		default:
			return t, false
		}
	}

	switch t := rawType.(type) {
	case string:
		norm, isNull := normalize(t)
		if isNull {
			schema["nullable"] = true
			delete(schema, "type")
			return
		}
		schema["type"] = norm
	case []any:
		nullable := false
		var chosen string
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				continue
			}
			norm, isNull := normalize(s)
			if isNull {
				nullable = true
				continue
			}
			if chosen == "" {
				chosen = norm
			}
		}
		if nullable {
			schema["nullable"] = true
		}
		if chosen != "" {
			schema["type"] = chosen
		} else {
			delete(schema, "type")
		}
	}
}

// geminiFormatWhitelist 列出各类型下 Gemini 接受的 format 值，其余一律删除。
var geminiFormatWhitelist = map[string]map[string]struct{}{
	"STRING":  {"enum": {}, "date-time": {}},
	"NUMBER":  {"float": {}, "double": {}},
	"INTEGER": {"int32": {}, "int64": {}},
}

// normalizeGeminiSchemaFormat 删除与当前 type 不匹配的 format 值（如 string 的 "email"）。
func normalizeGeminiSchemaFormat(schema map[string]any) {
	format, ok := schema["format"].(string)
	if !ok || format == "" {
		return
	}
	typeStr, _ := schema["type"].(string)
	allowed, ok := geminiFormatWhitelist[typeStr]
	if !ok {
		delete(schema, "format")
		return
	}
	if _, ok := allowed[format]; !ok {
		delete(schema, "format")
	}
}
