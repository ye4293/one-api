package replicate

import "time"

type FluxReplicate struct {
	Input FluxReplicateInput `json:"input"`
}

type FluxReplicateInput struct {
	Prompt        string `json:"prompt" binding:"required"`
	AspectRatio   string `json:"aspect_ratio,omitempty"`
	NumOutputs    int    `json:"num_outputs,omitempty" binding:"min=1,max=4"`
	Seed          int    `json:"seed,omitempty"`
	OutputFormat  string `json:"output_format,omitempty"`
	OutputQuality int    `json:"output_quality,omitempty" binding:"min=0,max=100"`
}

type ReplicateResponse struct {
	ID          string         `json:"id"`
	Model       string         `json:"model"`
	Version     string         `json:"version"`
	Input       ReplicateInput `json:"input"`
	Logs        string         `json:"logs"`
	Output      interface{}    `json:"output"`
	DataRemoved bool           `json:"data_removed"`
	Error       interface{}    `json:"error"`
	Status      string         `json:"status"`
	CreatedAt   string         `json:"created_at"`
	URLs        ReplicateURLs  `json:"urls"`
}

type ReplicateInput struct {
	AspectRatio string `json:"aspect_ratio"`
	NumOutputs  int    `json:"num_outputs"`
	Prompt      string `json:"prompt"`
}

type ReplicateURLs struct {
	Cancel string `json:"cancel"`
	Get    string `json:"get"`
}

type FinalRequestResponse struct {
	ID          string      `json:"id"`
	Model       string      `json:"model"`
	Version     string      `json:"version"`
	Input       Input       `json:"input"`
	Logs        string      `json:"logs"`
	Output      []string    `json:"output"`
	DataRemoved bool        `json:"data_removed"`
	Error       interface{} `json:"error"`
	Status      string      `json:"status"`
	CreatedAt   time.Time   `json:"created_at"`
	StartedAt   time.Time   `json:"started_at"`
	CompletedAt time.Time   `json:"completed_at"`
	URLs        URLs        `json:"urls"`
	Metrics     Metrics     `json:"metrics"`
}

type Input struct {
	AspectRatio   string `json:"aspect_ratio"`
	NumOutputs    int    `json:"num_outputs"`
	OutputFormat  string `json:"output_format"`
	OutputQuality int    `json:"output_quality"`
	Prompt        string `json:"prompt"`
}

type URLs struct {
	Cancel string `json:"cancel"`
	Get    string `json:"get"`
}

type Metrics struct {
	ImageCount  int     `json:"image_count"`
	PredictTime float64 `json:"predict_time"`
}
