package main

import (
	"testing"

	"github.com/toolingdev/jadeck/sdk/addon/addontest"
)

func TestOverThePipe(t *testing.T) {
	h := addontest.Start(t, spec())
	if err := h.TryRequest("open", map[string]any{"conn": "m", "type": "osc", "config": map[string]any{"preset": "wing"}}, nil); err == nil {
		t.Fatal("a console without an address opened")
	}
	h.Open("m", "osc", map[string]any{"preset": "wing", "host": "127.0.0.1"})
	var n int
	h.Call("m", "mixer.maxChannel", nil, &n)
	if n < 32 {
		t.Fatalf("max channel %d", n)
	}
}
