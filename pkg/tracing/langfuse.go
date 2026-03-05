package tracing

import (
	"log/slog"
	"os"

	"github.com/cloudwego/eino-ext/callbacks/langfuse"
	"github.com/cloudwego/eino/callbacks"
)

// InitLangfuse registers a global Langfuse callback handler if
// LANGFUSE_HOST, LANGFUSE_PUBLIC_KEY, and LANGFUSE_SECRET_KEY are set.
// Returns a flush function that must be called before process exit.
func InitLangfuse() (flush func()) {
	host := os.Getenv("LANGFUSE_HOST")
	publicKey := os.Getenv("LANGFUSE_PUBLIC_KEY")
	secretKey := os.Getenv("LANGFUSE_SECRET_KEY")

	if host == "" || publicKey == "" || secretKey == "" {
		return func() {}
	}

	handler, flusher := langfuse.NewLangfuseHandler(&langfuse.Config{
		Host:      host,
		PublicKey: publicKey,
		SecretKey: secretKey,
	})

	callbacks.AppendGlobalHandlers(handler)
	slog.Info("langfuse tracing enabled", "host", host)

	return flusher
}
