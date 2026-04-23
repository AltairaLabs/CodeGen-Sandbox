package search

import (
	"math"
	"path/filepath"
	"sort"
	"sync"
)

// BM25 textbook defaults; proven across literature and quite robust — we
// don't need to tune these for the first pass.
const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

// Result is one hit from Index.Search.
type Result struct {
	Unit  Unit
	Score float64
}

// Index is a thread-safe in-memory BM25 index keyed by Unit identity. Build
// walks a workspace; AddFile / RemoveFile mutate post-hoc as files change.
type Index struct {
	root string

	mu sync.RWMutex

	// units is indexed by unit ID; IDs are never reused so gaps can exist
	// after RemoveFile — they are skipped at query time.
	units map[int]Unit
	// tokens[id] is the tokenised field vector for unit id (identifier
	// tokens + docstring tokens concatenated; BM25 treats them as one bag).
	tokens map[int][]string
	// lengths[id] is |tokens[id]|, cached for per-query BM25 length norm.
	lengths map[int]int
	// df[token] is the document-frequency count — how many units contain
	// token at least once.
	df map[string]int
	// fileUnits indexes which unit IDs belong to each workspace-relative file;
	// used by RemoveFile / AddFile to undo prior insertions.
	fileUnits map[string][]int

	nextID    int
	totalLen  int
	totalDocs int

	// watcherStarted guards Watch so repeat calls are no-ops.
	watcherStarted bool
}

// newIndex returns a zero-valued Index rooted at root.
func newIndex(root string) *Index {
	return &Index{
		root:      root,
		units:     make(map[int]Unit),
		tokens:    make(map[int][]string),
		lengths:   make(map[int]int),
		df:        make(map[string]int),
		fileUnits: make(map[string][]int),
	}
}

// Build walks root, extracts every Go file's units, and returns a populated
// Index. .git/ and node_modules/ directories are skipped.
func Build(root string) (*Index, error) {
	idx := newIndex(root)
	err := WalkGoFiles(root, func(path string) error {
		units, err := ExtractGoFile(path, root)
		if err != nil {
			// Malformed source is common mid-edit; skip silently rather
			// than fail the whole index build.
			return nil
		}
		idx.addUnitsLocked(toRelFile(root, path), units)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return idx, nil
}

// FileCount returns the number of files currently contributing units.
func (i *Index) FileCount() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return len(i.fileUnits)
}

// UnitCount returns the number of indexed units.
func (i *Index) UnitCount() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return len(i.units)
}

// AddFile re-extracts path and replaces any existing units for it. A
// non-.go path is a no-op. The input path may be absolute or relative to
// root.
func (i *Index) AddFile(path string) error {
	abs := i.absPath(path)
	if filepath.Ext(abs) != ".go" {
		return nil
	}
	units, err := ExtractGoFile(abs, i.root)
	if err != nil {
		// Parse failure: behave as if the file isn't indexable.
		i.RemoveFile(abs)
		return nil
	}
	rel := toRelFile(i.root, abs)

	i.mu.Lock()
	defer i.mu.Unlock()
	i.removeFileLocked(rel)
	i.addUnitsLocked(rel, units)
	return nil
}

// RemoveFile drops every unit associated with path.
func (i *Index) RemoveFile(path string) {
	rel := toRelFile(i.root, i.absPath(path))
	i.mu.Lock()
	defer i.mu.Unlock()
	i.removeFileLocked(rel)
}

// absPath resolves input to an absolute path using the index's root.
func (i *Index) absPath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(i.root, p)
}

// addUnitsLocked inserts every unit under the given rel key. Caller may
// hold i.mu (Build uses addUnitsLocked with no lock by design, since Build
// owns the only reference to a fresh index — this is safe because Index
// isn't exposed until Build returns).
func (i *Index) addUnitsLocked(rel string, units []Unit) {
	for _, u := range units {
		u.File = rel
		id := i.nextID
		i.nextID++
		toks := unitTokens(u)
		i.units[id] = u
		i.tokens[id] = toks
		i.lengths[id] = len(toks)
		i.totalLen += len(toks)
		i.totalDocs++
		for _, t := range unique(toks) {
			i.df[t]++
		}
		i.fileUnits[rel] = append(i.fileUnits[rel], id)
	}
}

func (i *Index) removeFileLocked(rel string) {
	ids, ok := i.fileUnits[rel]
	if !ok {
		return
	}
	for _, id := range ids {
		for _, t := range unique(i.tokens[id]) {
			i.df[t]--
			if i.df[t] <= 0 {
				delete(i.df, t)
			}
		}
		i.totalLen -= i.lengths[id]
		i.totalDocs--
		delete(i.units, id)
		delete(i.tokens, id)
		delete(i.lengths, id)
	}
	delete(i.fileUnits, rel)
}

// Search returns up to limit results for query, ranked by BM25.
// An empty or whitespace-only query yields an empty slice.
func (i *Index) Search(query string, limit int) []Result {
	qtoks := Tokenize(query)
	if len(qtoks) == 0 {
		return nil
	}

	i.mu.RLock()
	defer i.mu.RUnlock()

	if i.totalDocs == 0 {
		return nil
	}
	avgLen := float64(i.totalLen) / float64(i.totalDocs)

	scores := make(map[int]float64, 64)
	for _, qt := range qtoks {
		df := i.df[qt]
		if df == 0 {
			continue
		}
		idf := math.Log(1 + (float64(i.totalDocs)-float64(df)+0.5)/(float64(df)+0.5))
		for id, toks := range i.tokens {
			tf := countToken(toks, qt)
			if tf == 0 {
				continue
			}
			dl := float64(i.lengths[id])
			norm := 1 - bm25B + bm25B*(dl/avgLen)
			denom := float64(tf) + bm25K1*norm
			scores[id] += idf * (float64(tf) * (bm25K1 + 1)) / denom
		}
	}

	results := make([]Result, 0, len(scores))
	for id, s := range scores {
		results = append(results, Result{Unit: i.units[id], Score: s})
	}
	sort.Slice(results, func(a, b int) bool {
		if results[a].Score != results[b].Score {
			return results[a].Score > results[b].Score
		}
		// Deterministic tiebreak on (file, symbol) — stable across runs.
		if results[a].Unit.File != results[b].Unit.File {
			return results[a].Unit.File < results[b].Unit.File
		}
		return results[a].Unit.Symbol < results[b].Unit.Symbol
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results
}

// unitTokens builds the token bag for a Unit: symbol name + signature
// identifiers + docstring tokens. Kind and file path are excluded — they
// aren't predictive enough to be worth the noise.
func unitTokens(u Unit) []string {
	toks := Tokenize(u.Symbol)
	toks = append(toks, Tokenize(u.Signature)...)
	toks = append(toks, Tokenize(u.Doc)...)
	return toks
}

func countToken(toks []string, t string) int {
	n := 0
	for _, x := range toks {
		if x == t {
			n++
		}
	}
	return n
}

func unique(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(s))
	out := make([]string, 0, len(s))
	for _, v := range s {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func toRelFile(root, abs string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}
