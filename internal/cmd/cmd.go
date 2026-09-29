// Package cmd implements the ktpls command line.
package cmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Iryoda/ktpls/internal/protocol"
	"github.com/Iryoda/ktpls/internal/server"
)

const usage = `ktpls is a Kotlin language server.

Usage:
  ktpls [serve] [flags]   run the language server on stdin/stdout
  ktpls version           print the version

Flags:
`

// Main runs the command and returns the process exit code.
func Main(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "version":
			fmt.Println("ktpls", server.Version)
			return 0
		case "serve":
			args = args[1:]
		}
	}

	fs := flag.NewFlagSet("ktpls", flag.ContinueOnError)
	logfile := fs.String("logfile", "", "write logs to this file (default: stderr)")
	verbose := fs.Bool("v", false, "enable debug logging")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Println("ktpls", server.Version)
		return 0
	}

	var logw io.Writer = os.Stderr
	if *logfile != "" {
		f, err := os.OpenFile(*logfile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ktpls: %v\n", err)
			return 1
		}
		defer f.Close()
		logw = f
	}
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(logw, &slog.HandlerOptions{Level: level}))

	return serve(log, os.Stdin, os.Stdout)
}

// serve runs the server over r/w until the client exits. Stdout carries
// the protocol, so nothing else may ever write to it.
func serve(log *slog.Logger, r io.Reader, w io.Writer) int {
	log.Info("starting", "version", server.Version, "pid", os.Getpid())
	conn := protocol.NewConn(r, w, log)
	srv := server.New(conn, log)

	errc := make(chan error, 1)
	go func() { errc <- conn.Run(context.Background(), srv.Handle) }()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	select {
	case <-srv.Exited():
	case err := <-errc:
		if err != nil {
			log.Error("connection failed", "err", err)
		} else {
			log.Info("client closed the connection")
		}
	case sig := <-sigs:
		log.Info("terminated", "signal", sig.String())
	}
	// However we exit, stop the build and the daemons it started.
	srv.Close()
	if srv.ShutdownReceived() {
		log.Info("exiting")
		return 0
	}
	log.Warn("exiting without shutdown")
	return 1
}
