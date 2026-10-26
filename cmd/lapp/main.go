package main

import (
	"context"
	"os"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/tracing"
)

func main() {
	// Load .env file if present (does not override existing env vars)
	_ = godotenv.Load()

	flush := tracing.InitLangfuse()
	otelShutdown := tracing.InitOTel(context.Background())

	root := &cobra.Command{
		Use:   "lapp [file|-]",
		Short: "Find the patterns in your logs. Filter out the noise.",
		Long: `Find the patterns in your logs. Filter out the noise.

  lapp app.log                  import a log and open its patterns in the browser
  kubectl logs pod/api | lapp --name api.log   same, from a pipe
  lapp filter app.log > clean.log   print the log without lines filtered as noise`,
		Args: cobra.MaximumNArgs(1),
		RunE: runQuick,
	}
	root.Flags().StringVar(&quickAddr, "addr", "127.0.0.1:0", "local address for the web app")
	root.Flags().BoolVar(&quickNoOpen, "no-open", false, "don't open a browser")
	root.Flags().StringVar(&quickName, "name", "", "name for piped logs, e.g. checkout.log (also picks which saved noise rules apply)")

	root.AddCommand(workspaceCmd())
	root.AddCommand(webCmd())
	root.AddCommand(filterCmd())

	err := root.Execute()
	otelShutdown()
	flush()

	if err != nil {
		os.Exit(1)
	}
}
