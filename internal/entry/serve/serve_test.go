package serve

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestServeOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    Options
		wantErr string
	}{
		{name: "defaults", want: Options{Listen: defaultServeListen}},
		{name: "open", args: []string{"--open"}, want: Options{Listen: defaultServeListen, Open: true}},
		{name: "IPv4 loopback", args: []string{"--listen", "127.0.0.1:8000"}, want: Options{Listen: "127.0.0.1:8000"}},
		{name: "IPv6 loopback", args: []string{"--listen", "[::1]:8000"}, want: Options{Listen: "[::1]:8000"}},
		{name: "localhost", args: []string{"--listen", "localhost:8000"}, want: Options{Listen: "localhost:8000"}},
		{name: "wildcard rejected", args: []string{"--listen", "0.0.0.0:8000"}, wantErr: "must be loopback"},
		{name: "remote rejected", args: []string{"--listen", "192.168.1.5:8000"}, wantErr: "must be loopback"},
		{name: "unknown flag", args: []string{"--wat"}, wantErr: "flag provided but not defined"},
		{name: "positional rejected", args: []string{"extra"}, wantErr: "does not accept positional"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseOptions(test.args)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("parseServeOptions() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("parseServeOptions() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestRunServePrintsFragmentURLAndStopsOnContextCancel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ctx, cancel := context.WithCancel(context.Background())
	var output bytes.Buffer
	opened := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- runServe(ctx, Options{Listen: "127.0.0.1:0", Open: true}, &output, func(url string) { opened <- url })
	}()
	var url string
	select {
	case url = <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not emit bootstrap URL")
	}
	if !strings.Contains(url, "#bootstrap=") || !strings.Contains(output.String(), url) {
		t.Fatalf("bootstrap URL = %q output=%q", url, output.String())
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop after context cancellation")
	}
}
