package audit

// truncate 将 s 截断到 limitKB 千字节以内，返回截断后字符串及是否发生截断。
// limitKB <= 0 表示不限制。
func truncate(s string, limitKB int) (string, bool) {
	if limitKB <= 0 {
		return s, false
	}
	limit := limitKB * 1024
	if len(s) <= limit {
		return s, false
	}
	return s[:limit], true
}

// removeField 从 fields 中移除所有值等于 name 的元素。
func removeField(fields []string, name string) []string {
	out := fields[:0]
	for _, f := range fields {
		if f != name {
			out = append(out, f)
		}
	}
	return out
}
