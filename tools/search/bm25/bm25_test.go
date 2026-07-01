// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package bm25

import (
	"math"
	"testing"
)

func TestOkapi_BuildAndScores(t *testing.T) {
	o := NewOkapi(DefaultOkapiConfig())
	docs := [][]string{
		{"hello", "world"},
		{"hello", "golang", "world"},
		{"test", "code"},
	}
	o.Build(docs)

	// "golang" appears in only 1 of 3 docs → positive IDF → non-zero score.
	scores := o.Scores([]string{"golang"})
	if len(scores) != 3 {
		t.Fatalf("expected 3 scores, got %d", len(scores))
	}
	if scores[0] != 0 {
		t.Errorf("doc 0 should have zero score for 'golang', got %f", scores[0])
	}
	if scores[1] <= 0 {
		t.Errorf("doc 1 should have non-zero score for 'golang', got %f", scores[1])
	}
	if scores[2] != 0 {
		t.Errorf("doc 2 should have zero score for 'golang', got %f", scores[2])
	}
}

func TestOkapi_TopN(t *testing.T) {
	o := NewOkapi(DefaultOkapiConfig())
	docs := [][]string{
		{"hello", "world"},
		{"hello", "golang", "world"},
		{"unique"},
	}
	o.Build(docs)

	// "unique" appears in only 1 doc → highest score for that doc.
	indices, scores := o.TopN([]string{"unique"}, 2)
	if len(indices) != 2 {
		t.Fatalf("expected 2 results, got %d", len(indices))
	}
	// Only doc 2 has "unique". The other two docs are tied at zero, so sort
	// order after index 0 is implementation-defined. Just check doc 2 is first.
	if indices[0] != 2 {
		t.Errorf("expected doc 2 as top result for 'unique', got doc %d", indices[0])
	}
	if scores[0] <= 0 {
		t.Errorf("expected positive score for doc 2, got %f", scores[0])
	}
}

func TestOkapi_TopN_MultipleQuery(t *testing.T) {
	o := NewOkapi(DefaultOkapiConfig())
	docs := [][]string{
		{"hello", "world"},
		{"hello", "golang"},
		{"hello", "golang", "bm25"},
	}
	o.Build(docs)

	// "bm25" appears in only 1 doc.
	indices, _ := o.TopN([]string{"bm25"}, 3)
	if len(indices) == 0 {
		t.Fatal("expected at least one result for 'bm25'")
	}
	if indices[0] != 2 {
		t.Errorf("expected doc 2 as top result for 'bm25', got doc %d", indices[0])
	}
}

func TestOkapi_IDF_UnknownTerm(t *testing.T) {
	o := NewOkapi(DefaultOkapiConfig())
	o.Build([][]string{{"hello", "world"}})
	scores := o.Scores([]string{"nonexistent"})
	if len(scores) != 1 {
		t.Fatalf("expected 1 score, got %d", len(scores))
	}
	if scores[0] != 0 {
		t.Errorf("expected 0 for unknown term, got %f", scores[0])
	}
}

func TestOkapi_TermInAllDocs(t *testing.T) {
	o := NewOkapi(DefaultOkapiConfig())
	docs := [][]string{
		{"hello", "world"},
		{"hello", "golang"},
	}
	o.Build(docs)

	// "hello" appears in both docs → negative IDF → clamped to 0.
	scores := o.Scores([]string{"hello"})
	if len(scores) != 2 {
		t.Fatalf("expected 2 scores, got %d", len(scores))
	}
	// IDF of "hello": log((2-2+0.5)/(2+0.5)) = log(0.5/2.5) ≈ -1.6 → clamped to 0.
	if scores[0] != 0 {
		t.Errorf("expected 0 for term in all docs, got %f", scores[0])
	}
	if scores[1] != 0 {
		t.Errorf("expected 0 for term in all docs, got %f", scores[1])
	}
}

func TestOkapi_EmptyCorpus(t *testing.T) {
	o := NewOkapi(DefaultOkapiConfig())
	o.Build([][]string{{}})
	scores := o.Scores([]string{"hello"})
	if len(scores) != 1 {
		t.Errorf("expected 1 score for single empty doc, got %d", len(scores))
	}
}

func TestOkapi_EmptyQuery(t *testing.T) {
	o := NewOkapi(DefaultOkapiConfig())
	o.Build([][]string{{"hello", "world"}})
	scores := o.Scores(nil)
	if len(scores) != 1 {
		t.Errorf("expected 1 score, got %d", len(scores))
	}
	if scores[0] != 0 {
		t.Errorf("expected 0 for empty query, got %f", scores[0])
	}
}

func TestOkapi_SetDocData(t *testing.T) {
	o := NewOkapi(DefaultOkapiConfig())
	// 3 docs so a term in 1 of them gets positive IDF.
	docLengths := []int{2, 3, 1}
	docFreq := map[string]int{"hello": 2, "world": 1, "golang": 1}
	docTerms := []map[string]int{
		{"hello": 1, "world": 1},
		{"hello": 1, "golang": 1, "world": 1},
		{"hello": 1},
	}
	o.SetDocData(docLengths, docFreq, docTerms)

	if o.DocCount() != 3 {
		t.Errorf("expected 3 docs, got %d", o.DocCount())
	}
	if o.AvgDocLen() != 2.0 {
		t.Errorf("expected avg doc len 2.0, got %f", o.AvgDocLen())
	}

	// "golang" appears in only 1 of 3 docs → positive IDF.
	scores := o.Scores([]string{"golang"})
	if len(scores) != 3 {
		t.Fatalf("expected 3 scores, got %d", len(scores))
	}
	if scores[0] != 0 {
		t.Errorf("doc 0 should have zero 'golang' score, got %f", scores[0])
	}
	if scores[1] <= 0 {
		t.Errorf("doc 1 should have non-zero 'golang' score, got %f", scores[1])
	}
	if scores[2] != 0 {
		t.Errorf("doc 2 should have zero 'golang' score, got %f", scores[2])
	}
}

func TestOkapi_AddDocument(t *testing.T) {
	o := NewOkapi(DefaultOkapiConfig())
	o.Build([][]string{{"hello", "world"}, {"foo"}})

	id := o.AddDocument([]string{"hello", "golang"})
	if id != 2 {
		t.Errorf("expected id 2, got %d", id)
	}
	if o.DocCount() != 3 {
		t.Errorf("expected 3 docs, got %d", o.DocCount())
	}

	// "golang" appears in only 1 of 3 docs → positive IDF.
	scores := o.Scores([]string{"golang"})
	if scores[0] != 0 {
		t.Errorf("doc 0 should have zero 'golang' score, got %f", scores[0])
	}
	if scores[1] != 0 {
		t.Errorf("doc 1 should have zero 'golang' score, got %f", scores[1])
	}
	if scores[2] <= 0 {
		t.Errorf("doc 2 should have non-zero 'golang' score, got %f", scores[2])
	}
}

func TestOkapi_UpdateDocument(t *testing.T) {
	o := NewOkapi(DefaultOkapiConfig())
	o.Build([][]string{
		{"hello", "world"},
		{"foo", "bar"},
		{"extra"},
	})

	// Update doc 1 from {"foo", "bar"} → {"hello", "golang"}.
	o.UpdateDocument(1, []string{"foo", "bar"}, []string{"hello", "golang"})

	// "golang" in only 1 of 3 docs → positive IDF.
	scores := o.Scores([]string{"golang"})
	if scores[0] != 0 {
		t.Errorf("doc 0 should have zero 'golang' score, got %f", scores[0])
	}
	if scores[1] <= 0 {
		t.Errorf("doc 1 should have non-zero 'golang' score, got %f", scores[1])
	}
	if scores[2] != 0 {
		t.Errorf("doc 2 should have zero 'golang' score, got %f", scores[2])
	}

	// "foo" is no longer in any doc.
	scores = o.Scores([]string{"foo"})
	for i, s := range scores {
		if s != 0 {
			t.Errorf("doc %d should have zero 'foo' score after update, got %f", i, s)
		}
	}
}

func TestOkapi_RemoveLastDocument(t *testing.T) {
	o := NewOkapi(DefaultOkapiConfig())
	o.Build([][]string{{"hello"}, {"world"}})
	o.RemoveLastDocument()

	if o.DocCount() != 1 {
		t.Errorf("expected 1 doc, got %d", o.DocCount())
	}
	// "world" should no longer be in doc freq.
	if _, ok := o.DocFreq()["world"]; ok {
		t.Error("expected 'world' to be removed from doc freq")
	}
	// "hello" should still be there.
	if _, ok := o.DocFreq()["hello"]; !ok {
		t.Error("expected 'hello' to remain in doc freq")
	}
}

func TestOkapi_RoundTripData(t *testing.T) {
	o := NewOkapi(DefaultOkapiConfig())
	docs := [][]string{
		{"hello", "world"},
		{"golang", "bm25", "search"},
	}
	o.Build(docs)

	dl := o.DocLengths()
	df := o.DocFreq()
	dt := o.DocTerms()

	o2 := NewOkapi(DefaultOkapiConfig())
	o2.SetDocData(dl, df, dt)

	// Both should produce identical scores for a rare term.
	scores1 := o.Scores([]string{"bm25"})
	scores2 := o2.Scores([]string{"bm25"})
	if len(scores1) != len(scores2) {
		t.Fatalf("score count mismatch: %d vs %d", len(scores1), len(scores2))
	}
	for i := range scores1 {
		if math.Abs(scores1[i]-scores2[i]) > 1e-9 {
			t.Errorf("score[%d] mismatch: %f vs %f", i, scores1[i], scores2[i])
		}
	}
}

func TestOkapi_ScoringStability(t *testing.T) {
	// Verify that repeated scoring with the same data gives consistent results.
	o := NewOkapi(OkapiConfig{K1: 1.2, B: 0.5})
	docs := [][]string{
		{"hello", "world"},
		{"hello", "golang", "world"},
		{"unique", "token"},
	}
	o.Build(docs)

	s1 := o.Scores([]string{"hello", "world"})
	s2 := o.Scores([]string{"hello", "world"})
	if len(s1) != len(s2) {
		t.Fatalf("score length mismatch: %d vs %d", len(s1), len(s2))
	}
	for i := range s1 {
		if s1[i] != s2[i] {
			t.Errorf("score[%d] unstable: %f vs %f", i, s1[i], s2[i])
		}
	}
}
