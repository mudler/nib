// Package systemone is a classify.Classifier for the SystemOne API
// (kev-compatible), which LocalAI, vllm.cpp and kev serve at POST
// {base_url}/systemone.
package systemone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mudler/nib/classify"
)

func init() {
	classify.Register("systemone", func(baseURL, apiKey, model string, timeout time.Duration) classify.Classifier {
		return New(baseURL, apiKey, model, timeout)
	})
}

// Client talks to one SystemOne server.
type Client struct {
	url     string
	apiKey  string
	model   string
	timeout time.Duration
	http    *http.Client
}

// New returns a client for the server at baseURL (e.g. http://host:8080/v1).
func New(baseURL, apiKey, model string, timeout time.Duration) *Client {
	return &Client{
		url:     strings.TrimRight(baseURL, "/") + "/systemone",
		apiKey:  apiKey,
		model:   model,
		timeout: timeout,
		http:    &http.Client{},
	}
}

type wireQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions,omitempty"`
	Criteria     any    `json:"criteria,omitempty"`
}

type wireRequest struct {
	State     string                  `json:"state"`
	Questions map[string]wireQuestion `json:"questions"`
	Model     string                  `json:"model,omitempty"`
}

type wireEntity struct {
	Text       string  `json:"text"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Confidence float64 `json:"confidence"`
}

type wireAnswer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul"`
	Entities      []wireEntity       `json:"entities"`
	Choice        *string            `json:"choice"`
	Confidence    *float64           `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
	Score         *float64           `json:"score"`
}

type wireResponse struct {
	Answers map[string]wireAnswer `json:"answers"`
}

// Classify implements classify.Classifier.
func (c *Client) Classify(ctx context.Context, state string, qs map[string]classify.Question) (map[string]classify.Answer, error) {
	req := wireRequest{State: state, Questions: make(map[string]wireQuestion, len(qs)), Model: c.model}
	for id, q := range qs {
		wq := wireQuestion{Type: q.Type, Instructions: q.Instructions}
		switch q.Type {
		case classify.TypeChoice:
			crit := make(map[string]string, len(q.Choices))
			for k, v := range q.Choices {
				crit[k] = v
			}
			wq.Criteria = crit
		case classify.TypeScore:
			wq.Criteria = q.Levels
		}
		req.Questions[id] = wq
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		hreq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("systemone: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("systemone: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	var wr wireResponse
	if err := json.NewDecoder(resp.Body).Decode(&wr); err != nil {
		return nil, fmt.Errorf("systemone: decode response: %w", err)
	}
	out := make(map[string]classify.Answer, len(qs))
	for id, q := range qs {
		wa, ok := wr.Answers[id]
		if !ok {
			return nil, fmt.Errorf("systemone: no answer for question %q", id)
		}
		a, err := toAnswer(q.Type, wa)
		if err != nil {
			return nil, fmt.Errorf("systemone: question %q: %w", id, err)
		}
		out[id] = a
	}
	return out, nil
}

func toAnswer(typ string, wa wireAnswer) (classify.Answer, error) {
	if wa.Type != "" && wa.Type != typ {
		return classify.Answer{}, fmt.Errorf("answered as %q, asked as %q", wa.Type, typ)
	}
	a := classify.Answer{Probabilities: wa.Probabilities}
	if wa.Confidence != nil {
		a.Confidence = *wa.Confidence
	}
	switch typ {
	case classify.TypeNoul:
		if wa.Noul == nil {
			return a, fmt.Errorf("missing noul value")
		}
		a.Noul = *wa.Noul
		for _, e := range wa.Entities {
			a.Entities = append(a.Entities, classify.Entity{Text: e.Text, Start: e.Start, End: e.End, Confidence: e.Confidence})
		}
	case classify.TypeChoice:
		if wa.Choice == nil {
			return a, fmt.Errorf("missing choice")
		}
		a.Choice = *wa.Choice
	case classify.TypeScore:
		if wa.Score == nil {
			return a, fmt.Errorf("missing score")
		}
		a.Score = *wa.Score
	}
	return a, nil
}
