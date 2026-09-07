package settings

import (
	"os"
	"path/filepath"
)

func writeSettingsFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".paw-settings-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
