package search_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bmaltais/model-shelf/internal/search"
)

func TestFindModels_Basic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		results := []map[string]any{
			{"id": "Qwen/Qwen3-14B-GGUF", "downloads": 500000},
			{"id": "mlx-community/Qwen3-4bit", "downloads": 100000},
			{"id": "Qwen/Qwen3-14B", "downloads": 300000},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	}))
	defer srv.Close()

	results, err := search.FindModels("qwen3", search.Options{BaseURL: srv.URL, Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].RepoID != "Qwen/Qwen3-14B-GGUF" {
		t.Errorf("first result = %q, want Qwen/Qwen3-14B-GGUF", results[0].RepoID)
	}
	if results[0].Format != "gguf" {
		t.Errorf("format = %q, want gguf", results[0].Format)
	}
	if results[0].Downloads != 500000 {
		t.Errorf("downloads = %d, want 500000", results[0].Downloads)
	}
}

func TestFindModels_FormatFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		results := []map[string]any{
			{"id": "Qwen/Qwen3-14B-GGUF", "downloads": 500000},
			{"id": "mlx-community/Qwen3-4bit", "downloads": 100000},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	}))
	defer srv.Close()

	results, err := search.FindModels("qwen3", search.Options{BaseURL: srv.URL, Limit: 10, Format: "gguf"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result after format filter, got %d", len(results))
	}
	if results[0].Format != "gguf" {
		t.Errorf("format = %q, want gguf", results[0].Format)
	}
}
