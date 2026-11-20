package server

import "testing"

func TestStripJSONFence(t *testing.T) {
	if got := stripJSONFence("```json\n{\"a\":1}\n```"); got != `{"a":1}` {
		t.Fatalf("got %q", got)
	}
}
