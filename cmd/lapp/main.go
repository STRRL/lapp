package main

import (
	"context"
	"os"

	"github.com/cloudwego/eino/callbacks"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/tape"
	"github.com/strrl/lapp/pkg/tracing"
)

func main() {
	// Load .env file if present (does not override existing env vars)
	_ = godotenv.Load()

	flush := tracing.InitLangfuse()
	otelShutdown := tracing.InitOTel(context.Background())
	callbacks.AppendGlobalHandlers(tape.NewSlogHandler())

	root := &cobra.Command{
		Use:   "lapp",
		Short: "Log Auto Pattern Pipeline",
		Long:  "LAPP automatically discovers log templates and stores structured results for querying.",
	}

	root.AddCommand(workspaceCmd())

	err := root.Execute()
	otelShutdown()
	flush()

	if err != nil {
		os.Exit(1)
	}
}
