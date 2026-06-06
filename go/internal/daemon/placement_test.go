package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSelectExecutor_NoExecutors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	nodes := []MeshNode{
		{Name: "store-1", Roles: []string{"store"}, Status: StatusOnline, GPU: nil},
	}
	_, err := SelectExecutor(nodes, 8.0, "test/model", "mlx", "", nil, nil)
	if err == nil {
		t.Fatal("expected error when no executors available")
	}
	pe, ok := err.(*PlacementError)
	if !ok {
		t.Fatalf("expected PlacementError, got %T", err)
	}
	if len(pe.Candidates) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(pe.Candidates))
	}
}

func TestSelectExecutor_InsufficientVRAM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	nodes := []MeshNode{
		{Name: "exec-1", Roles: []string{"executor"}, Status: StatusOnline, GPU: &GPUInfo{
			Name: "RTX 3060", VRAMTotalGB: 12.0, VRAMAvailableGB: 10.0,
		}},
		{Name: "exec-2", Roles: []string{"executor"}, Status: StatusOnline, GPU: &GPUInfo{
			Name: "RTX 3080", VRAMTotalGB: 10.0, VRAMAvailableGB: 8.0,
		}},
	}
	_, err := SelectExecutor(nodes, 20.0, "test/model", "mlx", "", nil, nil)
	if err == nil {
		t.Fatal("expected error when no executor has sufficient VRAM")
	}
	pe, ok := err.(*PlacementError)
	if !ok {
		t.Fatalf("expected PlacementError, got %T", err)
	}
	if pe.EstimatedVRAMGB != 20.0 {
		t.Errorf("expected estimated VRAM 20.0, got %.1f", pe.EstimatedVRAMGB)
	}
	if len(pe.Candidates) != 2 {
		t.Errorf("expected 2 candidates listed, got %d", len(pe.Candidates))
	}
}

func TestSelectExecutor_SelectsByVRAM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	nodes := []MeshNode{
		{Name: "exec-small", Roles: []string{"executor"}, Status: StatusOnline,
			DiskFreeGB: 100,
			GPU:        &GPUInfo{Name: "RTX 3060", VRAMTotalGB: 12.0, VRAMAvailableGB: 10.0}},
		{Name: "exec-big", Roles: []string{"executor"}, Status: StatusOnline,
			DiskFreeGB: 200,
			GPU:        &GPUInfo{Name: "RTX 4090", VRAMTotalGB: 24.0, VRAMAvailableGB: 20.0}},
	}

	// Both have sufficient VRAM for 10GB model — should prefer most disk space.
	result, err := SelectExecutor(nodes, 10.0, "test/model", "mlx", "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Target != "exec-big" {
		t.Errorf("expected exec-big (most disk space), got %s", result.Target)
	}
	if result.Reason != "not currently serving and most free disk space" {
		t.Errorf("expected reason 'not currently serving and most free disk space', got %q", result.Reason)
	}
}

func TestSelectExecutor_PrefersNodeWithModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	nodes := []MeshNode{
		{Name: "exec-1", Roles: []string{"executor"}, Status: StatusOnline,
			DiskFreeGB: 200,
			GPU:        &GPUInfo{Name: "RTX 4090", VRAMTotalGB: 24.0, VRAMAvailableGB: 20.0}},
		{Name: "exec-2", Roles: []string{"executor"}, Status: StatusOnline,
			DiskFreeGB: 100,
			GPU:        &GPUInfo{Name: "RTX 4090", VRAMTotalGB: 24.0, VRAMAvailableGB: 20.0}},
	}

	// exec-2 already has the model on disk.
	inventoryByNode := map[string][]InventoryEntry{
		"exec-2": {{RepoID: "test/model", Format: "mlx", SizeBytes: 5000000000}},
	}

	result, err := SelectExecutor(nodes, 10.0, "test/model", "mlx", "", inventoryByNode, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Target != "exec-2" {
		t.Errorf("expected exec-2 (already has model), got %s", result.Target)
	}
	if result.Reason != "already has model on disk and not currently serving" {
		t.Errorf("expected reason 'already has model on disk and not currently serving', got %q", result.Reason)
	}
}

func TestSelectExecutor_PrefersNotServing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	nodes := []MeshNode{
		{Name: "exec-serving", Roles: []string{"executor"}, Status: StatusOnline,
			DiskFreeGB: 500,
			GPU:        &GPUInfo{Name: "RTX 4090", VRAMTotalGB: 24.0, VRAMAvailableGB: 20.0}},
		{Name: "exec-idle", Roles: []string{"executor"}, Status: StatusOnline,
			DiskFreeGB: 100,
			GPU:        &GPUInfo{Name: "RTX 4090", VRAMTotalGB: 24.0, VRAMAvailableGB: 20.0}},
	}

	// exec-serving is currently busy.
	activeJobs := map[string]int{
		"exec-serving": 1,
	}

	result, err := SelectExecutor(nodes, 10.0, "test/model", "mlx", "", nil, activeJobs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Target != "exec-idle" {
		t.Errorf("expected exec-idle (not serving), got %s", result.Target)
	}
	if result.Reason != "not currently serving and most free disk space" {
		t.Errorf("expected reason 'not currently serving and most free disk space', got %q", result.Reason)
	}
}

func TestSelectExecutor_SkipsOfflineNodes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	nodes := []MeshNode{
		{Name: "exec-offline", Roles: []string{"executor"}, Status: StatusOffline,
			DiskFreeGB: 500,
			GPU:        &GPUInfo{Name: "A100", VRAMTotalGB: 80.0, VRAMAvailableGB: 70.0}},
		{Name: "exec-online", Roles: []string{"executor"}, Status: StatusOnline,
			DiskFreeGB: 100,
			GPU:        &GPUInfo{Name: "RTX 4090", VRAMTotalGB: 24.0, VRAMAvailableGB: 20.0}},
	}

	result, err := SelectExecutor(nodes, 10.0, "test/model", "mlx", "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Target != "exec-online" {
		t.Errorf("expected exec-online (offline node skipped), got %s", result.Target)
	}
}

func TestSelectExecutor_GGUFWithQuant(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	nodes := []MeshNode{
		{Name: "exec-1", Roles: []string{"executor"}, Status: StatusOnline,
			DiskFreeGB: 100,
			GPU:        &GPUInfo{Name: "RTX 4090", VRAMTotalGB: 24.0, VRAMAvailableGB: 20.0}},
		{Name: "exec-2", Roles: []string{"executor"}, Status: StatusOnline,
			DiskFreeGB: 200,
			GPU:        &GPUInfo{Name: "RTX 4090", VRAMTotalGB: 24.0, VRAMAvailableGB: 20.0}},
	}

	// exec-1 has the same model but with a different quant.
	inventoryByNode := map[string][]InventoryEntry{
		"exec-1": {{RepoID: "Qwen/Qwen3-14B-GGUF", Format: "gguf", Quant: "Q8_0", SizeBytes: 15000000000}},
	}

	// Requesting Q4_K_M — exec-1 does NOT have this quant.
	result, err := SelectExecutor(nodes, 8.0, "Qwen/Qwen3-14B-GGUF", "gguf", "Q4_K_M", inventoryByNode, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Neither has the specific quant, so prefer most disk space.
	if result.Target != "exec-2" {
		t.Errorf("expected exec-2 (most disk space), got %s", result.Target)
	}
}

func TestSelectExecutor_NoGPUTreatedAsZeroVRAM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	nodes := []MeshNode{
		{Name: "exec-no-gpu", Roles: []string{"executor"}, Status: StatusOnline,
			DiskFreeGB: 500, GPU: nil},
		{Name: "exec-with-gpu", Roles: []string{"executor"}, Status: StatusOnline,
			DiskFreeGB: 100,
			GPU:        &GPUInfo{Name: "RTX 4090", VRAMTotalGB: 24.0, VRAMAvailableGB: 20.0}},
	}

	result, err := SelectExecutor(nodes, 10.0, "test/model", "mlx", "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// exec-no-gpu has 0 VRAM and can't fit 10GB model.
	if result.Target != "exec-with-gpu" {
		t.Errorf("expected exec-with-gpu (exec-no-gpu has no VRAM), got %s", result.Target)
	}
}

func TestSelectExecutor_CPUOnlyNodeWithModelOnDisk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	nodes := []MeshNode{
		{Name: "cpu-node", Roles: []string{"executor"}, Status: StatusOnline,
			DiskFreeGB: 500, GPU: nil},
		{Name: "gpu-node", Roles: []string{"executor"}, Status: StatusOnline,
			DiskFreeGB: 100,
			GPU:        &GPUInfo{Name: "RTX 4090", VRAMTotalGB: 24.0, VRAMAvailableGB: 20.0}},
	}

	// cpu-node already has the model on disk — should bypass VRAM filter.
	inventoryByNode := map[string][]InventoryEntry{
		"cpu-node": {{RepoID: "unsloth/Qwen3-0.6B-GGUF", Format: "gguf", Quant: "Q4_K_M", SizeBytes: 400000000}},
	}

	result, err := SelectExecutor(nodes, 0.4, "unsloth/Qwen3-0.6B-GGUF", "gguf", "Q4_K_M", inventoryByNode, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Target != "cpu-node" {
		t.Errorf("expected cpu-node (has model on disk, bypasses VRAM filter), got %s", result.Target)
	}
	if result.Reason != "already has model on disk and not currently serving (CPU inference — no GPU)" {
		t.Errorf("unexpected reason: %s", result.Reason)
	}
}

func TestSelectExecutor_CPUOnlyNodeWithoutModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// CPU-only node without the model should still be rejected by VRAM filter.
	nodes := []MeshNode{
		{Name: "cpu-node", Roles: []string{"executor"}, Status: StatusOnline,
			DiskFreeGB: 500, GPU: nil},
		{Name: "gpu-node", Roles: []string{"executor"}, Status: StatusOnline,
			DiskFreeGB: 100,
			GPU:        &GPUInfo{Name: "RTX 4090", VRAMTotalGB: 24.0, VRAMAvailableGB: 20.0}},
	}

	result, err := SelectExecutor(nodes, 10.0, "test/model", "mlx", "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Target != "gpu-node" {
		t.Errorf("expected gpu-node (cpu-node has no VRAM and no model), got %s", result.Target)
	}
}

func TestHasRole(t *testing.T) {
	tests := []struct {
		roles []string
		role  string
		want  bool
	}{
		{[]string{"executor", "store"}, "executor", true},
		{[]string{"executor", "store"}, "controller", false},
		{[]string{"Executor"}, "executor", true},
		{nil, "executor", false},
	}
	for _, tc := range tests {
		got := HasRole(tc.roles, tc.role)
		if got != tc.want {
			t.Errorf("HasRole(%v, %q) = %v, want %v", tc.roles, tc.role, got, tc.want)
		}
	}
}

func TestSizeForGGUF(t *testing.T) {
	siblings := []struct {
		Filename string `json:"rfilename"`
		Size     int64  `json:"size"`
	}{
		{"Qwen3-14B-Q4_K_M.gguf", 8000000000},
		{"Qwen3-14B-Q8_0.gguf", 15000000000},
		{"random-file.txt", 1024},
	}

	t.Run("exact match", func(t *testing.T) {
		size, err := sizeForGGUF(siblings, "Q4_K_M", "repo")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if size != 8000000000 {
			t.Errorf("expected 8GB, got %d", size)
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		size, err := sizeForGGUF(siblings, "q4_k_m", "repo")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if size != 8000000000 {
			t.Errorf("expected 8GB, got %d", size)
		}
	})

	t.Run("substring fallback", func(t *testing.T) {
		// Even if ExtractQuant wouldn't perfectly match, strings.Contains should.
		size, err := sizeForGGUF(siblings, "Q8_0", "repo")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if size != 15000000000 {
			t.Errorf("expected 15GB, got %d", size)
		}
	})

	t.Run("no match", func(t *testing.T) {
		_, err := sizeForGGUF(siblings, "Q2_K", "repo")
		if err == nil {
			t.Fatal("expected error for no match")
		}
	})
}

func TestQueryContentLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "HEAD" {
			t.Errorf("expected HEAD request, got %s", r.Method)
		}
		w.Header().Set("Content-Length", "12345")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	size, err := queryContentLength(server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if size != 12345 {
		t.Errorf("expected 12345, got %d", size)
	}
}

func TestIsWeightFile(t *testing.T) {
	tests := []struct {
		name   string
		format string
		want   bool
	}{
		{"model.safetensors", "safetensors", true},
		{"model.safetensors", "mlx", true},
		{"weights.npz", "mlx", true},
		{"config.json", "safetensors", false},
		{"config.json", "mlx", false},
		{"model.gguf", "safetensors", false},
	}
	for _, tc := range tests {
		got := isWeightFile(tc.name, tc.format)
		if got != tc.want {
			t.Errorf("isWeightFile(%q, %q) = %v, want %v", tc.name, tc.format, got, tc.want)
		}
	}
}

func TestPlacementError_Error(t *testing.T) {
	t.Run("no executors", func(t *testing.T) {
		pe := &PlacementError{EstimatedVRAMGB: 10.0}
		msg := pe.Error()
		if msg != "no Executor nodes available in the mesh" {
			t.Errorf("unexpected message: %s", msg)
		}
	})

	t.Run("insufficient VRAM", func(t *testing.T) {
		pe := &PlacementError{
			EstimatedVRAMGB: 20.0,
			Candidates: []PlacementCandidate{
				{Name: "exec-1", VRAMTotalGB: 12.0},
			},
		}
		msg := pe.Error()
		if msg == "" {
			t.Error("expected non-empty error message")
		}
	})
}
