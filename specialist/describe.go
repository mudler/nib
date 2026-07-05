package specialist

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
)

// DataURI reads a file and returns a base64 data: URI (e.g. data:image/png;base64,...).
// Exported so the attachments package reuses it for native image parts.
func DataURI(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	ct := mime.TypeByExtension(filepath.Ext(path))
	if ct == "" {
		ct = "application/octet-stream"
	}
	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(b), nil
}

// Describe turns an image into text by sending it to a vision model and
// returning the model's textual description.
//
// It is the building block for the future Phase 2 `read_image` tool, which will
// let any agent (including a text-only one) pull an image into the conversation
// as model-generated text on demand. It is deliberately NOT wired into the
// attachment routing path: that path Blocks images on text-only models and nudges
// the user to switch to a vision model, rather than silently substituting a lossy
// AI-generated description for the actual image. (Audio, by contrast, auto-falls
// back to faithful transcription — see attachments.Route.) So Describe is
// intentionally ahead of its consumer in this branch.
func (c *Client) Describe(ctx context.Context, path, model, prompt string) (string, error) {
	if prompt == "" {
		prompt = "Describe this image in detail."
	}
	uri, err := DataURI(path)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"model": model, // empty → LocalAI auto-selects a vision model
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": prompt},
				{"type": "image_url", "image_url": map[string]string{"url": uri}},
			},
		}},
	}
	buf, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("describe failed: %s: %s", resp.Status, b)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("describe: empty choices")
	}
	return out.Choices[0].Message.Content, nil
}

// DescribeVideo turns a video into text by sending it to a video-capable model
// (via a native video_url content part) and returning the model's textual
// description. Mirrors Describe; it is the building block for the read_video tool.
// Empty model ⇒ LocalAI's default chat model (which must support video input).
func (c *Client) DescribeVideo(ctx context.Context, path, model, prompt string) (string, error) {
	if prompt == "" {
		prompt = "Describe this video in detail."
	}
	uri, err := DataURI(path)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"model": model,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": prompt},
				{"type": "video_url", "video_url": map[string]string{"url": uri}},
			},
		}},
	}
	buf, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("describe video failed: %s: %s", resp.Status, b)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("describe video: empty choices")
	}
	return out.Choices[0].Message.Content, nil
}
