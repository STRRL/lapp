package tracing

import (
	"testing"

	"github.com/cloudwego/eino/callbacks"
)

func TestSlogEinoHandlerImplementsCallbackHandler(t *testing.T) {
	t.Helper()
	var _ callbacks.Handler = (*SlogEinoHandler)(nil)
}
