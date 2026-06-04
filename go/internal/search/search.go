// Package search provides Hugging Face Hub search functionality.
package search

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/alexziskind1/model-shelf/internal/resolver"
)

// FindResult represents a single search result.
type FindResult struct {
	RepoID    string `json:"repo_id"`
	Format    string `json:"format"`
	Downloads int    `json:"downloads"`
}

// FindModels searches the HF Hub for models matching a query.
func FindModels(query string, format string, limit int) ([]FindResult, error) {
	fetchLimit := limit
	if format != "" {
		fetchLimit = limit * 5
	}

	apiURL := fmt.Sprintf("https://huggingface.co/api/models?search=%s&limit=%d&sort=downloads&direction=-1",
		url.QueryEscape(query), fetchLimit)

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("HF API request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HF API returned %d", resp.StatusCode)
	}

	var models []struct {
		ID        string `json:"id"`
		Downloads int    `json:"downloads"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return nil, fmt.Errorf("parsing HF response: %w", err)
	}

	var out []FindResult
	for _, m := range models {
		fmt := resolver.DetectFormat(m.ID)
		if format != "" && fmt != format {
			continue
		}
		out = append(out, FindResult{
			RepoID:    m.ID,
			Format:    fmt,
			Downloads: m.Downloads,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}
