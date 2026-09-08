package oneshot

import (
	"context"
	"fmt"
	"os"
	appcore "paw/internal/app"
	"paw/internal/todo"
	"paw/internal/ui/headless"
)

func Run(ctx context.Context, opts Options) error {
	output := headless.New(os.Stdout)
	todoBroker := todo.NewBroker()
	defer todoBroker.Close()
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	app, err := appcore.BuildWorkspaceRuntime(ctx, appcore.WorkspaceRuntimeOptions{
		Root: root, SessionID: opts.SessionID, Output: output,
		AllowOutsideRead: opts.AllowOutsideRead, AllowIncomplete: false,
		TodoBroker: todoBroker, SelectionBroker: nil,
		ControllerMode: appcore.ControllerModeTUI,
	})
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()
	appcore.BindSessionTools(app, todoBroker, app.SessionID)
	appcore.ApplyCompressionSettings(app.SessionHost.Engine, app.SettingsController)
	app.SessionHost.SetStreamMAEnabled(opts.StreamMA)

	fmt.Fprintf(os.Stderr, "session: %s\n", app.SessionID)
	_, err = app.SessionHost.RunTurn(ctx, opts.Prompt)
	return err
}
