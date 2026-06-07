package main

import (
	"context"
	stderrors "errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"

	goerrors "github.com/go-errors/errors"
	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/webapp"
)

var webAddr string
var webRoot string
var webStaticDir string
var webModel string

func webCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Start the local LAPP web app",
		RunE:  runWeb,
	}
	cmd.Flags().StringVar(&webAddr, "addr", "127.0.0.1:0", "local address to listen on")
	cmd.Flags().StringVar(&webRoot, "root", "", "workspace root (default ~/.lapp/workspaces)")
	cmd.Flags().StringVar(&webStaticDir, "static-dir", "", "static frontend directory override")
	cmd.Flags().StringVar(&webModel, "model", "", "override semantic labeling model")
	return cmd
}

func runWeb(cmd *cobra.Command, _ []string) error {
	handler, err := webapp.NewHandler(webapp.ServerConfig{
		Root:      webRoot,
		StaticDir: webStaticDir,
		APIKey:    os.Getenv("OPENROUTER_API_KEY"),
		Model:     webModel,
	})
	if err != nil {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(cmd.Context(), "tcp", webAddr)
	if err != nil {
		return goerrors.Errorf("listen: %w", err)
	}
	server := &http.Server{Handler: handler}
	go func() {
		<-cmd.Context().Done()
		_ = server.Shutdown(context.Background())
	}()
	url := "http://" + listener.Addr().String()
	slog.Info("LAPP web started", "url", url)
	fmt.Println(url)
	if err := server.Serve(listener); err != nil && !stderrors.Is(err, http.ErrServerClosed) {
		return goerrors.Errorf("serve web: %w", err)
	}
	return nil
}
