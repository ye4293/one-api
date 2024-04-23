package util

func GetMappedModelName(modelName string, mapping map[string]string) (string, string, bool) {
	if mapping == nil {
		return modelName, modelName, false
	}
	mappedModelName := mapping[modelName]
	if mappedModelName != "" {
		return mappedModelName, modelName, true
	}
	return modelName, modelName, false
}
