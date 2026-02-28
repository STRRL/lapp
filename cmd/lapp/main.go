package main

import (
	"os"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/tracing"
)

var dbPath string

func main() {
	// Load .env file if present (does not override existing env vars)
	_ = godotenv.Load()

	flush := tracing.InitLangfuse()

	root := &cobra.Command{
		Use:   "lapp",
		Short: "Log Auto Pattern Pipeline",
		Long:  "LAPP automatically discovers log templates and stores structured results for querying.",
	}

	root.PersistentFlags().StringVar(&dbPath, "db", "lapp.duckdb", "path to DuckDB database")

	root.AddCommand(analyzeCmd())
	root.AddCommand(debugCmd())

	err := root.Execute()
	flush()

	if err != nil {
		os.Exit(1)
	}
}
