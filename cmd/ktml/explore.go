package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/karl-tye/ktml/internal/config"
	"github.com/karl-tye/ktml/internal/explorer"
)

// exploreCmd runs ktml explore [-addr host:port] [root]: the Component
// Explorer over the stories under root (default "."), on a loopback address
// only. It reads the explorer section of root's ktml.config.json and returns
// the process exit status.
func exploreCmd(stdout, stderr io.Writer, args []string) int {
	flags := flag.NewFlagSet("explore", flag.ContinueOnError)
	flags.SetOutput(stderr)
	addr := flags.String("addr", "127.0.0.1:7332", "loopback `address` to serve on; port 0 picks a free port")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	root := "."
	switch flags.NArg() {
	case 0:
	case 1:
		root = flags.Arg(0)
	default:
		fmt.Fprintln(stderr, "usage: ktml explore [-addr host:port] [root]")
		return 2
	}
	cfg, err := config.Load(filepath.Join(root, "ktml.config.json"))
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	listener, err := explorer.Listen(*addr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	server, err := explorer.New(explorer.Options{Root: root, Config: cfg.Explorer})
	if err != nil {
		listener.Close()
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer server.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(stdout, "KTML Explorer on http://%s/\n", listener.Addr())
	if err := server.Serve(ctx, listener); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// devExplorer is the Component Explorer that ktml dev serves.
type devExplorer struct {
	server *explorer.Server
	// url is its base URL, such as http://127.0.0.1:7332.
	url string
	// wait waits for it to stop.
	wait func() error
}

// startDevExplorer starts the Component Explorer that ktml dev serves when
// dev.explorer is configured: an in-process Explorer over root, with its own
// loopback server, livereload hub, and watcher, until ctx ends. It logs the
// Explorer's URL to stdout.
func startDevExplorer(ctx context.Context, stdout io.Writer, root string, cfg *config.Config) (*devExplorer, error) {
	listener, err := explorer.Listen(cfg.Dev.Explorer.Addr)
	if err != nil {
		return nil, fmt.Errorf("dev explorer: %w", err)
	}
	server, err := explorer.New(explorer.Options{Root: root, Config: cfg.Explorer})
	if err != nil {
		listener.Close()
		return nil, err
	}
	url := "http://" + listener.Addr().String()
	fmt.Fprintf(stdout, "KTML Explorer on %s/\n", url)
	done := make(chan error, 1)
	go func() {
		err := server.Serve(ctx, listener)
		_ = listener.Close()
		done <- errors.Join(err, server.Close())
	}()
	return &devExplorer{server: server, url: url, wait: func() error { return <-done }}, nil
}
