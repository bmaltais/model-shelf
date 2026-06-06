package daemon

import (
	"testing"
)

func TestParseNvidiaSMI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name   string
		input  string
		want   *GPUInfo
		wantNil bool
	}{
		{
			name:  "single GPU",
			input: "NVIDIA A100-SXM4-80GB, 81920, 79000\n",
			want: &GPUInfo{
				Name:            "NVIDIA A100-SXM4-80GB",
				VRAMTotalGB:     80.0,
				VRAMAvailableGB: 79000.0 / 1024.0,
			},
		},
		{
			name:  "multi GPU takes first",
			input: "NVIDIA H100, 81920, 70000\nNVIDIA H100, 81920, 65000\n",
			want: &GPUInfo{
				Name:            "NVIDIA H100",
				VRAMTotalGB:     80.0,
				VRAMAvailableGB: 70000.0 / 1024.0,
			},
		},
		{
			name:    "empty output",
			input:   "",
			wantNil: true,
		},
		{
			name:    "malformed output",
			input:   "something wrong\n",
			wantNil: true,
		},
		{
			name:    "partial fields",
			input:   "NVIDIA A100, 81920\n",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseNvidiaSMI(tt.input)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil GPUInfo")
			}
			if got.Name != tt.want.Name {
				t.Errorf("name: got %q, want %q", got.Name, tt.want.Name)
			}
			// Allow small floating point differences.
			if diff := got.VRAMTotalGB - tt.want.VRAMTotalGB; diff > 0.1 || diff < -0.1 {
				t.Errorf("vram_total_gb: got %f, want %f", got.VRAMTotalGB, tt.want.VRAMTotalGB)
			}
			if diff := got.VRAMAvailableGB - tt.want.VRAMAvailableGB; diff > 0.1 || diff < -0.1 {
				t.Errorf("vram_available_gb: got %f, want %f", got.VRAMAvailableGB, tt.want.VRAMAvailableGB)
			}
		})
	}
}

func TestDetectGPU_Override(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	override := &GPUOverride{
		Name:        "Custom GPU",
		VRAMTotalGB: 128.0,
	}
	info := DetectGPU(override)
	if info == nil {
		t.Fatal("expected non-nil GPUInfo with override")
	}
	if info.Name != "Custom GPU" {
		t.Errorf("name: got %q, want %q", info.Name, "Custom GPU")
	}
	if info.VRAMTotalGB != 128.0 {
		t.Errorf("vram_total_gb: got %f, want 128.0", info.VRAMTotalGB)
	}
	if info.VRAMAvailableGB != 128.0 {
		t.Errorf("vram_available_gb: got %f, want 128.0", info.VRAMAvailableGB)
	}
}

func TestDetectGPU_NilOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// With nil override, relies on system detection.
	// On CI/test machines without GPU, should return nil gracefully.
	info := DetectGPU(nil)
	// Just ensure it doesn't panic — result depends on host hardware.
	_ = info
}

func TestParseVMStatValue(t *testing.T) {
	tests := []struct {
		line string
		want uint64
	}{
		{"Pages free:                              123456.", 123456},
		{"Pages inactive:                          789012.", 789012},
		{"Invalid line", 0},
	}
	for _, tt := range tests {
		got := parseVMStatValue(tt.line)
		if got != tt.want {
			t.Errorf("parseVMStatValue(%q) = %d, want %d", tt.line, got, tt.want)
		}
	}
}

func TestRefreshGPUAvailableVRAM_NilCurrent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	result := RefreshGPUAvailableVRAM(nil, nil)
	if result != nil {
		t.Errorf("expected nil when current is nil, got %+v", result)
	}
}

func TestRefreshGPUAvailableVRAM_Override(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	current := &GPUInfo{Name: "Test GPU", VRAMTotalGB: 64, VRAMAvailableGB: 32}
	override := &GPUOverride{Name: "Test GPU", VRAMTotalGB: 64}
	result := RefreshGPUAvailableVRAM(current, override)
	if result != current {
		t.Error("expected same pointer back with override")
	}
}
