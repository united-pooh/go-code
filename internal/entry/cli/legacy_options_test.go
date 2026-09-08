package cli

import (
	"flag"
	"io"
	"testing"
)

func TestParseLegacyOptionsStillAcceptsWorkerAndInteractiveFlags(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	got := parseOptionsFrom(flags, []string{"-p", "hello", "-s", "session", "-task-worker", "-yolo"})
	if got.prompt != "hello" || got.sessionID != "session" || !got.taskWorker || !got.allowOutsideRead {
		t.Fatalf("legacy options = %#v", got)
	}
}
