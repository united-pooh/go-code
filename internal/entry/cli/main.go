package cli

import (
	"context"
	"log"
	"os"
	"paw/internal/capability/model"
	"paw/internal/entry/interactive"
	"paw/internal/entry/oneshot"
	"paw/internal/entry/serve"
	"paw/internal/entry/tracer"
	"paw/internal/entry/worker"
)

func Main() {
	if len(os.Args) > 1 && os.Args[1] == "tracer" {
		opts, err := tracer.ParseOptions(os.Args[2:])
		if err != nil {
			log.Fatal(err)
		}
		if err := tracer.Run(opts); err != nil {
			log.Fatal(err)
		}
		return
	}
	// Load .env/.env.local before any mode so provider auth configured via
	// auth.env resolves from the project environment instead of prompting
	// for the macOS keychain on every startup.
	if _, err := model.LoadOptionalEnvFiles(); err != nil {
		log.Printf("warning: load .env/.env.local: %v", err)
	}

	if len(os.Args) > 1 && os.Args[1] == "serve" {
		serveOpts, err := serve.ParseOptions(os.Args[2:])
		if err != nil {
			log.Fatal(err)
		}
		if err := serve.Run(serveOpts); err != nil {
			log.Fatal(err)
		}
		return
	}

	opts := parseOptions()
	ctx := context.Background()
	sandboxLimits := parseSandboxLimits(opts.sandboxLimits)

	if opts.taskWorkerPool {
		if err := worker.RunPool(ctx, os.Stdin, os.Stdout, opts.allowOutsideRead, sandboxLimits); err != nil {
			log.Fatal(err)
		}
		return
	}

	if opts.taskWorker {
		if err := worker.Run(ctx, os.Stdin, os.Stdout, opts.allowOutsideRead, sandboxLimits); err != nil {
			log.Fatal(err)
		}
		return
	}

	if opts.prompt != "" {
		if err := oneshot.Run(ctx, oneshot.Options{Prompt: opts.prompt, SessionID: opts.sessionID, AllowOutsideRead: opts.allowOutsideRead, StreamMA: opts.streamMA}); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := interactive.Run(ctx, interactive.Options{SessionID: opts.sessionID, AllowOutsideRead: opts.allowOutsideRead, StreamMA: opts.streamMA, TokenTracer: opts.tokenTracer, TokenTracerOpen: opts.tokenTracerOpen, TokenTracerPort: opts.tokenTracerPort}); err != nil {
		log.Fatal(err)
	}
}
