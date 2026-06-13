// Package search queries the Hugging Face Hub for models matching a text query.
package search

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/bmaltais/model-shelf/internal/resolver"
)

const defaultBaseURL = "https://huggingface.co"

// FindResult describes a single search hit.
type FindResult struct {
	RepoID    string `json:"repo_id"`
	Format    string `json:"format"`
	Downloads int    `json:"downloads"`
}

// Options configures a FindModels call.
type Options struct {
	Limit   int
	Format  string // optional filter
	BaseURL string // override for testing
}

// FindModels searches the HF Hub and returns up to Options.Limit results.
func FindModels(query string, opts Options) ([]FindResult, error) {
	base := opts.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	fetchLimit := limit
	if opts.Format != "" {
		fetchLimit = limit * 5
	}

	apiURL := fmt.Sprintf("%s/api/models?search=%s&limit=%d&sort=downloads&direction=-1",
		base, url.QueryEscape(query), fetchLimit)

	resp, err := http.Get(apiURL) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("search request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search: HF API returned %s", resp.Status)
	}

	var raw []struct {
		ID        string `json:"id"`
		Downloads int    `json:"downloads"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding search response: %w", err)
	}

	var out []FindResult
	for _, m := range raw {
		fmt := resolver.DetectFormat(m.ID)
		if opts.Format != "" && fmt != opts.Format {
			continue
		}
		out = append(out, FindResult{RepoID: m.ID, Format: fmt, Downloads: m.Downloads})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}
