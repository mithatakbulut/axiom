package logging

import (
	"context"
	"testing"
)

func TestCorrelationIDContext(t *testing.T) {
	id := NewCorrelationID()
	if len(id) != 32 {
		t.Fatalf("correlation ID length=%d", len(id))
	}
	ctx := WithCorrelationID(context.Background(), id)
	if got := CorrelationID(ctx); got != id {
		t.Fatalf("correlation ID=%q", got)
	}
}
