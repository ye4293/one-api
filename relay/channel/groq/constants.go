package groq

import "github.com/songquanpeng/one-api/relay/model"

// https://console.groq.com/docs/models

var ModelList = []string{
	"gemma-7b-it",
	"llama2-7b-2048",
	"llama2-70b-4096",
	"mixtral-8x7b-32768",
	"llama3-8b-8192",
	"llama3-70b-8192",
}

var ModelDetails = []model.APIModel{}
