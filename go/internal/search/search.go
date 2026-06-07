// Package search provides Hugging Face Hub search functionality.
package search

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/alexziskind1/model-shelf/internal/resolver"
)

// httpClient is a shared client with a reasonable timeout for API calls.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// FindResult represents a single search result.
type FindResult struct {
	RepoID    string `json:"repo_id"`
	Format    string `json:"format"`
	Downloads int    `json:"downloads"`
}

// FindModels searches the HF Hub for models matching a query.
func FindModels(query string, format string, limit int) ([]FindResult, error) {
	if limit <= 0 {
		limit = 10
	}
	fetchLimit := limit
	if format != "" {
		fetchLimit = limit * 5
	}

	// When a format filter is specified, append the format keyword to the search
	// query so the HF API returns format-relevant results. Without this, the API
	// returns results matching only the text query (e.g. safetensors models) which
	// are then filtered out client-side, yielding empty results.
	searchQuery := query
	if format != "" && !containsFormatKeyword(query, format) {
		searchQuery = query + " " + format
	}

	apiURL := fmt.Sprintf("https://huggingface.co/api/models?search=%s&limit=%d&sort=downloads&direction=-1",
		url.QueryEscape(searchQuery), fetchLimit)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	if token := os.Getenv("HF_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if token := os.Getenv("HUGGING_FACE_HUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
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

// containsFormatKeyword checks if the query already contains a keyword
// associated with the given format, making an additional append redundant.
func containsFormatKeyword(query, format string) bool {
	lower := strings.ToLower(query)
	switch format {
	case "mlx":
		return strings.Contains(lower, "mlx")
	case "gguf":
		return strings.Contains(lower, "gguf")
	case "safetensors":
		return strings.Contains(lower, "safetensors")
	}
	return false
}
