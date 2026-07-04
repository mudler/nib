package specialist

import "net/http"

// Client calls LocalAI's OpenAI-compatible specialist endpoints (transcription,
// vision description). baseURL is the LocalAI base, e.g. http://localhost:8080/v1.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{baseURL: baseURL, apiKey: apiKey, http: http.DefaultClient}
}
