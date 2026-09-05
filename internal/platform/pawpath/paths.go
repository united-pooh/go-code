package pawpath

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func Home() (string, error) {
	root := strings.TrimSpace(os.Getenv("PAW_CONFIG_HOME"))
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve Paw home: %w", err)
		}
		root = filepath.Join(home, ".paw")
	}
	return filepath.Abs(root)
}

func ProjectDir(workspace string) (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return ProjectDirInHome(home, workspace)
}

func ProjectDirInHome(home, workspace string) (string, error) {
	if home == "" {
		return "", fmt.Errorf("Paw home is empty")
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return "", fmt.Errorf("resolve Paw home: %w", err)
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	return filepath.Join(home, "projects", projectName(workspace)), nil
}

// Keep the existing session directory identity so upgrades retain global history.
func projectName(workspace string) string {
	var slug strings.Builder
	for _, r := range filepath.Base(workspace) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			slug.WriteRune(r)
		default:
			slug.WriteByte('-')
		}
	}
	name := strings.Trim(slug.String(), "-")
	if name == "" {
		name = "project"
	}
	sum := sha256.Sum256([]byte(workspace))
	return fmt.Sprintf("%s-%x", name, sum[:4])
}
