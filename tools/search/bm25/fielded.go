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
	terms := expandQuery(s.tokenizer.Tokenize(query))
	if len(terms) == 0 {
		return 0, 0, 0
	}
	fd := s.docs[docID]
	score, matched := 0.0, 0
	seen := make(map[string]bool)
	for _, q := range terms {
		if seen[q] {
			continue
		}
		seen[q] = true
		termScore := 0.0
		for f := 0; f < 5; f++ {
			tf := float64(fd.terms[f][q])
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (float64(s.count)-float64(s.df[q])+0.5)/(float64(s.df[q])+0.5))
			avg := s.avg[f]
			if avg < 1 {
				avg = 1
			}
			k := 1.5 * (0.25 + 0.75*float64(fd.lengths[f])/avg)
			termScore += s.fieldWeight(f) * idf * (tf * 2.5) / (tf + k)
		}
		if termScore > 0 {
			matched++
			score += termScore
		}
	}
	// Exact identifier matches are highly reliable and should beat body noise.
	identifier := strings.ToLower(strings.TrimSpace(query))
	if identifier != "" && (strings.EqualFold(identifier, d.Symbol) || strings.EqualFold(identifier, filepath.Base(d.Path))) {
		score *= 1.8
	}
	coverage := float64(matched) / float64(len(terms))
	confidence := coverage
	if score > 0 {
		confidence = math.Min(1, coverage*0.7+(1-math.Exp(-score/8))*0.3)
	}
	return score, coverage, confidence
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
