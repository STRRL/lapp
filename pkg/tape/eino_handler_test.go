package tape

import (
	"testing"

	"github.com/cloudwego/eino/callbacks"
)

func TestEinoHandlerImplementsCallbackHandler(t *testing.T) {
	var _ callbacks.Handler = (*EinoHandler)(nil)
}
