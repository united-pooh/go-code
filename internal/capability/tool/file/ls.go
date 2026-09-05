package file

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type LSTool struct {
	Root      string
	ReadRoots []string
}

type lsInput struct {
	Path string `json:"path,omitempty"`
}

func (t *LSTool) Name() string {
	return "LS"
}

func (t *LSTool) Description() string {
	return "列出工作区内某个路径下的文件和目录"
}

func (t *LSTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
}

func (t *LSTool) IsConcurrencySafe(json.RawMessage) bool {
	return true
}

func (t *LSTool) Run(ctx context.Context, input json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var in lsInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return "", err
		}
	}

	target, policy, err := resolveReadPathWithinRoots(t.Root, in.Path, t.ReadRoots)
	if err != nil {
		return "", err
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		return "", err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		readable, _, err := policy.access(filepath.Join(target, entry.Name()))
		if err != nil {
			return "", err
		}
		if !readable {
			continue
		}
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, "\n"), nil
}
