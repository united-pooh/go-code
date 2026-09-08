package task

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"paw/internal/runtime/actor"
	"paw/internal/storage/es"
	"paw/internal/ui"
)

func (m *Manager) importLegacyTasks(ctx context.Context) error {
	if err := m.storageError(); err != nil {
		return err
	}
	if err := os.MkdirAll(m.registry.tasksDir(), 0o755); err != nil {
		m.registry.initErr = fmt.Errorf("create task storage: %w", err)
		return m.registry.initErr
	}

	var errs []error
	seen := make(map[string]bool)
	indexed, err := m.actors.list(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("recover global task registry: %w", err))
	}
	global := make(map[string]TaskSnapshot)
	projections := make(map[string]TaskSnapshot)
	for _, task := range indexed {
		global[task.ID] = task
		projections[task.ID] = task
	}
	ids, err := taskMigrationActorIDs(filepath.Join(m.registry.projectDir, "actors"))
	if err != nil {
		errs = append(errs, err)
	}
	for _, id := range ids {
		if _, ok := global[id]; !ok {
			global[id] = TaskSnapshot{ID: id}
		}
	}
	for _, id := range taskMigrationIDs(global) {
		seen[id] = true
		if err := m.recoverTaskProjection(ctx, global[id], projections[id]); err != nil {
			errs = append(errs, fmt.Errorf("recover global task %q: %w", id, err))
		}
	}

	// Only the global system may execute messages. Legacy recovery folds durable
	// facts without replaying inboxes, outboxes, timers, or repairing old streams.
	legacy, failed, err := m.readLegacyActorTasks(ctx)
	if err != nil {
		errs = append(errs, err)
	}
	for _, id := range failed {
		seen[id] = true
	}
	for _, id := range taskMigrationIDs(legacy) {
		if seen[id] {
			continue
		}
		seen[id] = true
		if err := m.recoverTaskProjection(ctx, legacy[id], projections[id]); err != nil {
			errs = append(errs, fmt.Errorf("import legacy task %q: %w", id, err))
		}
	}

	// Metadata is a compatibility projection, never an override for actor facts.
	tasks, err := m.registry.listTasks(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("import task metadata: %w", err))
	}
	for _, task := range tasks {
		if seen[task.ID] {
			continue
		}
		if err := m.recoverTaskProjection(ctx, task, projections[task.ID]); err != nil {
			errs = append(errs, fmt.Errorf("import task %q: %w", task.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) recoverTaskProjection(ctx context.Context, task, projection TaskSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateTaskID(task.ID); err != nil {
		return err
	}
	authoritative, found, err := m.actors.status(ctx, task.ID)
	if err != nil {
		return err
	}
	if found {
		if task.ID != authoritative.ID {
			return fmt.Errorf("task id does not match actor id")
		}
		task = authoritative
	}
	if task.Status == "" {
		return nil
	}
	task.OutputPath = m.registry.outputPath(task.ID)
	outputErr := m.registry.importLegacyOutput(ctx, task.ID)
	if !found {
		return errors.Join(outputErr, m.actors.record(ctx, taskEventForStatus(task.Status), task))
	}
	if err := m.registry.saveTask(ctx, task); err != nil {
		return errors.Join(outputErr, err)
	}
	if reflect.DeepEqual(projection, task) {
		return outputErr
	}
	err = m.actors.system.Tell(ctx, taskRegistryActorID, actor.Msg{
		Kind:       taskRegistryUpsert,
		Payload:    taskRegistryUpdate{Task: task},
		Durability: actor.Durable,
	})
	m.actors.system.Drain()
	return errors.Join(outputErr, err)
}

func (m *Manager) readLegacyActorTasks(ctx context.Context) (map[string]TaskSnapshot, []string, error) {
	base := filepath.Join(m.registry.root, ".paw", "actors")
	index := newTaskIndexActor(taskRegistryActorID, nil)
	var errs []error
	if err := readTaskMigrationActor(ctx, base, taskRegistryActorID, index); err != nil {
		errs = append(errs, fmt.Errorf("read legacy task registry: %w", err))
		index = newTaskIndexActor(taskRegistryActorID, nil)
	}
	tasks := index.state.Tasks
	ids, err := taskMigrationActorIDs(base)
	if err != nil {
		errs = append(errs, err)
	}
	var failed []string
	for _, id := range ids {
		a := newTaskActor(actor.ActorID{Type: taskActorType, Key: id}, m.registry, nil)
		if err := readTaskMigrationActor(ctx, base, a.ID(), a); err != nil {
			errs = append(errs, fmt.Errorf("read legacy task %q: %w", id, err))
			delete(tasks, id)
			failed = append(failed, id)
			continue
		}
		if a.state.Found {
			if a.state.Task.ID == "" {
				a.state.Task.ID = id
			}
			if a.state.Task.ID != id {
				errs = append(errs, fmt.Errorf("legacy task %q has mismatched id %q", id, a.state.Task.ID))
				delete(tasks, id)
				failed = append(failed, id)
				continue
			}
			tasks[id] = a.state.Task
		}
	}
	return tasks, failed, errors.Join(errs...)
}

func readTaskMigrationActor(ctx context.Context, base string, id actor.ActorID, target actor.EventSourced) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store, err := es.NewJSONLStore(base, id.Type)
	if err != nil {
		return err
	}
	events, _, err := store.Load(ctx, id.Key)
	if err != nil {
		return err
	}
	var seq int64
	snapshot, found, snapshotErr := store.ReadSnapshot(ctx, id.Key)
	if snapshotErr != nil && len(events) == 0 {
		return snapshotErr
	}
	if snapshotErr == nil && found {
		if err := target.Restore(snapshot.State); err != nil {
			return err
		}
		seq = snapshot.Seq
	}
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return err
		}
		if event.Kind != es.KindRuntime && event.Seq > seq {
			if err := target.Fold(event); err != nil {
				return err
			}
		}
	}
	return nil
}

func taskMigrationActorIDs(base string) ([]string, error) {
	dir := filepath.Join(base, taskActorType)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan task actors %q: %w", dir, err)
	}
	seen := make(map[string]bool)
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		for _, suffix := range []string{".events.jsonl", ".snapshot.json"} {
			id, ok := strings.CutSuffix(entry.Name(), suffix)
			if ok && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

func taskMigrationIDs(tasks map[string]TaskSnapshot) []string {
	ids := make([]string, 0, len(tasks))
	for id := range tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *Manager) reportTaskMigrationError(err error) {
	if m.notifier != nil {
		if notifyErr := m.notifier.OnSystemMessage(ui.SystemEvent{Title: "task recovery", Body: err.Error()}); notifyErr == nil {
			return
		}
	}
	log.Printf("task recovery: %v", err)
}
