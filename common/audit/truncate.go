package audit

// truncate 将 s 截断到 limitKB 千字节以内，返回截断后字符串及是否发生截断。
func truncate(s string, limitKB int) (string, bool) {
	limit := limitKB * 1024
	if len(s) <= limit {
		return s, false
	}
	return s[:limit], true
}
