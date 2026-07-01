package audit

import (
	"encoding/json"
	"net/http"
	"strings"
)

const redactedValue = "***REDACTED***"

func redactHeaders(h http.Header, redactSet map[string]struct{}) http.Header {
	out := http.Header{}
	for k, vs := range h {
		if _, ok := redactSet[strings.ToLower(k)]; ok {
			out[k] = []string{redactedValue}
			continue
		}
		cp := make([]string, len(vs))
		copy(cp, vs)
		out[k] = cp
	}
	return out
}

func headersToJSON(h http.Header) string {
	b, err := json.Marshal(h)
	if err != nil {
		return "{}"
	}
	return string(b)
}
