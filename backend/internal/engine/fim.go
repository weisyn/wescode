package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// FIMProvider holds the credentials needed for a direct LLM FIM call.
type FIMProvider struct {
	BaseURL string
	APIKey  string
	Model   string
}

// DefaultProvider returns the first usable provider config for FIM requests.
// Cell Mode: delegates to CellClient.DefaultProviderKey.
// Config Mode: reads from s.cfg.Providers.
func (s *Service) DefaultProvider() *FIMProvider {
	if s.providers != nil {
		baseURL, apiKey, model := s.providers.DefaultProviderKey()
		if apiKey != "" {
			return &FIMProvider{BaseURL: baseURL, APIKey: apiKey, Model: model}
		}
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.cfg.Providers {
		if strings.TrimSpace(p.APIKey) != "" {
			model := p.CompletionModel
			if model == "" {
				model = p.Model
			}
			return &FIMProvider{
				BaseURL: p.BaseURL,
				APIKey:  p.APIKey,
				Model:   model,
			}
		}
	}
	return nil
}

// FIMComplete sends a FIM prompt to the LLM and returns the completion text.
func (s *Service) FIMComplete(ctx context.Context, provider *FIMProvider, prompt string) (string, error) {
	if provider == nil {
		return "", fmt.Errorf("no provider configured")
	}

	body, err := json.Marshal(map[string]any{
		"model":       provider.Model,
		"max_tokens":  512,
		"temperature": 0.0,
		"stop":        []string{"\n\n\n"}, // INV-FIM-02: only triple-newline; smart trim handles the rest
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshal FIM request: %w", err)
	}

	url := strings.TrimRight(provider.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build FIM request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+provider.APIKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("FIM request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return "", fmt.Errorf("FIM HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode FIM response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", nil
	}

	completion := result.Choices[0].Message.Content
	completion = strings.TrimPrefix(completion, "```")
	completion = strings.TrimSuffix(completion, "```")
	completion = strings.TrimSpace(completion)
	return completion, nil
}
