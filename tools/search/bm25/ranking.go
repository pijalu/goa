package bm25

import "sort"

type rankedChunk struct {
	id                          int
	score, coverage, confidence float64
}

func (idx *Index) rankChunks(query string, limit int, minScore float64) []rankedChunk {
	ids, _ := idx.chunkOKAPI.TopN(expandQuery(idx.tokenizer.Tokenize(query)), limit)
	ranked := make([]rankedChunk, 0, len(ids))
	for _, id := range ids {
		if id < 0 || id >= len(idx.Data.Documents) {
			continue
		}
		d := idx.Data.Documents[id]
		score, coverage, confidence := idx.fielded.Score(query, d, id)
		if score >= minScore {
			ranked = append(ranked, rankedChunk{id, score, coverage, confidence})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	return ranked
}

func (idx *Index) resultChunks(ranked []rankedChunk, maxResults int) []SearchResult {
	out := make([]SearchResult, 0, maxResults)
	pathCount := make(map[string]int)
	for _, r := range ranked {
		d := idx.Data.Documents[r.id]
		if pathCount[d.Path] >= 2 {
			continue
		}
		pathCount[d.Path]++
		out = append(out, SearchResult{Path: d.Path, Score: r.score, Coverage: r.coverage, Confidence: r.confidence, Lines: d.EndLine - d.StartLine + 1, ID: d.ID, Language: d.Language, Kind: d.Kind, Symbol: d.Symbol, StartLine: d.StartLine, EndLine: d.EndLine, Content: d.Content})
		if len(out) >= maxResults {
			break
		}
	}
	return out
}
