package tracer

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"paw/internal/platform/pawpath"
	"paw/internal/tokentracer"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Options struct {
	Host string
	Port int
	Open bool
}

func ParseOptions(args []string) (Options, error) {
	var opts Options
	flags := flag.NewFlagSet("tracer", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listen := flags.String("listen", "127.0.0.1:0", "loopback address; port 0 selects an available port")
	flags.BoolVar(&opts.Open, "open", false, "open the global dashboard")
	if err := flags.Parse(args); err != nil {
		return opts, err
	}
	if flags.NArg() != 0 {
		return opts, fmt.Errorf("tracer accepts no positional arguments")
	}
	host, port, err := net.SplitHostPort(*listen)
	if err != nil {
		return opts, err
	}
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return opts, fmt.Errorf("tracer listen address must be loopback")
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 0 || value > 65535 {
		return opts, fmt.Errorf("invalid tracer port")
	}
	opts.Host, opts.Port = host, value
	return opts, nil
}

func Run(opts Options) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return runTracer(ctx, opts, os.Stdout)
}

func runTracer(ctx context.Context, opts Options, output io.Writer) error {
	home, err := pawpath.Home()
	if err != nil {
		return err
	}
	server := tokentracer.NewGlobalServer(home, tokentracer.ServerConfig{Host: opts.Host, Port: opts.Port, OpenBrowser: opts.Open})
	if err := server.Start(ctx); err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if _, err := fmt.Fprintf(output, "Paw global Token Tracer: %s\n", server.URL()); err != nil {
		return err
	}
	err = server.Wait(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
