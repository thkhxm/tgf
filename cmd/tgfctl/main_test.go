package main

import (
	"context"
	"testing"
)

func TestUnknownSubcommandFails(t *testing.T) {
	t.Parallel()
	if err := run(context.Background(), []string{"unknown"}); err == nil {
		t.Fatal("expected unknown subcommand to fail")
	}
}
