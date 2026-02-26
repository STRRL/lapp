package main

import (
	"os"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

var dbPath string

func main() {
	// Load .env file if present (does not override existing env vars)
	_ = godotenv.Load()

	root := &cobra.Command{
		Use:   "lapp",
		Short: "Log Auto Pattern Pipeline",
		Long:  "LAPP automatically discovers log templates and stores structured results for querying.",
	}

	root.PersistentFlags().StringVar(&dbPath, "db", "lapp.duckdb", "path to DuckDB database")

	root.AddCommand(ingestCmd())
	root.AddCommand(templatesCmd())
	root.AddCommand(queryCmd())
	root.AddCommand(analyzeCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
