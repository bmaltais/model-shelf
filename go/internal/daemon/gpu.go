package daemon

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// GPUInfo describes GPU hardware on a node.
type GPUInfo struct {
	Name            string  `json:"name"`
	VRAMTotalGB     float64 `json:"vram_total_gb"`
	VRAMAvailableGB float64 `json:"vram_available_gb"`
}

// GPUOverride holds manual GPU configuration from config.toml.
type GPUOverride struct {
	Name        string  `toml:"name"`
	VRAMTotalGB float64 `toml:"vram_total_gb"`
}

// DetectGPU auto-detects GPU hardware. Returns nil if no GPU is found.
// If override is non-nil, it is used instead of auto-detection for name and total VRAM.
func DetectGPU(override *GPUOverride) *GPUInfo {
	if override != nil && override.Name != "" {
		return &GPUInfo{
			Name:            override.Name,
			VRAMTotalGB:     override.VRAMTotalGB,
			VRAMAvailableGB: override.VRAMTotalGB, // assume all available initially
		}
	}

	// Try nvidia-smi first.
	if info := detectNvidia(); info != nil {
		return info
	}

	// Try unified memory detection (Apple Silicon, Grace Blackwell).
	if info := detectUnifiedMemory(); info != nil {
		return info
	}

	return nil
}

// RefreshGPUAvailableVRAM updates only the available VRAM field.
// Returns nil if no GPU is detected or the refresh fails.
func RefreshGPUAvailableVRAM(current *GPUInfo, override *GPUOverride) *GPUInfo {
	if current == nil {
		return nil
	}
	if override != nil && override.Name != "" {
		// Manual override — can't refresh dynamically.
		return current
	}

	// Try nvidia-smi for available memory.
	if info := detectNvidia(); info != nil {
		return info
	}

	// For unified memory, re-detect.
	if info := detectUnifiedMemory(); info != nil {
		return info
	}

	return current
}

// detectNvidia runs nvidia-smi and parses GPU info.
func detectNvidia() *GPUInfo {
	out, err := exec.Command("nvidia-smi",
		"--query-gpu=name,memory.total,memory.free",
		"--format=csv,noheader,nounits",
	).Output()
	if err != nil {
		return nil
	}
	return parseNvidiaSMI(string(out))
}

// parseNvidiaSMI parses nvidia-smi CSV output.
// Format: "GPU Name, total_mb, free_mb"
// For multi-GPU, reports the first GPU.
func parseNvidiaSMI(output string) *GPUInfo {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return nil
	}
	// Take first GPU.
	parts := strings.SplitN(lines[0], ",", 3)
	if len(parts) != 3 {
		return nil
	}
	name := strings.TrimSpace(parts[0])
	totalMB, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return nil
	}
	freeMB, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if err != nil {
		return nil
	}
	return &GPUInfo{
		Name:            name,
		VRAMTotalGB:     totalMB / 1024.0,
		VRAMAvailableGB: freeMB / 1024.0,
	}
}

// detectUnifiedMemory detects unified memory on macOS (Apple Silicon).
func detectUnifiedMemory() *GPUInfo {
	if runtime.GOOS != "darwin" {
		return nil
	}
	// Use sysctl to get total memory.
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return nil
	}
	bytes, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return nil
	}
	totalGB := float64(bytes) / (1024 * 1024 * 1024)

	// Get chip name from sysctl.
	chipOut, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
	chipName := "Apple Silicon"
	if err == nil {
		chipName = strings.TrimSpace(string(chipOut))
	}

	// Get available memory via vm_stat (approximate).
	availableGB := estimateFreeMemoryDarwin()
	if availableGB <= 0 {
		availableGB = totalGB // fallback: report total
	}

	return &GPUInfo{
		Name:            chipName + " (Unified Memory)",
		VRAMTotalGB:     totalGB,
		VRAMAvailableGB: availableGB,
	}
}

// estimateFreeMemoryDarwin uses vm_stat to estimate free memory on macOS.
func estimateFreeMemoryDarwin() float64 {
	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0
	}
	lines := strings.Split(string(out), "\n")
	var freePages, inactivePages uint64
	for _, line := range lines {
		if strings.HasPrefix(line, "Pages free:") {
			freePages = parseVMStatValue(line)
		} else if strings.HasPrefix(line, "Pages inactive:") {
			inactivePages = parseVMStatValue(line)
		}
	}
	// Page size is 16384 on Apple Silicon, 4096 on Intel.
	pageSize := uint64(16384)
	if runtime.GOARCH == "amd64" {
		pageSize = 4096
	}
	freeBytes := (freePages + inactivePages) * pageSize
	return float64(freeBytes) / (1024 * 1024 * 1024)
}

// parseVMStatValue extracts the numeric value from a vm_stat line like "Pages free: 12345."
func parseVMStatValue(line string) uint64 {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return 0
	}
	val := strings.TrimSpace(parts[1])
	val = strings.TrimSuffix(val, ".")
	n, _ := strconv.ParseUint(val, 10, 64)
	return n
}
