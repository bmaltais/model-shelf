package daemon

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexziskind1/model-shelf/internal/meshconfig"
)

func TestHandleModelDownload_GGUF(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}

	// Place a GGUF model on the shelf.
	ggufDir := filepath.Join(shelfRoot, "gguf", "Qwen", "Qwen3-14B-GGUF")
	os.MkdirAll(ggufDir, 0o755)
	modelData := []byte("fake-gguf-model-data-12345")
	os.WriteFile(filepath.Join(ggufDir, "Qwen3-14B-Q4_K_M.gguf"), modelData, 0o644)

	cfg := &meshconfig.Config{
		Name:      "source-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/gguf/Qwen/Qwen3-14B-GGUF/download?quant=Q4_K_M", nil)
	w := httptest.NewRecorder()

	d.handleModelDownload(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if w.Header().Get("Content-Type") != "application/octet-stream" {
		t.Errorf("expected Content-Type application/octet-stream, got %s", w.Header().Get("Content-Type"))
	}

	if !bytes.Equal(w.Body.Bytes(), modelData) {
		t.Errorf("expected model data to match, got %d bytes", w.Body.Len())
	}
}

func TestHandleModelDownload_GGUF_ContentLength(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}

	ggufDir := filepath.Join(shelfRoot, "gguf", "Qwen", "Qwen3-14B-GGUF")
	os.MkdirAll(ggufDir, 0o755)
	modelData := bytes.Repeat([]byte("x"), 4096)
	os.WriteFile(filepath.Join(ggufDir, "Qwen3-14B-Q4_K_M.gguf"), modelData, 0o644)

	cfg := &meshconfig.Config{
		Name:      "source-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/gguf/Qwen/Qwen3-14B-GGUF/download?quant=Q4_K_M", nil)
	w := httptest.NewRecorder()
	d.handleModelDownload(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	cl := w.Header().Get("Content-Length")
	if cl == "" {
		t.Fatal("Content-Length header not set for GGUF response")
	}
	var contentLength int64
	if _, err := fmt.Sscanf(cl, "%d", &contentLength); err != nil {
		t.Fatalf("Content-Length is not a valid integer: %q", cl)
	}
	if contentLength != int64(w.Body.Len()) {
		t.Errorf("Content-Length %d does not match actual body size %d", contentLength, w.Body.Len())
	}
	if contentLength != int64(len(modelData)) {
		t.Errorf("Content-Length %d does not match model file size %d", contentLength, len(modelData))
	}
}

func TestHandleModelDownload_MLX(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}

	// Place an MLX model on the shelf.
	mlxDir := filepath.Join(shelfRoot, "mlx", "mlx-community", "TestModel")
	os.MkdirAll(mlxDir, 0o755)
	os.WriteFile(filepath.Join(mlxDir, "config.json"), []byte(`{"model_type": "test"}`), 0o644)
	os.WriteFile(filepath.Join(mlxDir, "weights.safetensors"), []byte("fake-weights"), 0o644)

	cfg := &meshconfig.Config{
		Name:      "source-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/mlx/mlx-community/TestModel/download", nil)
	w := httptest.NewRecorder()

	d.handleModelDownload(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if w.Header().Get("Content-Type") != "application/x-tar" {
		t.Errorf("expected Content-Type application/x-tar, got %s", w.Header().Get("Content-Type"))
	}

	// Verify tar contents.
	tr := tar.NewReader(w.Body)
	files := make(map[string][]byte)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading tar: %v", err)
		}
		data, _ := io.ReadAll(tr)
		files[header.Name] = data
	}

	if _, ok := files["config.json"]; !ok {
		t.Error("tar archive missing config.json")
	}
	if _, ok := files["weights.safetensors"]; !ok {
		t.Error("tar archive missing weights.safetensors")
	}
	if string(files["config.json"]) != `{"model_type": "test"}` {
		t.Errorf("config.json content mismatch: %s", files["config.json"])
	}
}

func TestHandleModelDownload_NotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}

	cfg := &meshconfig.Config{
		Name:      "source-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/gguf/NoSuch/Model-GGUF/download?quant=Q4_K_M", nil)
	w := httptest.NewRecorder()

	d.handleModelDownload(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleModelDownload_MethodNotAllowed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &meshconfig.Config{
		Name:      "source-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: t.TempDir(),
	}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodPost, "/v1/models/gguf/Qwen/Model/download?quant=Q4_K_M", nil)
	w := httptest.NewRecorder()

	d.handleModelDownload(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleModelDownload_MissingQuant(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}

	cfg := &meshconfig.Config{
		Name:      "source-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/gguf/Qwen/Model-GGUF/download", nil)
	w := httptest.NewRecorder()

	d.handleModelDownload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing quant, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleModelDownload_AuthRequired(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}

	cfg := &meshconfig.Config{
		Name:      "source-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
		MeshKey:   "secret-key",
	}
	d := New(cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models/", d.handleModelDownload)
	handler := d.authMiddleware(mux)

	// Without auth.
	req := httptest.NewRequest(http.MethodGet, "/v1/models/mlx/test/model/download", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", w.Code)
	}

	// With correct auth.
	req = httptest.NewRequest(http.MethodGet, "/v1/models/mlx/test/model/download", nil)
	req.Header.Set("Authorization", "Bearer secret-key")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Should not be 401 (will be 404 since model doesn't exist, but auth passed).
	if w.Code == http.StatusUnauthorized {
		t.Fatal("expected auth to pass with correct key")
	}
}

func TestFindPeerWithModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Create a fake peer that has the model in its inventory.
	peerInventory := []InventoryEntry{
		{RepoID: "Qwen/Qwen3-14B-GGUF", Format: "gguf", Quant: "Q4_K_M", SizeBytes: 10000},
		{RepoID: "mlx-community/TestModel", Format: "mlx", SizeBytes: 5000},
	}
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"disk_free_gb": 100, "disk_total_gb": 500, "uptime_seconds": 3600})
			return
		}
		if r.URL.Path == "/v1/inventory" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(peerInventory)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer peerServer.Close()

	peerHost := peerServer.Listener.Addr().(*net.TCPAddr).IP.String()
	peerPort := peerServer.Listener.Addr().(*net.TCPAddr).Port

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}
	cfg := &meshconfig.Config{
		Name:      "local-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	d.gossip.AddNode(MeshNode{
		Name:    "peer-node",
		Address: peerHost,
		Port:    peerPort,
		Roles:   []string{"store"},
		Status:  StatusOnline,
	})

	// Should find the GGUF model on the peer.
	peer := d.findPeerWithModel("Qwen/Qwen3-14B-GGUF", "gguf", "Q4_K_M")
	if peer == nil {
		t.Fatal("expected to find peer with model")
	}
	if peer.Name != "peer-node" {
		t.Fatalf("expected peer-node, got %s", peer.Name)
	}

	// Should find the MLX model on the peer.
	peer = d.findPeerWithModel("mlx-community/TestModel", "mlx", "")
	if peer == nil {
		t.Fatal("expected to find peer with MLX model")
	}

	// Should NOT find a model that doesn't exist.
	peer = d.findPeerWithModel("nonexistent/model", "mlx", "")
	if peer != nil {
		t.Fatal("expected no peer for nonexistent model")
	}
}

func TestPeerTransfer_GGUF(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Set up a source node with a model.
	sourceShelf := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(sourceShelf, f), 0o755)
	}
	ggufDir := filepath.Join(sourceShelf, "gguf", "Qwen", "TestModel-GGUF")
	os.MkdirAll(ggufDir, 0o755)
	modelData := bytes.Repeat([]byte("x"), 1024) // 1KB fake model
	os.WriteFile(filepath.Join(ggufDir, "TestModel-Q4_K_M.gguf"), modelData, 0o644)

	sourceCfg := &meshconfig.Config{
		Name:      "source-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: sourceShelf,
	}
	sourceD := New(sourceCfg)

	// Start a test server with the source daemon's handler.
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"disk_free_gb": 100, "disk_total_gb": 500, "uptime_seconds": 3600})
			return
		}
		sourceD.handleModelDownload(w, r)
	}))
	defer sourceServer.Close()

	sourceHost := sourceServer.Listener.Addr().(*net.TCPAddr).IP.String()
	sourcePort := sourceServer.Listener.Addr().(*net.TCPAddr).Port

	// Set up the destination node.
	destShelf := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(destShelf, f), 0o755)
	}
	destCfg := &meshconfig.Config{
		Name:      "dest-node",
		Port:      8845,
		Roles:     []string{"store"},
		ShelfRoot: destShelf,
	}
	destD := New(destCfg)

	// Create a job and perform the transfer.
	job := destD.jobs.Create("Qwen/TestModel-GGUF", "gguf", "Q4_K_M", "dest-node")
	peer := &peerSource{Name: "source-node", Address: sourceHost, Port: sourcePort}
	err := destD.transferFromPeer(job.ID, peer, "Qwen/TestModel-GGUF", "gguf", "Q4_K_M")
	if err != nil {
		t.Fatalf("transfer failed: %v", err)
	}

	// Verify the file was written.
	destPath := filepath.Join(destShelf, "gguf", "Qwen", "TestModel-GGUF", "TestModel-Q4_K_M.gguf")
	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read transferred file: %v", err)
	}
	if !bytes.Equal(data, modelData) {
		t.Errorf("transferred data mismatch: got %d bytes, want %d", len(data), len(modelData))
	}

	// Verify job status.
	got := destD.jobs.Get(job.ID)
	if got.Status != JobTransferring {
		t.Errorf("expected job status transferring (before completion), got %s", got.Status)
	}
}

func TestPeerTransfer_MLX(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Set up a source node with an MLX model.
	sourceShelf := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(sourceShelf, f), 0o755)
	}
	mlxDir := filepath.Join(sourceShelf, "mlx", "mlx-community", "TestModel")
	os.MkdirAll(mlxDir, 0o755)
	os.WriteFile(filepath.Join(mlxDir, "config.json"), []byte(`{"model_type": "test"}`), 0o644)
	os.WriteFile(filepath.Join(mlxDir, "weights.safetensors"), []byte("fake-weights-data"), 0o644)

	sourceCfg := &meshconfig.Config{
		Name:      "source-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: sourceShelf,
	}
	sourceD := New(sourceCfg)

	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"disk_free_gb": 100, "disk_total_gb": 500, "uptime_seconds": 3600})
			return
		}
		sourceD.handleModelDownload(w, r)
	}))
	defer sourceServer.Close()

	sourceHost := sourceServer.Listener.Addr().(*net.TCPAddr).IP.String()
	sourcePort := sourceServer.Listener.Addr().(*net.TCPAddr).Port

	// Set up the destination node.
	destShelf := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(destShelf, f), 0o755)
	}
	destCfg := &meshconfig.Config{
		Name:      "dest-node",
		Port:      8845,
		Roles:     []string{"store"},
		ShelfRoot: destShelf,
	}
	destD := New(destCfg)

	// Create a job and perform the transfer.
	job := destD.jobs.Create("mlx-community/TestModel", "mlx", "", "dest-node")
	peer := &peerSource{Name: "source-node", Address: sourceHost, Port: sourcePort}
	err := destD.transferFromPeer(job.ID, peer, "mlx-community/TestModel", "mlx", "")
	if err != nil {
		t.Fatalf("transfer failed: %v", err)
	}

	// Verify the files were extracted.
	destDir := filepath.Join(destShelf, "mlx", "mlx-community", "TestModel")
	configData, err := os.ReadFile(filepath.Join(destDir, "config.json"))
	if err != nil {
		t.Fatalf("config.json not found: %v", err)
	}
	if string(configData) != `{"model_type": "test"}` {
		t.Errorf("config.json content mismatch: %s", configData)
	}

	weightsData, err := os.ReadFile(filepath.Join(destDir, "weights.safetensors"))
	if err != nil {
		t.Fatalf("weights.safetensors not found: %v", err)
	}
	if string(weightsData) != "fake-weights-data" {
		t.Errorf("weights content mismatch: %s", weightsData)
	}
}

func TestExecutePull_PrefersPeerTransfer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HF_TOKEN", "")
	t.Setenv("HUGGING_FACE_HUB_TOKEN", "")

	// Set up a source node with an MLX model.
	sourceShelf := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(sourceShelf, f), 0o755)
	}
	mlxDir := filepath.Join(sourceShelf, "mlx", "testorg", "testmodel")
	os.MkdirAll(mlxDir, 0o755)
	os.WriteFile(filepath.Join(mlxDir, "config.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(mlxDir, "model.safetensors"), []byte("model-data"), 0o644)

	sourceCfg := &meshconfig.Config{
		Name:      "source-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: sourceShelf,
	}
	sourceD := New(sourceCfg)

	// Source node serves both inventory and download.
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"disk_free_gb": 100, "disk_total_gb": 500, "uptime_seconds": 3600})
			return
		}
		if r.URL.Path == "/v1/inventory" {
			entries := sourceD.inventory.Entries()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(entries)
			return
		}
		if r.URL.Path == "/v1/jobs" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]Job{})
			return
		}
		sourceD.handleModelDownload(w, r)
	}))
	defer sourceServer.Close()

	sourceHost := sourceServer.Listener.Addr().(*net.TCPAddr).IP.String()
	sourcePort := sourceServer.Listener.Addr().(*net.TCPAddr).Port

	// Set up the destination node.
	destShelf := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(destShelf, f), 0o755)
	}
	destCfg := &meshconfig.Config{
		Name:      "dest-node",
		Port:      8845,
		Roles:     []string{"store"},
		ShelfRoot: destShelf,
	}
	destD := New(destCfg)

	// Add source as a peer.
	destD.gossip.AddNode(MeshNode{
		Name:    "source-node",
		Address: sourceHost,
		Port:    sourcePort,
		Roles:   []string{"store"},
		Status:  StatusOnline,
	})

	// Execute pull — should transfer from peer, not HF.
	job := destD.jobs.Create("testorg/testmodel", "mlx", "", "dest-node")
	destD.executePull(job.ID, "testorg/testmodel", "mlx", "", false, "auto")

	got := destD.jobs.Get(job.ID)
	if got.Status != JobCompleted {
		t.Fatalf("expected completed (peer transfer), got %s (error: %s)", got.Status, got.Error)
	}

	// Verify the model was transferred.
	destDir := filepath.Join(destShelf, "mlx", "testorg", "testmodel")
	if _, err := os.Stat(filepath.Join(destDir, "config.json")); err != nil {
		t.Error("config.json not found after peer transfer")
	}
	if _, err := os.Stat(filepath.Join(destDir, "model.safetensors")); err != nil {
		t.Error("model.safetensors not found after peer transfer")
	}
}

func TestPeerTransfer_FallbackToHF(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HF_TOKEN", "")
	t.Setenv("HUGGING_FACE_HUB_TOKEN", "")

	// Set up a source node that will fail (returns 500).
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"disk_free_gb": 100, "disk_total_gb": 500, "uptime_seconds": 3600})
			return
		}
		if r.URL.Path == "/v1/inventory" {
			// Report that it has the model.
			entries := []InventoryEntry{
				{RepoID: "testorg/testmodel", Format: "mlx", SizeBytes: 1000},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(entries)
			return
		}
		if r.URL.Path == "/v1/jobs" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]Job{})
			return
		}
		// Download fails.
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer sourceServer.Close()

	sourceHost := sourceServer.Listener.Addr().(*net.TCPAddr).IP.String()
	sourcePort := sourceServer.Listener.Addr().(*net.TCPAddr).Port

	// Set up destination with shelf.
	destShelf := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(destShelf, f), 0o755)
	}
	destCfg := &meshconfig.Config{
		Name:      "dest-node",
		Port:      8845,
		Roles:     []string{"store"},
		ShelfRoot: destShelf,
	}
	destD := New(destCfg)

	destD.gossip.AddNode(MeshNode{
		Name:    "source-node",
		Address: sourceHost,
		Port:    sourcePort,
		Roles:   []string{"store"},
		Status:  StatusOnline,
	})

	// Execute pull — peer transfer will fail, should fall back to HF.
	// HF will also fail (invalid repo), but the important thing is the fallback path is taken.
	job := destD.jobs.Create("testorg/testmodel", "mlx", "", "dest-node")
	destD.executePull(job.ID, "testorg/testmodel", "mlx", "", false, "auto")

	got := destD.jobs.Get(job.ID)
	// Should fail because HF download also fails (no real repo), but the job should
	// NOT stay in "transferring" — it should attempt HF fallback.
	if got.Status == JobTransferring {
		t.Fatal("expected job to not remain in transferring status after peer failure")
	}
	// It will be "failed" because "testorg/testmodel" isn't a real HF repo.
	if got.Status != JobFailed {
		t.Logf("job status: %s (error: %s)", got.Status, got.Error)
	}
}

func TestHandleModelDownload_UpdatesLastAccessed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}

	// Place a GGUF model on the shelf.
	ggufDir := filepath.Join(shelfRoot, "gguf", "Qwen", "TestModel-GGUF")
	os.MkdirAll(ggufDir, 0o755)
	os.WriteFile(filepath.Join(ggufDir, "TestModel-Q4_K_M.gguf"), []byte("data"), 0o644)

	cfg := &meshconfig.Config{
		Name:      "source-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/gguf/Qwen/TestModel-GGUF/download?quant=Q4_K_M", nil)
	w := httptest.NewRecorder()
	d.handleModelDownload(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify inventory was touched on the source.
	entries := d.inventory.Entries()
	found := false
	for _, e := range entries {
		if e.RepoID == "Qwen/TestModel-GGUF" && e.Format == "gguf" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected inventory to be updated (last-accessed) on download serve")
	}
}

func TestHandleModelDownload_MLX_ContentLength(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}

	mlxDir := filepath.Join(shelfRoot, "mlx", "mlx-community", "TestModel")
	os.MkdirAll(mlxDir, 0o755)
	configData := []byte(`{"model_type": "test"}`)
	weightsData := []byte("fake-weights-data-123")
	os.WriteFile(filepath.Join(mlxDir, "config.json"), configData, 0o644)
	os.WriteFile(filepath.Join(mlxDir, "weights.safetensors"), weightsData, 0o644)

	cfg := &meshconfig.Config{
		Name:      "source-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/mlx/mlx-community/TestModel/download", nil)
	w := httptest.NewRecorder()
	d.handleModelDownload(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Content-Length must be set and match the actual body size.
	cl := w.Header().Get("Content-Length")
	if cl == "" {
		t.Fatal("Content-Length header not set for MLX tar response")
	}
	var contentLength int64
	if _, err := fmt.Sscanf(cl, "%d", &contentLength); err != nil {
		t.Fatalf("Content-Length is not a valid integer: %q", cl)
	}
	if contentLength != int64(w.Body.Len()) {
		t.Errorf("Content-Length %d does not match actual body size %d", contentLength, w.Body.Len())
	}
}

func TestTarredSize(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.bin"), bytes.Repeat([]byte("x"), 1000), 0o644)
	os.WriteFile(filepath.Join(dir, "b.bin"), bytes.Repeat([]byte("y"), 512), 0o644)
	os.WriteFile(filepath.Join(dir, "c.bin"), []byte("small"), 0o644)

	size, err := tarredSize(dir)
	if err != nil {
		t.Fatalf("tarredSize error: %v", err)
	}

	// Write an actual tar archive and compare sizes.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	entries := []struct {
		name string
		data []byte
	}{
		{"a.bin", bytes.Repeat([]byte("x"), 1000)},
		{"b.bin", bytes.Repeat([]byte("y"), 512)},
		{"c.bin", []byte("small")},
	}
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Size: int64(len(e.data)), Mode: 0o644, Typeflag: tar.TypeReg}
		tw.WriteHeader(hdr)
		tw.Write(e.data)
	}
	tw.Close()

	if size != int64(buf.Len()) {
		t.Errorf("tarredSize=%d, actual tar=%d", size, buf.Len())
	}
}

func TestTransferSnapshot_UsesStagingDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}

	cfg := &meshconfig.Config{
		Name:      "test-node",
		Port:      8844,
		Roles:     []string{"store"},
		ShelfRoot: shelfRoot,
	}
	d := New(cfg)

	// Create a tar archive simulating a snapshot transfer.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	files := map[string][]byte{
		"config.json":       []byte(`{"model_type": "qwen3"}`),
		"model.safetensors": bytes.Repeat([]byte("M"), 500),
	}
	for name, data := range files {
		hdr := &tar.Header{Name: name, Size: int64(len(data)), Mode: 0o644, Typeflag: tar.TypeReg}
		tw.WriteHeader(hdr)
		tw.Write(data)
	}
	tw.Close()

	repoID := "mlx-community/Qwen3-0.6B-4bit"
	format := "mlx"
	jobID := d.jobs.Create(repoID, format, "", "test-node").ID

	// Run the transfer.
	err := d.transferSnapshot(jobID, &buf, int64(buf.Len()), repoID, format)
	if err != nil {
		t.Fatalf("transferSnapshot failed: %v", err)
	}

	// Verify the model is in the final path (not staging).
	destDir := filepath.Join(shelfRoot, "mlx", "mlx-community", "Qwen3-0.6B-4bit")
	if _, err := os.Stat(filepath.Join(destDir, "config.json")); err != nil {
		t.Errorf("expected config.json in final path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "model.safetensors")); err != nil {
		t.Errorf("expected model.safetensors in final path: %v", err)
	}

	// Verify no staging directory remains.
	stagingDir := filepath.Join(shelfRoot, "mlx", "mlx-community", ".Qwen3-0.6B-4bit.transferring")
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Errorf("staging directory should not exist after successful transfer")
	}
}

func TestTransferSnapshot_StagingNotVisibleInInventory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shelfRoot := t.TempDir()
	for _, f := range []string{"gguf", "mlx", "safetensors"} {
		os.MkdirAll(filepath.Join(shelfRoot, f), 0o755)
	}

	// Create a dot-prefixed directory simulating an in-progress transfer.
	stagingDir := filepath.Join(shelfRoot, "mlx", "mlx-community", ".SomeModel.transferring")
	os.MkdirAll(stagingDir, 0o755)
	os.WriteFile(filepath.Join(stagingDir, "config.json"), []byte("{}"), 0o644)

	inv := NewInventory()
	if err := inv.ScanShelf(shelfRoot); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	entries := inv.Entries()
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries (staging should be invisible), got %d", len(entries))
	}
}
