package serve

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	appcore "paw/internal/app"
	webserver "paw/internal/ui/web"
	"runtime"
	"strings"
	"time"
)

const defaultServeListen = "127.0.0.1:0"

type Options struct {
	Listen string
	Open   bool
}

func ParseOptions(args []string) (Options, error) {
	options := Options{Listen: defaultServeListen}
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.Listen, "listen", defaultServeListen, "loopback address to listen on")
	flags.BoolVar(&options.Open, "open", false, "open the workbench in the default browser")
	if err := flags.Parse(args); err != nil {
		return Options{}, err
	}
	if flags.NArg() != 0 {
		return Options{}, fmt.Errorf("serve does not accept positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	address, err := validateServeListen(options.Listen)
	if err != nil {
		return Options{}, err
	}
	options.Listen = address
	return options, nil
}

func validateServeListen(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultServeListen
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return "", fmt.Errorf("invalid serve listen address %q: %w", value, err)
	}
	if port == "" {
		return "", fmt.Errorf("serve listen port is required")
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return "", fmt.Errorf("serve listen address must be loopback: %s", value)
	}
	return net.JoinHostPort(host, port), nil
}

func Run(options Options) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return runServe(ctx, options, os.Stdout, openServeBrowser)
}

func runServe(ctx context.Context, options Options, output io.Writer, openBrowser func(string)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	recent, err := appcore.NewRecentWorkspaceStore("")
	if err != nil {
		return err
	}
	governor := appcore.NewResourceGovernor(4)
	supervisor := appcore.NewSupervisor(appcore.SupervisorConfig{
		Capacity: 2, Recent: recent,
		Factory: func(ctx context.Context, opts appcore.WorkspaceRuntimeOptions) (*appcore.WorkspaceRuntime, error) {
			opts.ResourceGovernor = governor
			opts.ControllerMode = appcore.ControllerModeWeb
			opts.AllowIncomplete = true
			return appcore.BuildWorkspaceRuntime(ctx, opts)
		},
	})
	auth := webserver.NewAuthStore(false)
	server, err := webserver.NewServer(webserver.ServerConfig{Listen: options.Listen, Supervisor: supervisor, Auth: auth})
	if err != nil {
		return err
	}
	if cwd, err := os.Getwd(); err == nil && strings.TrimSpace(cwd) != "" {
		if _, err := supervisor.Open(ctx, appcore.WorkspaceRuntimeOptions{Root: cwd}); err != nil {
			if output != nil {
				_, _ = fmt.Fprintf(output, "warning: auto-open cwd workspace %s: %v\n", cwd, err)
			}
		}
	}
	if err := server.Start(); err != nil {
		return err
	}
	token, err := auth.NewBootstrapToken()
	if err != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
		return err
	}
	bootstrapURL := server.URL() + "/#bootstrap=" + token
	if output != nil {
		_, _ = fmt.Fprintf(output, "Paw workbench: %s\n", bootstrapURL)
	}
	if options.Open && openBrowser != nil {
		openBrowser(bootstrapURL)
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Wait() }()
	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		shutdownErr := server.Shutdown(shutdownCtx)
		waitErr := <-serveErr
		return errors.Join(shutdownErr, waitErr)
	}
}

func openServeBrowser(url string) {
	url = strings.TrimSpace(url)
	if url == "" {
		return
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	_ = command.Start()
}
