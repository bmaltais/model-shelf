// Package tui provides interactive terminal UI helpers.
package tui

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/huh"

	"github.com/bmaltais/model-shelf/internal/detect"
)

// PickShelfPath shows an interactive shelf picker and returns the chosen path.
// Returns ("", nil) if the user cancels. Returns an error on terminal failure.
func PickShelfPath(candidates []detect.Candidate) (string, error) {
	var huhOptions []huh.Option[string]
	for _, c := range candidates {
		tag := "new"
		if c.Existing {
			tag = "existing"
		}
		where := "internal"
		if c.IsExternal {
			where = "external"
		}
		label := fmt.Sprintf("[%s] %s (%s)  —  %s", where, c.Label, tag, c.Path)
		huhOptions = append(huhOptions, huh.NewOption(label, c.Path))
	}
	huhOptions = append(huhOptions, huh.NewOption("Enter a custom path...", "__custom__"))

	var chosen string
	selectForm := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Where do you want your Model Shelf?").
				Options(huhOptions...).
				Value(&chosen),
		),
	)

	if err := selectForm.Run(); err != nil {
		if err == huh.ErrUserAborted {
			return "", nil
		}
		return "", err
	}

	if chosen == "__custom__" {
		home, _ := os.UserHomeDir()
		defaultPath := filepath.Join(home, ".cache", "model-shelf", "models")
		var customPath string
		inputForm := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Enter shelf path:").
					Placeholder(defaultPath).
					Value(&customPath),
			),
		)
		if err := inputForm.Run(); err != nil {
			if err == huh.ErrUserAborted {
				return "", nil
			}
			return "", err
		}
		if customPath == "" {
			customPath = defaultPath
		}
		return filepath.Clean(customPath), nil
	}

	return chosen, nil
}
