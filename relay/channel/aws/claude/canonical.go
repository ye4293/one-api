package aws

import "strings"

var awsModelRegionPrefixes = []string{"us.", "eu.", "apac.", "ap.", "global."}

func StripAwsRegionPrefix(modelID string) string {
	for _, p := range awsModelRegionPrefixes {
		if rest, ok := strings.CutPrefix(modelID, p); ok {
			return rest
		}
	}
	return modelID
}

func CanonicalAwsModelID(name string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		return ""
	}
	if id, ok := AwsModelIDMap[s]; ok {
		return id
	}
	stripped := strings.TrimSuffix(StripAwsRegionPrefix(s), "-thinking")
	if id, ok := AwsModelIDMap[stripped]; ok {
		return id
	}
	return stripped
}

var awsCanonicalToDisplay = buildAwsDisplayNameMap()

func buildAwsDisplayNameMap() map[string]string {
	m := make(map[string]string, len(AwsModelIDMap))
	for short, id := range AwsModelIDMap {
		incumbent, exists := m[id]
		if !exists || preferAwsDisplayName(short, incumbent) {
			m[id] = short
		}
	}
	return m
}

func preferAwsDisplayName(candidate, incumbent string) bool {
	cThinking := strings.HasSuffix(candidate, "-thinking")
	iThinking := strings.HasSuffix(incumbent, "-thinking")
	if cThinking != iThinking {
		return !cThinking
	}
	return candidate < incumbent
}

func AwsDisplayModelName(name string) string {
	if short, ok := awsCanonicalToDisplay[CanonicalAwsModelID(name)]; ok {
		return short
	}
	return strings.TrimSpace(name)
}
