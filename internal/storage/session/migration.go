package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// meta.json is the commit marker. Each payload is synced and published without
// replacement before the marker, so interruption leaves a retryable merge while
// readers still use legacy. Never rename or remove the destination directory.
func (s *JSONLStore) migrateSession(legacy, global string, meta []byte) error {
	info, err := os.Stat(filepath.Join(legacy, "meta.json"))
	if err != nil {
		return err
	}
	if err := s.mergeSessionDirectory(legacy, global, true); err != nil {
		return err
	}
	metaPath := filepath.Join(global, "meta.json")
	published, err := s.publishMigrationFile(metaPath, meta, info.Mode().Perm(), info.ModTime())
	if err != nil {
		return err
	}
	if err := s.syncMigrationDirectory(global); err != nil {
		// Roll back only our commit marker, never an existing winner's metadata
		// or payload. A retry can reuse the already-published complete files.
		if published != nil {
			current, statErr := os.Lstat(metaPath)
			if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				return errors.Join(err, statErr)
			}
			if statErr == nil && os.SameFile(published, current) {
				return errors.Join(err, os.Remove(metaPath), s.syncMigrationDirectory(global))
			}
		}
		return err
	}
	return nil
}

func (s *JSONLStore) mergeSessionDirectory(src, dst string, sessionRoot bool) error {
	if err := s.makeMigrationDirectory(dst); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if sessionRoot && entry.Name() == "meta.json" {
			continue
		}
		source, target := filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := s.mergeSessionDirectory(source, target, false); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("不支持迁移非普通文件: %s", source)
		}
		if exists, err := migrationFileExists(target); err != nil {
			return err
		} else if exists {
			continue
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if _, err := s.publishMigrationFile(target, data, info.Mode().Perm(), info.ModTime()); err != nil {
			return err
		}
	}
	return s.syncMigrationDirectory(dst)
}

func migrationFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("迁移目标不是普通文件: %s", path)
	}
	return true, nil
}

func (s *JSONLStore) publishMigrationFile(path string, data []byte, mode os.FileMode, modTime time.Time) (os.FileInfo, error) {
	if exists, err := migrationFileExists(path); err != nil || exists {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".migrate-"+filepath.Base(path)+"-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := tmp.Chmod(mode); err != nil {
		return nil, err
	}
	if _, err := tmp.Write(data); err != nil {
		return nil, err
	}
	if err := os.Chtimes(tmp.Name(), modTime, modTime); err != nil {
		return nil, err
	}
	if err := s.syncFile(tmp); err != nil {
		return nil, err
	}
	info, err := tmp.Stat()
	if err != nil {
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	// Link is an atomic no-replace publication on the same filesystem. A retry
	// or another store may already have published the destination; keep it.
	if err := os.Link(tmp.Name(), path); err != nil {
		if errors.Is(err, os.ErrExist) {
			_, err = migrationFileExists(path)
		}
		return nil, err
	}
	return info, nil
}

func (s *JSONLStore) makeMigrationDirectory(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("迁移目标不是目录: %s", path)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(path)
	if err := s.makeMigrationDirectory(parent); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if err := s.makeMigrationDirectory(path); err != nil {
			return err
		}
	}
	return s.syncMigrationDirectory(parent)
}

func (s *JSONLStore) syncMigrationDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return s.syncFile(dir)
}
