package server

import (
	"testing"

	"nuvio/internal/metadata"
	"nuvio/internal/model"
)

func TestPickCandidateMatchesTypeAndYear(t *testing.T) {
	candidates := []metadata.Candidate{
		{ID: 1, Type: "tv", Title: "The Matrix", Year: 2019},
		{ID: 2, Type: "movie", Title: "The Matrix Reloaded", Year: 2003},
		{ID: 3, Type: "movie", Title: "The Matrix", Year: 1999},
	}
	got := pickCandidate(candidates, "The Matrix", 1999, model.TypeMovie)
	if got == nil || got.ID != 3 {
		t.Fatalf("picked %+v", got)
	}
	if got := pickCandidate(candidates, "The Matrix", 0, model.TypeSeries); got == nil || got.ID != 1 {
		t.Fatalf("series pick = %+v", got)
	}
}

func TestStripJSONFence(t *testing.T) {
	if got := stripJSONFence("```json\n{\"a\":1}\n```"); got != `{"a":1}` {
		t.Fatalf("got %q", got)
	}
}
