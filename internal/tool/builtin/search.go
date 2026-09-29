package builtin

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
)

// maxToolResults caps how many matches glob/grep return in one call, and
// how many entries ls lists. A variable so tests can lower it.
var maxToolResults = 200

// maxCounted is how many matches glob/grep count before they stop
// searching: past it, an exact total isn't worth reading the whole tree.
func maxCounted() int { return 5 * maxToolResults }

// skipDirs are directories a walk never enters: version control, and
// dependency or environment trees whose matches are rarely the project's.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	".direnv":      true,
}

// walkFiles calls fn for every file under base (or base itself if it is a
// file) with its slash-separated path as the model should use it (relative
// to the working directory, or absolute when base is) and relative to base,
// for pattern matching, until fn returns false. Unreadable entries below
// base are skipped rather than failing the whole walk.
func walkFiles(ctx context.Context, base string, fn func(path, rel string) bool) error {
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			if p == base {
				return err
			}
			return nil
		}
		if d.IsDir() {
			// A walk from elsewhere, e.g. the home directory, stays out of
			// credential directories; searching one directly asks first.
			if p != base && (skipDirs[d.Name()] || sensitivePath(p)) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return nil
		}
		if rel == "." {
			rel = filepath.Base(p)
		}
		if !fn(filepath.ToSlash(p), filepath.ToSlash(rel)) {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walking %s: %w", base, err)
	}
	return nil
}

// matchSegments matches a "/"-split glob against a "/"-split path, where a
// "**" segment matches zero or more path segments.
func matchSegments(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		if matchSegments(pat[1:], segs) {
			return true
		}
		return len(segs) > 0 && matchSegments(pat, segs[1:])
	}
	if len(segs) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], segs[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pat[1:], segs[1:])
}

// noMatches is what glob and grep return instead of empty output, which
// models tend to answer by retrying variations.
func noMatches(pattern, base string) string {
	return fmt.Sprintf("(no matches for %q in %s)", pattern, base)
}

// resultsNote tells how complete a list of shown results is: all of
// total, cut from total, or cut after the search stopped counting.
func resultsNote(total, shown int) string {
	if total >= maxCounted() {
		return fmt.Sprintf("\n... (more than %d matches, search stopped; showing the first %d: narrow the pattern or path)", total, shown)
	}
	return truncationNote(total, shown)
}

// truncationNote says how many of total entries were left out, if any.
func truncationNote(total, shown int) string {
	if total <= shown {
		return ""
	}
	return fmt.Sprintf("\n... (%d more, truncated at %d)", total-shown, shown)
}
