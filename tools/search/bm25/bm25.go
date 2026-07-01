// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package bm25

import (
	"math"
	"sort"
)

// Okapi is an implementation of the BM25Okapi ranking function, the most
// widely used BM25 variant. It scores documents by their relevance to a
// query using term frequency (TF) and inverse document frequency (IDF) with
// document-length normalisation.
//
// The scoring formula is:
//
//	score(d, q) = Σ IDF(t) · (tf(t,d) · (k1 + 1)) / (tf(t,d) + k1 · (1 - b + b · |d| / avgdl))
//
// where:
//   - IDF(t) = log((N - n(t) + 0.5) / (n(t) + 0.5))
//   - k1 controls term-frequency saturation (1.2–2.0 typical)
//   - b controls document-length normalisation (0.0–1.0, 0.75 typical)
type Okapi struct {
	avgDocLen  float64
	docCount   int
	docLengths []int
	docFreq    map[string]int   // term → number of documents containing it
	docTerms   []map[string]int // per-document: term → frequency
	k1         float64
	b          float64
}

// OkapiConfig carries optional parameters for NewOkapi.
type OkapiConfig struct {
	K1 float64 // term-frequency saturation (default 1.5)
	B  float64 // document-length normalisation (default 0.75)
}

// DefaultOkapiConfig returns sensible defaults for code search.
func DefaultOkapiConfig() OkapiConfig {
	return OkapiConfig{K1: 1.5, B: 0.75}
}

// NewOkapi creates a BM25Okapi scorer. Initialise with Build or
// SetDocData before scoring queries. For incremental use, call AddDocument,
// RemoveDocument, or UpdateDocument after initial setup.
func NewOkapi(cfg OkapiConfig) *Okapi {
	if cfg.K1 <= 0 {
		cfg.K1 = 1.5
	}
	if cfg.B <= 0 || cfg.B > 1 {
		cfg.B = 0.75
	}
	return &Okapi{
		k1:       cfg.K1,
		b:        cfg.B,
		docFreq:  make(map[string]int),
	}
}

// Build ingests tokenised documents and builds the term-frequency and
// document-frequency tables needed for scoring. It MUST be called before
// Scores or TopN on a fresh instance.
func (o *Okapi) Build(docs [][]string) {
	o.docCount = len(docs)
	o.docLengths = make([]int, o.docCount)
	o.docTerms = make([]map[string]int, o.docCount)
	o.docFreq = make(map[string]int)

	totalLen := 0
	for i, tokens := range docs {
		o.docLengths[i] = len(tokens)
		totalLen += len(tokens)

		freqs := make(map[string]int, len(tokens))
		for _, t := range tokens {
			freqs[t]++
		}
		o.docTerms[i] = freqs

		for t := range freqs {
			o.docFreq[t]++
		}
	}

	if o.docCount > 0 {
		o.avgDocLen = float64(totalLen) / float64(o.docCount)
	}
}

// AddDocument appends a tokenised document to the index and updates
// document frequencies and average document length accordingly. The
// document is assigned the next available docID. Returns its index.
func (o *Okapi) AddDocument(tokens []string) int {
	id := o.docCount
	o.docCount++

	// Build term frequency map for this document.
	freqs := make(map[string]int, len(tokens))
	for _, t := range tokens {
		freqs[t]++
	}

	// Update per-document data.
	o.docLengths = append(o.docLengths, len(tokens))
	o.docTerms = append(o.docTerms, freqs)

	// Update global document frequencies.
	for t := range freqs {
		o.docFreq[t]++
	}

	// Recalculate average document length.
	totalLen := o.avgDocLen * float64(o.docCount-1) + float64(len(tokens))
	o.avgDocLen = totalLen / float64(o.docCount)

	return id
}

// RemoveDocument removes the document at the given index from the index and
// updates all derived statistics. After removal, documents at indices > id
// shift down by one, but this operation is O(n) for internal data
// rearrangement. Use RemoveLastDocument for removing the most recently added
// document (O(1)).
func (o *Okapi) RemoveDocument(id int) {
	if id < 0 || id >= o.docCount {
		return
	}

	docLen := o.docLengths[id]
	for t := range o.docTerms[id] {
		o.docFreq[t]--
		if o.docFreq[t] <= 0 {
			delete(o.docFreq, t)
		}
	}

	// Remove by shifting everything down (O(n)).
	copy(o.docLengths[id:], o.docLengths[id+1:])
	o.docLengths = o.docLengths[:o.docCount-1]

	copy(o.docTerms[id:], o.docTerms[id+1:])
	o.docTerms = o.docTerms[:o.docCount-1]

	o.docCount--

	// Recalculate average document length.
	totalLen := o.avgDocLen * float64(o.docCount+1) - float64(docLen)
	if o.docCount > 0 {
		o.avgDocLen = totalLen / float64(o.docCount)
	} else {
		o.avgDocLen = 0
	}
}

// RemoveLastDocument removes the most recently added document in O(1) time.
// This is the preferred removal path when documents are added in batch and
// removed in LIFO order (e.g., during a full rebuild).
func (o *Okapi) RemoveLastDocument() {
	if o.docCount == 0 {
		return
	}
	id := o.docCount - 1
	for t := range o.docTerms[id] {
		o.docFreq[t]--
		if o.docFreq[t] <= 0 {
			delete(o.docFreq, t)
		}
	}

	o.docLengths = o.docLengths[:id]
	o.docTerms = o.docTerms[:id]
	o.docCount--

	// Recalculate average document length.
	// Since we don't have the old total readily available from a removal
	// path that doesn't track it, recompute from scratch. For batch
	// rebuilds this amortises fine.
	o.recalcAvgDocLen()
}

// UpdateDocument replaces the document at the given index with new tokens.
// This is equivalent to RemoveDocument followed by inserting at the same
// position, but avoids the O(n) shift when removing from the middle.
func (o *Okapi) UpdateDocument(id int, oldTokens, newTokens []string) {
	if id < 0 || id >= o.docCount {
		return
	}

	// Remove old term frequencies from global docFreq.
	for _, t := range oldTokens {
		// We need the actual unique terms, not the raw token list.
		// Use the stored docTerms for this document.
		_ = t
	}
	// Use the actual stored map for accuracy.
	for t := range o.docTerms[id] {
		o.docFreq[t]--
		if o.docFreq[t] <= 0 {
			delete(o.docFreq, t)
		}
	}

	// Add new term frequencies.
	newFreqs := make(map[string]int, len(newTokens))
	for _, t := range newTokens {
		newFreqs[t]++
	}
	for t := range newFreqs {
		o.docFreq[t]++
	}

	// Update document data.
	oldLen := o.docLengths[id]
	o.docLengths[id] = len(newTokens)
	o.docTerms[id] = newFreqs

	// Update average document length.
	totalLen := o.avgDocLen * float64(o.docCount)
	totalLen = totalLen - float64(oldLen) + float64(len(newTokens))
	o.avgDocLen = totalLen / float64(o.docCount)
}

// Scores computes a BM25 relevance score for every document in the corpus
// against the given query tokens.
func (o *Okapi) Scores(query []string) []float64 {
	scores := make([]float64, o.docCount)
	if o.docCount == 0 || len(query) == 0 {
		return scores
	}

	for _, q := range query {
		idf := o.idf(q)
		if idf <= 0 {
			continue
		}

		for i := 0; i < o.docCount; i++ {
			tf := float64(o.docTerms[i][q])
			if tf == 0 {
				continue
			}

			docLen := float64(o.docLengths[i])
			k := o.k1 * (1 - o.b + o.b*docLen/o.avgDocLen)
			scores[i] += idf * (tf * (o.k1 + 1)) / (tf + k)
		}
	}

	return scores
}

// TopN returns the indices and scores of the top N scoring documents for the
// given query, ordered by descending score.
func (o *Okapi) TopN(query []string, n int) (indices []int, scores []float64) {
	allScores := o.Scores(query)
	if len(allScores) == 0 {
		return nil, nil
	}
	if n <= 0 || n > len(allScores) {
		n = len(allScores)
	}

	type pair struct {
		idx   int
		score float64
	}
	pairs := make([]pair, len(allScores))
	for i, s := range allScores {
		pairs[i] = pair{idx: i, score: s}
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].score > pairs[j].score
	})

	if n > len(pairs) {
		n = len(pairs)
	}
	indices = make([]int, n)
	scores = make([]float64, n)
	for i := 0; i < n; i++ {
		indices[i] = pairs[i].idx
		scores[i] = pairs[i].score
	}
	return indices, scores
}

// idf returns the inverse document frequency for a term using the standard
// BM25 formulation. Negative IDF is clamped to zero (terms in more than half
// of documents are treated as noise).
func (o *Okapi) idf(term string) float64 {
	n := o.docFreq[term]
	if n == 0 {
		return 0
	}
	N := float64(o.docCount)
	nf := float64(n)
	idf := math.Log((N - nf + 0.5) / (nf + 0.5))
	if idf < 0 {
		return 0
	}
	return idf
}

// recalcAvgDocLen recomputes the average document length from scratch.
func (o *Okapi) recalcAvgDocLen() {
	totalLen := 0
	for _, l := range o.docLengths {
		totalLen += l
	}
	if o.docCount > 0 {
		o.avgDocLen = float64(totalLen) / float64(o.docCount)
	} else {
		o.avgDocLen = 0
	}
}

// --- Export/import for serialisation ---

// DocLengths returns the per-document token lengths.
func (o *Okapi) DocLengths() []int { return o.docLengths }

// DocCount returns the number of indexed documents.
func (o *Okapi) DocCount() int { return o.docCount }

// AvgDocLen returns the average document length.
func (o *Okapi) AvgDocLen() float64 { return o.avgDocLen }

// DocFreq returns the term→document-frequency map.
func (o *Okapi) DocFreq() map[string]int { return o.docFreq }

// DocTerms returns the per-document term→frequency maps.
func (o *Okapi) DocTerms() []map[string]int { return o.docTerms }

// SetDocData restores internal state from serialised data. This is the
// inverse of DocLengths / DocFreq / DocTerms for deserialisation.
func (o *Okapi) SetDocData(docLengths []int, docFreq map[string]int, docTerms []map[string]int) {
	o.docCount = len(docLengths)
	o.docLengths = docLengths
	o.docFreq = docFreq
	o.docTerms = docTerms
	o.recalcAvgDocLen()
}
