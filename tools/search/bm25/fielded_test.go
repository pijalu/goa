package bm25

import (
	"math"
	"testing"
)

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

func TestDedupeTokensPreservesOrderAndDropsRepeats(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, []string{}},
		{"unique", []string{"a", "b"}, []string{"a", "b"}},
		{"repeats", []string{"a", "b", "a", "c", "b", "a"}, []string{"a", "b", "c"}},
	}
	for _, tc := range cases {
		got := dedupeTokens(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
			}
		}
	}
}

// TestScoreClampsSubOneFieldAverages pins the avg<1 clamp: doc b's signature
// holds one token while the mean signature length is 0.5; the clamp caps the
// divisor at 1 so k stays finite. Without the clamp a zero-length field would
// poison the score with ±Inf/NaN.
func TestScoreClampsSubOneFieldAverages(t *testing.T) {
	docs := []DocumentMeta{
		{Path: "a.go", Symbol: "Alpha", Signature: "", Content: "alpha"},
		{Path: "b.go", Symbol: "Beta", Signature: "beta", Content: "beta"},
	}
	s := NewFieldedScorer(docs, DefaultFieldedWeights())
	if clampedAvg(0) != 1 || clampedAvg(0.5) != 1 {
		t.Fatal("clampedAvg must floor averages below one at one")
	}
	score, _, _ := s.Score("beta", docs[1], 1)
	if math.IsNaN(score) || math.IsInf(score, 0) || score <= 0 {
		t.Fatalf("score must stay finite positive, got %v", score)
	}
	idf := math.Log(1 + (float64(2)-float64(s.df["beta"])+0.5)/(float64(s.df["beta"])+0.5))
	want := 0.0
	for f := 0; f < 5; f++ {
		tf := float64(s.docs[1].terms[f]["beta"])
		if tf == 0 {
			continue
		}
		k := 1.5 * (0.25 + 0.75*float64(s.docs[1].lengths[f])/clampedAvg(s.avg[f]))
		want += s.fieldWeight(f) * idf * (tf * 2.5) / (tf + k)
	}
	want *= identifierBoost("beta", docs[1])
	if math.Abs(score-want) > 1e-9 {
		t.Fatalf("score %.12f != clamped formula %.12f", score, want)
	}
}

func TestClampedAvgKeepsLargerAverages(t *testing.T) {
	if got := clampedAvg(2.5); got != 2.5 {
		t.Fatalf("clampedAvg(2.5) = %v, want untouched", got)
	}
	if got := clampedAvg(1); got != 1 {
		t.Fatalf("clampedAvg(1) = %v, want 1", got)
	}
}

func TestIdentifierBoostMatchesSymbolOrBaseName(t *testing.T) {
	d := DocumentMeta{Path: "pkg/auth_handler.go", Symbol: "ValidateToken"}
	cases := []struct {
		query string
		want  float64
	}{
		{"ValidateToken", 1.8},
		{"  ValidateToken  ", 1.8},
		{"auth_handler.go", 1.8},
		{"AUTH_HANDLER.GO", 1.8},
		{"validatetoken", 1.8},
		{"validate tokens", 1},
		{"", 1},
		{"   ", 1},
	}
	for _, tc := range cases {
		if got := identifierBoost(tc.query, d); got != tc.want {
			t.Errorf("%q: got %v want %v", tc.query, got, tc.want)
		}
	}
}

func TestConfidenceForSaturatesAtOneAndPassesCoverageThrough(t *testing.T) {
	if got := confidenceFor(0, 0.42); got != 0.42 {
		t.Fatalf("zero score should pass coverage through: got %v", got)
	}
	if got := confidenceFor(-3, 0.5); got != 0.5 {
		t.Fatalf("non-positive score should pass coverage through: got %v", got)
	}
	if got := confidenceFor(10_000, 1); got != 1 {
		t.Fatalf("huge score must saturate at exactly 1: got %v", got)
	}
	low := confidenceFor(1, 0.5)
	high := confidenceFor(50, 0.5)
	if !(low < high && high < 1) {
		t.Fatalf("confidence must grow with score but stay capped: %v %v", low, high)
	}
}
