package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"paw/internal/platform/pawpath"
)

const tasksDirName = "tasks"

type taskRegistry struct {
	root       string
	projectDir string
	initErr    error
}

func newTaskRegistry(root string) taskRegistry {
	if root == "" {
		root = "."
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return taskRegistry{initErr: fmt.Errorf("resolve task workspace: %w", err)}
	}
	projectDir, err := pawpath.ProjectDir(root)
	if err != nil {
		return taskRegistry{initErr: fmt.Errorf("resolve task storage: %w", err)}
	}
	return taskRegistry{root: root, projectDir: projectDir}
}

func (r taskRegistry) err() error {
	if r.initErr != nil {
		return r.initErr
	}
	if r.projectDir == "" {
		return fmt.Errorf("task storage is unavailable")
	}
	return nil
}

func validateTaskID(taskID string) error {
	if strings.TrimSpace(taskID) == "" || taskID == "." || taskID == ".." || strings.ContainsAny(taskID, "/\\\x00") {
		return fmt.Errorf("invalid task id: %q", taskID)
	}
	return nil
}

func (r taskRegistry) outputPath(taskID string) string {
	if r.err() != nil || validateTaskID(taskID) != nil {
		return ""
	}
	return filepath.Join(r.taskDir(taskID), "output.json")
}

func (r taskRegistry) saveTask(ctx context.Context, task TaskSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.err(); err != nil {
		return err
	}
	if err := validateTaskID(task.ID); err != nil {
		return err
	}
	task.OutputPath = r.outputPath(task.ID)
	if err := os.MkdirAll(r.taskDir(task.ID), 0o755); err != nil {
		return fmt.Errorf("create task directory: %w", err)
	}
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(r.metaPath(task.ID), data, 0o600); err != nil {
		return fmt.Errorf("write task meta: %w", err)
	}
	return nil
}

func (r taskRegistry) saveOutput(ctx context.Context, taskID string, result WorkerResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.err(); err != nil {
		return err
	}
	if err := validateTaskID(taskID); err != nil {
		return err
	}
	if err := os.MkdirAll(r.taskDir(taskID), 0o755); err != nil {
		return fmt.Errorf("create task directory: %w", err)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task output: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(r.outputPath(taskID), data, 0o600); err != nil {
		return fmt.Errorf("write task output: %w", err)
	}
	return nil
}

func (r taskRegistry) loadTask(ctx context.Context, taskID string) (TaskSnapshot, bool, error) {
	if err := ctx.Err(); err != nil {
		return TaskSnapshot{}, false, err
	}
	if err := r.err(); err != nil {
		return TaskSnapshot{}, false, err
	}
	if err := validateTaskID(taskID); err != nil {
		return TaskSnapshot{}, false, err
	}
	data, err := os.ReadFile(r.metaPath(taskID))
	legacy := errors.Is(err, os.ErrNotExist)
	if legacy {
		data, err = os.ReadFile(filepath.Join(r.legacyTasksDir(), taskID, "meta.json"))
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return TaskSnapshot{}, false, nil
		}
		return TaskSnapshot{}, false, err
	}
	var task TaskSnapshot
	if err := json.Unmarshal(data, &task); err != nil {
		return TaskSnapshot{}, false, fmt.Errorf("parse task meta: %w", err)
	}
	if task.ID == "" {
		task.ID = taskID
	}
	if task.ID != taskID {
		return TaskSnapshot{}, false, fmt.Errorf("task id %q does not match directory %q", task.ID, taskID)
	}
	task.OutputPath = r.outputPath(task.ID)
	if legacy {
		if err := r.importLegacyOutput(ctx, taskID); err != nil {
			return TaskSnapshot{}, false, err
		}
		if err := r.saveTask(ctx, task); err != nil {
			return TaskSnapshot{}, false, err
		}
	}
	return task, true, nil
}

func (r taskRegistry) importLegacyOutput(ctx context.Context, taskID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Legacy writers always used this path, regardless of metadata's output_path.
	data, err := os.ReadFile(filepath.Join(r.legacyTasksDir(), taskID, "output.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read legacy task output: %w", err)
	}
	if err := os.MkdirAll(r.taskDir(taskID), 0o755); err != nil {
		return fmt.Errorf("create task directory: %w", err)
	}
	file, err := os.OpenFile(r.outputPath(taskID), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("import task output: %w", err)
	}
	_, writeErr := file.Write(data)
	return errors.Join(writeErr, file.Close())
}

func (r taskRegistry) listTasks(ctx context.Context) ([]TaskSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := r.err(); err != nil {
		return nil, err
	}
	var tasks []TaskSnapshot
	var errs []error
	seen := make(map[string]bool)
	for _, dir := range []string{r.tasksDir(), r.legacyTasksDir()} {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("list task metadata %q: %w", dir, err))
			continue
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return tasks, errors.Join(append(errs, err)...)
			}
			if !entry.IsDir() || seen[entry.Name()] {
				continue
			}
			seen[entry.Name()] = true
			task, ok, err := r.loadTask(ctx, entry.Name())
			if err != nil {
				errs = append(errs, fmt.Errorf("load task %q: %w", entry.Name(), err))
				continue
			}
			if ok {
				tasks = append(tasks, task)
			}
		}
	}
	return tasks, errors.Join(errs...)
}

func (r taskRegistry) legacyTasksDir() string {
	if r.err() != nil {
		return ""
	}
	return filepath.Join(r.root, ".paw", tasksDirName)
}

func (r taskRegistry) tasksDir() string {
	if r.err() != nil {
		return ""
	}
	return filepath.Join(r.projectDir, tasksDirName)
}

func (r taskRegistry) taskDir(taskID string) string {
	if r.err() != nil || validateTaskID(taskID) != nil {
		return ""
	}
	return filepath.Join(r.tasksDir(), taskID)
}

func (r taskRegistry) metaPath(taskID string) string {
	if r.err() != nil || validateTaskID(taskID) != nil {
		return ""
	}
	return filepath.Join(r.taskDir(taskID), "meta.json")
}
