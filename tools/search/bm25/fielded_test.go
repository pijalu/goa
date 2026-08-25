package bm25

import "testing"

func TestFieldedScorerPrioritizesSymbolAndCoverage(t *testing.T) {
	docs := []DocumentMeta{
		{Path: "pkg/auth.go", Symbol: "ValidateToken", Signature: "func ValidateToken(token string) error", Content: "return nil"},
		{Path: "pkg/other.go", Symbol: "Validate", Signature: "func Validate()", Content: "token token token"},
	}
	s := NewFieldedScorer(docs, DefaultFieldedWeights())
	a, coverageA, confidenceA := s.Score("validate token", docs[0], 0)
	b, coverageB, confidenceB := s.Score("validate token", docs[1], 1)
	if a <= b {
		t.Fatalf("symbol/signature match should rank first: %.3f <= %.3f", a, b)
	}
	if coverageA < coverageB || confidenceA <= confidenceB {
		t.Fatalf("expected stronger coverage/confidence, got %.2f/%.2f vs %.2f/%.2f", coverageA, confidenceA, coverageB, confidenceB)
	}
}

func TestExpandQueryIsBoundedAndAddsAliases(t *testing.T) {
	got := expandQuery([]string{"authentication", "error"})
	if len(got) > 8 {
		t.Fatalf("expansion exceeded bound: %d", len(got))
	}
	seen := false
	for _, term := range got {
		if term == "auth" {
			seen = true
		}
	}
	if !seen {
		t.Fatal("authentication alias missing")
	}
}
