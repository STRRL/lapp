package main

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	goerrors "github.com/go-errors/errors"
	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/webapp"
	"golang.org/x/term"
)

var quickAddr string
var quickNoOpen bool
var filterService string
var quickName string

// runQuick is `lapp [file|-]`: import the log, find patterns, and open the
// pattern list in the browser. With no file and piped stdin it reads stdin.
func runQuick(cmd *cobra.Command, args []string) error {
	name, body, closeFn, err := openInput(args)
	if err != nil {
		return err
	}
	if body == nil {
		return cmd.Help()
	}
	defer closeFn()
	if quickName != "" {
		name = quickName
	}

	fmt.Fprintln(os.Stderr, "Finding patterns…")
	id, err := webapp.ImportLog(cmd.Context(), "", os.Getenv("OPENROUTER_API_KEY"), "", name, body)
	if err != nil {
		return err
	}
	return serveWeb(cmd.Context(), quickAddr, "/?ws="+url.QueryEscape(id), !quickNoOpen)
}

func filterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "filter [file|-]",
		Short: "Print a log without the lines of patterns filtered as noise",
		Long: `Find patterns in the log, drop the lines of patterns filtered as noise
(the rules saved for this service, or lapp's suggestions when none are saved),
and print the rest. The input file is never modified.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, body, closeFn, err := openInput(args)
			if err != nil {
				return err
			}
			if body == nil {
				return goerrors.New("give a log file, or pipe logs in")
			}
			defer closeFn()
			return webapp.FilterLog(cmd.Context(), "", filterService, name, body, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&filterService, "service", "", "use the noise rules saved for this service (default: from the file name)")
	return cmd
}

// openInput returns the input name and reader for a file argument, "-", or
// piped stdin. body is nil when there is nothing to read.
func openInput(args []string) (string, io.Reader, func(), error) {
	if len(args) == 1 && args[0] != "-" {
		f, err := os.Open(args[0])
		if err != nil {
			return "", nil, nil, err
		}
		return filepath.Base(args[0]), f, func() { _ = f.Close() }, nil
	}
	if len(args) == 1 || !term.IsTerminal(int(os.Stdin.Fd())) {
		return "stdin.log", os.Stdin, func() {}, nil
	}
	return "", nil, func() {}, nil
}

// serveWeb starts the local web app and optionally opens path in a browser.
func serveWeb(ctx context.Context, addr, path string, open bool) error {
	handler, err := webapp.NewHandler(webapp.ServerConfig{
		Root:         webRoot,
		StaticDir:    webStaticDir,
		APIKey:       os.Getenv("OPENROUTER_API_KEY"),
		Model:        webModel,
		VictoriaLogs: victoriaLogsAuthFromEnv(),
	})
	if err != nil {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return goerrors.Errorf("listen: %w", err)
	}
	server := &http.Server{Handler: handler}
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()
	u := "http://" + listener.Addr().String() + path
	slog.Info("lapp web started", "url", u)
	fmt.Println(u)
	if open {
		openBrowser(u)
	}
	if err := server.Serve(listener); err != nil && !stderrors.Is(err, http.ErrServerClosed) {
		return goerrors.Errorf("serve web: %w", err)
	}
	return nil
}

func openBrowser(u string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", u)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		c = exec.Command("xdg-open", u)
	}
	if err := c.Start(); err != nil {
		slog.Warn("could not open a browser", "error", err)
	}
}

var summaryJSON bool
var summaryService string
var summaryWorkspace string

func summaryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "summary [file|-]",
		Short: "Print patterns, counts and spike/noise marks as JSON, for scripts and coding agents",
		Long: `Print what lapp found in a log: patterns with counts, noise marks, spikes,
first-seen times and the "Start here" shortlist. With a file or a pipe, lapp
finds the patterns on the fly (no LLM, nothing kept) and applies the noise
rules saved for the service. With --workspace, it summarizes an earlier import,
including the comparison with the import before it.

  lapp summary app.log --json | jq '.patterns[] | select(.spike)'`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var s *webapp.Summary
			var err error
			if summaryWorkspace != "" {
				s, err = webapp.SummarizeWorkspace(cmd.Context(), "", summaryWorkspace)
			} else {
				name, body, closeFn, oerr := openInput(args)
				if oerr != nil {
					return oerr
				}
				if body == nil {
					return goerrors.New("give a log file, pipe logs in, or use --workspace")
				}
				defer closeFn()
				s, err = webapp.SummarizeLog(cmd.Context(), "", summaryService, name, body)
			}
			if err != nil {
				return err
			}
			if summaryJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(s)
			}
			s.WriteText(os.Stdout)
			return nil
		},
	}
	cmd.Flags().BoolVar(&summaryJSON, "json", false, "print JSON")
	cmd.Flags().StringVar(&summaryService, "service", "", "use the noise rules saved for this service (default: from the file name)")
	cmd.Flags().StringVar(&summaryWorkspace, "workspace", "", "summarize an earlier import (its id from the web app URL, e.g. apache)")
	return cmd
}
