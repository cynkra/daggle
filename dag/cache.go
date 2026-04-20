package dag

import (
	"os"
	"sync"
	"time"
)

// dagParseCache memoizes DAG parses by file path, invalidated by the
// file's mtime or size changing. Safe for concurrent use.
//
// API handlers walk the DAG tree and parse every file on every request;
// for 50 DAGs the repeat-request cost drops from 50 YAML parses to 50
// stats.
var dagParseCache = struct {
	mu      sync.Mutex
	entries map[string]parsedDAG
}{entries: make(map[string]parsedDAG)}

type parsedDAG struct {
	dag   *DAG
	mtime time.Time
	size  int64
}

// ParseFileCached is a cached wrapper around ParseFile. It stats the file
// to detect changes; on cache miss or mtime/size change, it re-parses and
// updates the cache. Callers must not mutate the returned *DAG.
func ParseFileCached(path string) (*DAG, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	dagParseCache.mu.Lock()
	cached, ok := dagParseCache.entries[path]
	dagParseCache.mu.Unlock()
	if ok && cached.mtime.Equal(info.ModTime()) && cached.size == info.Size() {
		return cached.dag, nil
	}

	d, err := ParseFile(path)
	if err != nil {
		return nil, err
	}

	// Re-stat so we only cache a parse whose mtime/size matches the data we
	// read. If the file was rewritten during the parse, skip caching and let
	// the next call re-parse.
	post, postErr := os.Stat(path)
	if postErr == nil && post.ModTime().Equal(info.ModTime()) && post.Size() == info.Size() {
		dagParseCache.mu.Lock()
		dagParseCache.entries[path] = parsedDAG{dag: d, mtime: post.ModTime(), size: post.Size()}
		dagParseCache.mu.Unlock()
	}
	return d, nil
}
