package bm25

import (
	"math"
	"path/filepath"
	"strings"
)

// FieldedWeights controls the relative importance of code-aware document fields.
type FieldedWeights struct {
	Path, Symbol, Signature, Imports, Body float64
}

func DefaultFieldedWeights() FieldedWeights {
	return FieldedWeights{Path: 3, Symbol: 6, Signature: 4, Imports: 2, Body: 1}
}

type fieldDocument struct {
	fields  [5][]string
	terms   [5]map[string]int
	lengths [5]int
}

// FieldedScorer implements BM25F over path, symbol, signature, imports and body.
// It intentionally keeps field statistics in memory; the source index remains
// backwards-compatible and can reconstruct these statistics from DocumentMeta.
type FieldedScorer struct {
	tokenizer *CodeTokenizer
	weights   FieldedWeights
	docs      []fieldDocument
	df        map[string]int
	count     int
	avg       [5]float64
}

func NewFieldedScorer(docs []DocumentMeta, weights FieldedWeights) *FieldedScorer {
	if weights.Path <= 0 {
		weights = DefaultFieldedWeights()
	}
	s := &FieldedScorer{tokenizer: NewCodeTokenizer(), weights: weights, count: len(docs), df: make(map[string]int), docs: make([]fieldDocument, len(docs))}
	for i, d := range docs {
		imports := extractImports(d.Content)
		texts := [5]string{filepath.ToSlash(d.Path), d.Symbol, d.Signature, imports, d.Content}
		seen := make(map[string]bool)
		for f, text := range texts {
			toks := s.tokenizer.Tokenize(text)
			s.docs[i].fields[f] = toks
			s.docs[i].lengths[f] = len(toks)
			s.docs[i].terms[f] = tokensToFreqs(toks)
			s.avg[f] += float64(len(toks))
			for _, tok := range toks {
				seen[tok] = true
			}
		}
		for tok := range seen {
			s.df[tok]++
		}
	}
	if s.count > 0 {
		for f := range s.avg {
			s.avg[f] /= float64(s.count)
		}
	}
	return s
}

// Score returns score, query-term coverage, and a confidence estimate.
func (s *FieldedScorer) Score(query string, d DocumentMeta, docID int) (float64, float64, float64) {
	if docID < 0 || docID >= len(s.docs) {
		return 0, 0, 0
	}
	expanded := expandQuery(s.tokenizer.Tokenize(query))
	if len(expanded) == 0 {
		return 0, 0, 0
	}
	fd := s.docs[docID]
	score, matched := 0.0, 0
	for _, q := range dedupeTokens(expanded) {
		contribution := s.termContribution(q, fd)
		if contribution > 0 {
			matched++
			score += contribution
		}
	}
	score *= identifierBoost(query, d)
	coverage := float64(matched) / float64(len(expanded))
	return score, coverage, confidenceFor(score, coverage)
}

// dedupeTokens drops repeated query terms while preserving first-seen order.
func dedupeTokens(terms []string) []string {
	seen := make(map[string]bool, len(terms))
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// termContribution sums the BM25F contribution of one distinct query term
// across all five fields. The mean field length is clamped at 1 so sparse or
// empty fields cannot blow up the length normalization.
func (s *FieldedScorer) termContribution(q string, fd fieldDocument) float64 {
	idf := math.Log(1 + (float64(s.count)-float64(s.df[q])+0.5)/(float64(s.df[q])+0.5))
	score := 0.0
	for f := range fd.fields {
		tf := float64(fd.terms[f][q])
		if tf == 0 {
			continue
		}
		k := 1.5 * (0.25 + 0.75*float64(fd.lengths[f])/clampedAvg(s.avg[f]))
		score += s.fieldWeight(f) * idf * (tf * 2.5) / (tf + k)
	}
	return score
}

// clampedAvg guards the length normalization: averages below one line would
// otherwise over-weight short fields (avg<1 clamp).
func clampedAvg(avg float64) float64 {
	if avg < 1 {
		return 1
	}
	return avg
}

// identifierBoost returns the score multiplier for exact identifier matches:
// the trimmed query equal-folding the document symbol or base file name earns
// 1.8x so it beats body noise; anything else leaves the score untouched.
func identifierBoost(query string, d DocumentMeta) float64 {
	identifier := strings.ToLower(strings.TrimSpace(query))
	if identifier != "" && (strings.EqualFold(identifier, d.Symbol) || strings.EqualFold(identifier, filepath.Base(d.Path))) {
		return 1.8
	}
	return 1
}

// confidenceFor blends term coverage with score saturation: confidence climbs
// toward 1 with score and saturates there; without score it stays raw coverage.
func confidenceFor(score, coverage float64) float64 {
	if score <= 0 {
		return coverage
	}
	return math.Min(1, coverage*0.7+(1-math.Exp(-score/8))*0.3)
}
func (s *FieldedScorer) fieldWeight(f int) float64 {
	return [...]float64{s.weights.Path, s.weights.Symbol, s.weights.Signature, s.weights.Imports, s.weights.Body}[f]
}

func extractImports(content string) string {
	var out strings.Builder
	for _, line := range strings.Split(content, "\n") {
		trim := strings.TrimSpace(line)
		for _, prefix := range []string{"import ", "from ", "using ", "include ", "require ", "use "} {
			if strings.HasPrefix(trim, prefix) {
				out.WriteString(trim)
				out.WriteByte(' ')
				break
			}
		}
	}
	return out.String()
}

// expandQuery adds a small, bounded synonym set useful for natural-language and
// multilingual requests without diluting exact query terms.
func expandQuery(tokens []string) []string {
	out := append([]string(nil), tokens...)
	seen := make(map[string]bool, len(out))
	for _, t := range out {
		seen[t] = true
	}
	aliases := map[string][]string{"auth": {"authentication", "login", "signin"}, "authentication": {"auth", "login"}, "login": {"auth", "signin"}, "cache": {"缓存", "caching"}, "error": {"exception", "failure", "errore", "erreur"}, "search": {"lookup", "query", "recherche"}, "usuario": {"user"}, "utilisateur": {"user"}}
	for _, t := range tokens {
		for _, a := range aliases[t] {
			if !seen[a] {
				out = append(out, a)
				seen[a] = true
			}
		}
	}
	if len(out) > len(tokens)+6 {
		out = out[:len(tokens)+6]
	}
	return out
}
