package cohere

var ModelList = []string{"command", "command-light", "command-nightly", "command-light-nightly", "command-r", "command-r-plus"}

func init() {
	num := len(ModelList)
	for i := 0; i < num; i++ {
		ModelList = append(ModelList, ModelList[i]+"-internet")
	}
}
