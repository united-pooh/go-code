package file

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"paw/internal/platform/pawpath"
)

type globalHomePolicy struct {
	home, realHome string
	readRoots      []string
}

func newGlobalHomePolicy(readRoots []string) (*globalHomePolicy, error) {
	home, err := pawpath.Home()
	if err != nil {
		return nil, err
	}
	realHome, err := canonicalPathWithMissing(home)
	if err != nil {
		return nil, fmt.Errorf("resolve global Paw home: %w", err)
	}
	policy := &globalHomePolicy{home: home, realHome: realHome}
	for _, root := range cleanExtraRoots(readRoots) {
		realRoot, err := canonicalPathWithMissing(root)
		if err != nil {
			continue
		}
		// An ancestor ReadRoot must not reopen the entire internal storage tree.
		if realRoot != realHome && isWithinRoot(realHome, realRoot) {
			policy.readRoots = append(policy.readRoots, realRoot)
		}
	}
	return policy, nil
}

// canonicalPathWithMissing resolves existing ancestors without creating paths.
func canonicalPathWithMissing(path string) (string, error) {
	return canonicalPathWithMissingLinks(path, 0)
}

func canonicalPathWithMissingLinks(path string, links int) (string, error) {
	if links > 255 {
		return "", fmt.Errorf("too many symbolic links: %s", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Abs(real)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	candidate := abs
	for {
		info, err := os.Lstat(candidate)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				link, err := os.Readlink(candidate)
				if err != nil {
					return "", err
				}
				if !filepath.IsAbs(link) {
					parent, err := filepath.EvalSymlinks(filepath.Dir(candidate))
					if err != nil {
						return "", err
					}
					link = filepath.Join(parent, link)
				}
				suffix, err := filepath.Rel(candidate, abs)
				if err != nil {
					return "", err
				}
				return canonicalPathWithMissingLinks(filepath.Join(link, suffix), links+1)
			}
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", err
		}
		candidate = parent
	}
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	suffix, err := filepath.Rel(candidate, abs)
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(real, suffix))
}

// access distinguishes readable paths from hidden ancestors that a recursive
// search may traverse solely to reach explicitly allowed ReadRoots.
func (p *globalHomePolicy) access(path string) (readable, descend bool, err error) {
	real, err := canonicalPathWithMissing(path)
	if err != nil {
		return false, false, err
	}
	if !isWithinRoot(p.home, path) && !isWithinRoot(p.realHome, real) {
		return true, true, nil
	}
	for _, root := range p.readRoots {
		if isWithinRoot(root, real) {
			return true, true, nil
		}
		if isWithinRoot(real, root) {
			descend = true
		}
	}
	return false, descend, nil
}

func (p *globalHomePolicy) check(path string) error {
	allowed, _, err := p.access(path)
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("path is inside protected global Paw home: %s", path)
	}
	return nil
}

func resolveReadPathWithinRoots(root, target string, readRoots []string) (string, *globalHomePolicy, error) {
	path, err := resolvePathWithinRoots(root, target, readRoots)
	if err != nil {
		return "", nil, err
	}
	policy, err := newGlobalHomePolicy(readRoots)
	if err != nil {
		return "", nil, err
	}
	if err := policy.check(path); err != nil {
		return "", nil, err
	}
	return path, policy, nil
}

func walkReadPaths(ctx context.Context, root string, policy *globalHomePolicy, visit func(string, fs.DirEntry) error) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		readable, descend, err := policy.access(path)
		if err != nil {
			return err
		}
		if !readable {
			if entry != nil && entry.IsDir() && !descend {
				return filepath.SkipDir
			}
			if !descend {
				return nil
			}
		}
		if walkErr != nil {
			return walkErr
		}
		if !readable {
			return nil
		}
		return visit(path, entry)
	})
}
