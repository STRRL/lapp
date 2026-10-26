package main

import (
	"github.com/spf13/cobra"
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
	return serveWeb(cmd.Context(), webAddr, "/", false)
}
