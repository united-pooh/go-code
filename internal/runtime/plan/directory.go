package plan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const DefaultDirectory = "docs/local/plans"

// Legacy documents are copied without replacement; their bytes, permissions
// and timestamps stay untouched. A complete temporary file is published with
// Link so interruption or concurrent startup cannot expose a partial document.
func preparePlanDirectory(dir string) error {
	clean := filepath.Clean(dir)
	if filepath.Base(clean) != "plans" || filepath.Base(filepath.Dir(clean)) != "local" || filepath.Base(filepath.Dir(filepath.Dir(clean))) != "docs" {
		return nil
	}
	legacy := filepath.Join(filepath.Dir(filepath.Dir(clean)), "superpowers", "plans")
	for _, dir := range []string{filepath.Dir(filepath.Dir(clean)), filepath.Dir(legacy), legacy, filepath.Dir(clean), clean} {
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("plan directory is not a real directory: %s", dir)
		}
	}
	entries, err := os.ReadDir(legacy)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read legacy plans: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if _, err := safeID(PlanID(strings.TrimSuffix(entry.Name(), ".md"))); err != nil {
			continue
		}
		if err := copyLegacyPlan(filepath.Join(legacy, entry.Name()), filepath.Join(clean, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyLegacyPlan(source, target string) error {
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("plan destination is not a regular file: %s", target)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("legacy plan is not a regular file: %s", source)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".plan-migrate-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Chtimes(tmp.Name(), info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), target); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		info, statErr := os.Lstat(target)
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("plan destination is not a regular file: %s", target)
		}
	}
	return nil
}
