package app

import (
	"fmt"
	"paw/internal/capability/tool"
	selecttool "paw/internal/capability/tool/select"
	"paw/internal/todo"
)

func registerMainAgentTools(registry *tool.Registry, broker *todo.Broker) error {
	tools := NewToolset(broker)
	return tools.RegisterMain(registry)
}

func registerInteractiveTools(registry *tool.Registry, broker *selecttool.Broker) error {
	tools := NewToolset(nil)
	if registry == nil {
		return fmt.Errorf("tool registry is nil")
	}
	if broker == nil {
		return fmt.Errorf("selection broker is nil")
	}
	return tools.RegisterInteractive(registry, broker)
}
