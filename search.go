package garland

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ropeRuneReader implements io.RuneReader for streaming regex operations.
// It traverses the rope structure without loading the entire document into memory.
type ropeRuneReader struct {
	g         *Garland
	bytePos   int64  // Current byte position in document
	totalSize int64  // Total document size
	leafData  []byte // Current leaf's data (cached)
	leafStart int64  // Byte offset where current leaf starts
	leafPos   int    // Position within leafData
}

// newRopeRuneReader creates a RuneReader starting at the given byte position.
func (g *Garland) newRopeRuneReader(startPos int64) *ropeRuneReader {
	return &ropeRuneReader{
		g:         g,
		bytePos:   startPos,
		totalSize: g.totalBytes,
		leafData:  nil,
		leafStart: -1,
		leafPos:   0,
	}
}

// ReadRune implements io.RuneReader.
func (r *ropeRuneReader) ReadRune() (rune, int, error) {
	if r.bytePos >= r.totalSize {
		return 0, 0, io.EOF
	}

	// If we need to load a new leaf
	if r.leafData == nil || r.bytePos < r.leafStart || r.bytePos >= r.leafStart+int64(len(r.leafData)) {
		if err := r.loadLeafAt(r.bytePos); err != nil {
			return 0, 0, err
		}
	}

	// Calculate position within current leaf
	r.leafPos = int(r.bytePos - r.leafStart)

	// Decode rune from current position
	ru, size := utf8.DecodeRune(r.leafData[r.leafPos:])
	if ru == utf8.RuneError && size <= 1 {
		// Invalid UTF-8, advance by 1 byte
		r.bytePos++
		return utf8.RuneError, 1, nil
	}

	r.bytePos += int64(size)
	return ru, size, nil
}

// loadLeafAt loads the leaf containing the given byte position.
func (r *ropeRuneReader) loadLeafAt(pos int64) error {
	leafResult, err := r.g.findLeafByByteUnlocked(pos)
	if err != nil {
		return err
	}

	// Get the leaf's snapshot and data
	snap := leafResult.Node.snapshotAt(r.g.currentFork, r.g.currentRevision)
	if snap == nil {
		return ErrInternal
	}

	// Thaw if needed (cold/warm storage -> memory), using the
	// snapshot's own history key - cold blocks are named by the key
	// the snapshot was chilled under.
	if err := r.g.ensureLeafDataResident(leafResult.Node, snap); err != nil {
		return err
	}

	r.leafData = snap.data
	r.leafStart = leafResult.LeafByteStart
	return nil
}

// SearchResult contains information about a search match.
type SearchResult struct {
	ByteStart int64  // Start position in bytes
	ByteEnd   int64  // End position in bytes (exclusive)
	Match     string // The matched text
}

// SearchOptions configures string search behavior.
type SearchOptions struct {
	CaseSensitive bool // If false, search is case-insensitive
	WholeWord     bool // If true, only match whole words
	Backward      bool // If true, search backward from cursor
}

// RegexOptions configures regex search behavior.
type RegexOptions struct {
	CaseInsensitive bool // If true, regex is case-insensitive
	Backward        bool // If true, search backward from cursor
}

// FindString searches for a string starting from the cursor position.
// Returns the first match found, or nil if no match.
// The cursor is NOT moved by this operation.
func (c *Cursor) FindString(needle string, opts SearchOptions) (*SearchResult, error) {
	if c.garland == nil {
		return nil, ErrCursorNotFound
	}
	if len(needle) == 0 {
		return nil, nil
	}

	c.garland.mu.Lock()
	defer c.garland.mu.Unlock()

	return c.garland.findStringInternal(c.bytePos, needle, opts)
}

// FindStringAll finds all occurrences of a string in the document.
// Returns all matches in document order (or reverse order if Backward).
func (c *Cursor) FindStringAll(needle string, opts SearchOptions) ([]SearchResult, error) {
	if c.garland == nil {
		return nil, ErrCursorNotFound
	}
	if len(needle) == 0 {
		return nil, nil
	}

	c.garland.mu.Lock()
	defer c.garland.mu.Unlock()

	return c.garland.findStringAllInternal(needle, opts)
}

// ReplaceString replaces the first occurrence of needle with replacement.
// Search starts from cursor position.
// Returns the change result and whether a replacement was made.
func (c *Cursor) ReplaceString(needle, replacement string, opts SearchOptions) (bool, ChangeResult, error) {
	if c.garland == nil {
		return false, ChangeResult{}, ErrCursorNotFound
	}
	if len(needle) == 0 {
		return false, ChangeResult{Fork: c.garland.currentFork, Revision: c.garland.currentRevision}, nil
	}

	// Find first match
	c.garland.mu.Lock()
	match, err := c.garland.findStringInternal(c.bytePos, needle, opts)
	c.garland.mu.Unlock()

	if err != nil {
		return false, ChangeResult{}, err
	}
	if match == nil {
		return false, ChangeResult{Fork: c.garland.currentFork, Revision: c.garland.currentRevision}, nil
	}

	// Replace using overwrite
	_, result, err := c.garland.overwriteBytesAtInternal(c, match.ByteStart, match.ByteEnd-match.ByteStart, []byte(replacement), nil, false)
	if err != nil {
		return false, ChangeResult{}, err
	}

	return true, result, nil
}

// ReplaceStringAll replaces all occurrences of needle with replacement.
// Returns the number of replacements made.
func (c *Cursor) ReplaceStringAll(needle, replacement string, opts SearchOptions) (int, ChangeResult, error) {
	if c.garland == nil {
		return 0, ChangeResult{}, ErrCursorNotFound
	}
	if len(needle) == 0 {
		return 0, ChangeResult{Fork: c.garland.currentFork, Revision: c.garland.currentRevision}, nil
	}

	return c.replaceStringCount(needle, replacement, -1, opts)
}

// ReplaceStringCount replaces up to count occurrences of needle with replacement.
// If count is -1, replaces all occurrences.
// Returns the number of replacements made.
func (c *Cursor) ReplaceStringCount(needle, replacement string, count int, opts SearchOptions) (int, ChangeResult, error) {
	if c.garland == nil {
		return 0, ChangeResult{}, ErrCursorNotFound
	}
	if len(needle) == 0 || count == 0 {
		return 0, ChangeResult{Fork: c.garland.currentFork, Revision: c.garland.currentRevision}, nil
	}

	return c.replaceStringCount(needle, replacement, count, opts)
}

// replaceStringCount is the internal implementation for counted replacements.
func (c *Cursor) replaceStringCount(needle, replacement string, count int, opts SearchOptions) (int, ChangeResult, error) {
	// Find all matches first (to avoid issues with changing positions).
	// Done BEFORE opening a transaction: a replace with no matches must
	// be a true no-op, not an empty commit that burns a revision and
	// returns zero-valued coordinates.
	c.garland.mu.Lock()
	matches, err := c.garland.findStringAllInternal(needle, opts)
	c.garland.mu.Unlock()
	if err != nil {
		return 0, ChangeResult{}, err
	}

	// Limit to count matches. For Backward the list arrives in reverse
	// document order, so the first N entries are the LAST N matches.
	if count >= 0 && count < len(matches) {
		matches = matches[:count]
	}
	if len(matches) == 0 {
		return 0, ChangeResult{Fork: c.garland.currentFork, Revision: c.garland.currentRevision}, nil
	}

	// Apply strictly bottom-up so earlier positions stay valid - by
	// DESCENDING position, independent of the search direction the
	// match list came in.
	sortSearchResultsDescending(matches)

	if err := c.garland.TransactionStart("replace"); err != nil {
		return 0, ChangeResult{}, err
	}
	replacements := 0
	for _, match := range matches {
		_, _, err := c.garland.overwriteBytesAtInternal(c, match.ByteStart, match.ByteEnd-match.ByteStart, []byte(replacement), nil, false)
		if err != nil {
			c.garland.TransactionRollback()
			return replacements, ChangeResult{}, err
		}
		replacements++
	}

	result, err := c.garland.TransactionCommit()
	if err != nil {
		return replacements, ChangeResult{}, err
	}
	return replacements, result, nil
}

// FindRegex searches for a regex pattern starting from the cursor position.
// Returns the first match found, or nil if no match.
// The cursor is NOT moved by this operation.
func (c *Cursor) FindRegex(pattern string, opts RegexOptions) (*SearchResult, error) {
	if c.garland == nil {
		return nil, ErrCursorNotFound
	}
	if len(pattern) == 0 {
		return nil, nil
	}

	// Compile regex
	re, err := compileRegex(pattern, opts.CaseInsensitive)
	if err != nil {
		return nil, err
	}

	c.garland.mu.Lock()
	defer c.garland.mu.Unlock()

	return c.garland.findRegexInternal(c.bytePos, re, opts)
}

// FindRegexAll finds all regex matches in the document.
func (c *Cursor) FindRegexAll(pattern string, opts RegexOptions) ([]SearchResult, error) {
	if c.garland == nil {
		return nil, ErrCursorNotFound
	}
	if len(pattern) == 0 {
		return nil, nil
	}

	re, err := compileRegex(pattern, opts.CaseInsensitive)
	if err != nil {
		return nil, err
	}

	c.garland.mu.Lock()
	defer c.garland.mu.Unlock()

	return c.garland.findRegexAllInternal(re, opts)
}

// MatchRegex checks if the regex matches at the current cursor position.
// Returns true if the pattern matches starting exactly at cursor position.
func (c *Cursor) MatchRegex(pattern string, caseInsensitive bool) (bool, *SearchResult, error) {
	if c.garland == nil {
		return false, nil, ErrCursorNotFound
	}
	if len(pattern) == 0 {
		return false, nil, nil
	}

	// Compile regex with ^ anchor to match at position
	anchoredPattern := "^(?:" + pattern + ")"
	re, err := compileRegex(anchoredPattern, caseInsensitive)
	if err != nil {
		return false, nil, err
	}

	c.garland.mu.Lock()
	defer c.garland.mu.Unlock()

	// Read from cursor to end (or reasonable chunk)
	data, err := c.garland.readBytesRangeInternal(c.bytePos, c.garland.totalBytes-c.bytePos)
	if err != nil {
		return false, nil, err
	}

	loc := re.FindIndex(data)
	if loc == nil || loc[0] != 0 {
		return false, nil, nil
	}

	return true, &SearchResult{
		ByteStart: c.bytePos,
		ByteEnd:   c.bytePos + int64(loc[1]),
		Match:     string(data[loc[0]:loc[1]]),
	}, nil
}

// ReplaceRegex replaces the first regex match with replacement.
// Replacement can include $1, $2, etc. for capture groups.
func (c *Cursor) ReplaceRegex(pattern, replacement string, opts RegexOptions) (bool, ChangeResult, error) {
	if c.garland == nil {
		return false, ChangeResult{}, ErrCursorNotFound
	}
	if len(pattern) == 0 {
		return false, ChangeResult{Fork: c.garland.currentFork, Revision: c.garland.currentRevision}, nil
	}

	re, err := compileRegex(pattern, opts.CaseInsensitive)
	if err != nil {
		return false, ChangeResult{}, err
	}

	// Find first match
	c.garland.mu.Lock()
	match, err := c.garland.findRegexInternal(c.bytePos, re, opts)
	c.garland.mu.Unlock()

	if err != nil {
		return false, ChangeResult{}, err
	}
	if match == nil {
		return false, ChangeResult{Fork: c.garland.currentFork, Revision: c.garland.currentRevision}, nil
	}

	// Expand replacement with capture groups
	expanded := re.ReplaceAllString(match.Match, replacement)

	// Replace using overwrite
	_, result, err := c.garland.overwriteBytesAtInternal(c, match.ByteStart, match.ByteEnd-match.ByteStart, []byte(expanded), nil, false)
	if err != nil {
		return false, ChangeResult{}, err
	}

	return true, result, nil
}

// ReplaceRegexAll replaces all regex matches with replacement.
func (c *Cursor) ReplaceRegexAll(pattern, replacement string, opts RegexOptions) (int, ChangeResult, error) {
	if c.garland == nil {
		return 0, ChangeResult{}, ErrCursorNotFound
	}
	if len(pattern) == 0 {
		return 0, ChangeResult{Fork: c.garland.currentFork, Revision: c.garland.currentRevision}, nil
	}

	return c.replaceRegexCount(pattern, replacement, -1, opts)
}

// ReplaceRegexCount replaces up to count regex matches with replacement.
func (c *Cursor) ReplaceRegexCount(pattern, replacement string, count int, opts RegexOptions) (int, ChangeResult, error) {
	if c.garland == nil {
		return 0, ChangeResult{}, ErrCursorNotFound
	}
	if len(pattern) == 0 || count == 0 {
		return 0, ChangeResult{Fork: c.garland.currentFork, Revision: c.garland.currentRevision}, nil
	}

	return c.replaceRegexCount(pattern, replacement, count, opts)
}

// replaceRegexCount is the internal implementation for counted regex replacements.
func (c *Cursor) replaceRegexCount(pattern, replacement string, count int, opts RegexOptions) (int, ChangeResult, error) {
	re, err := compileRegex(pattern, opts.CaseInsensitive)
	if err != nil {
		return 0, ChangeResult{}, err
	}

	// Find all matches BEFORE opening a transaction (see
	// replaceStringCount for why).
	c.garland.mu.Lock()
	matches, err := c.garland.findRegexAllInternal(re, opts)
	c.garland.mu.Unlock()
	if err != nil {
		return 0, ChangeResult{}, err
	}

	// Limit to count matches. For Backward the list arrives in reverse
	// document order, so the first N entries are the LAST N matches.
	if count >= 0 && count < len(matches) {
		matches = matches[:count]
	}
	if len(matches) == 0 {
		return 0, ChangeResult{Fork: c.garland.currentFork, Revision: c.garland.currentRevision}, nil
	}

	// Apply strictly bottom-up (descending positions), independent of
	// the direction the match list came in.
	sortSearchResultsDescending(matches)

	if err := c.garland.TransactionStart("regex-replace"); err != nil {
		return 0, ChangeResult{}, err
	}
	replacements := 0
	for _, match := range matches {
		// Expand replacement for this specific match
		expanded := re.ReplaceAllString(match.Match, replacement)

		_, _, err := c.garland.overwriteBytesAtInternal(c, match.ByteStart, match.ByteEnd-match.ByteStart, []byte(expanded), nil, false)
		if err != nil {
			c.garland.TransactionRollback()
			return replacements, ChangeResult{}, err
		}
		replacements++
	}

	result, err := c.garland.TransactionCommit()
	if err != nil {
		return replacements, ChangeResult{}, err
	}
	return replacements, result, nil
}

// Internal implementation methods

func (g *Garland) findStringInternal(startPos int64, needle string, opts SearchOptions) (*SearchResult, error) {
	if opts.Backward {
		return g.findStringBackwardInternal(startPos, needle, opts)
	}
	return g.findStringForwardInternal(startPos, needle, opts)
}

// SEARCH SPEC: matches are found scanning LEFT TO RIGHT and are
// NON-OVERLAPPING - after an accepted match the scan resumes at its
// end. A whole-word REJECTION advances one byte, so an overlapping
// later candidate can still be accepted. Backward search returns the
// same match set (scanned from position 0) in reverse order.

// stringMatchesFrom scans from startPos, returning up to limit
// non-overlapping matches (limit < 0 means all). Case-insensitive
// matching delegates to a case-folding regex: lowering the haystack
// bytes would shift offsets for runes whose lower form has a different
// encoded length (e.g. the Kelvin sign K folds to a 1-byte 'k').
func (g *Garland) stringMatchesFrom(startPos int64, needle string, opts SearchOptions, limit int) ([]SearchResult, error) {
	if !opts.CaseSensitive {
		re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(needle))
		if err != nil {
			return nil, err
		}
		return g.regexMatchesFrom(startPos, re, opts.WholeWord, limit)
	}

	needleBytes := []byte(needle)
	nlen := int64(len(needleBytes))
	const window = 1 << 20
	var out []SearchResult
	off := startPos
	if off < 0 {
		off = 0
	}
	for off+nlen <= g.totalBytes {
		end := off + window
		if end > g.totalBytes {
			end = g.totalBytes
		}
		data, err := g.readBytesRangeInternal(off, end-off)
		if err != nil {
			return nil, err
		}
		idx := int64(bytes.Index(data, needleBytes))
		if idx < 0 {
			if end == g.totalBytes {
				break
			}
			// Next window overlaps by needle length - 1 so a match
			// spanning the window edge is still seen in full.
			off = end - nlen + 1
			continue
		}
		st := off + idx
		if st+nlen > end {
			// Partial at window edge cannot happen (Index found the
			// full needle inside data), but keep the invariant clear.
			off = st
			continue
		}
		if opts.WholeWord && !g.isWholeWordChunked(st, nlen) {
			off = st + 1
			continue
		}
		out = append(out, SearchResult{
			ByteStart: st,
			ByteEnd:   st + nlen,
			Match:     string(data[idx : idx+nlen]),
		})
		if limit > 0 && len(out) >= limit {
			return out, nil
		}
		off = st + nlen
	}
	return out, nil
}

// regexMatchesFrom scans from startPos using the streaming rope reader,
// returning up to limit non-overlapping matches (limit < 0 means all).
// Each iteration finds the leftmost match at or after off, so the whole
// scan is a single forward pass over the document.
func (g *Garland) regexMatchesFrom(startPos int64, re *regexp.Regexp, whole bool, limit int) ([]SearchResult, error) {
	var out []SearchResult
	off := startPos
	if off < 0 {
		off = 0
	}
	for off <= g.totalBytes {
		reader := g.newRopeRuneReader(off)
		loc := re.FindReaderIndex(reader)
		if loc == nil {
			break
		}
		st, en := off+int64(loc[0]), off+int64(loc[1])
		if whole && !g.isWholeWordChunked(st, en-st) {
			off = st + 1
			continue
		}
		matchData, err := g.readBytesRangeInternal(st, en-st)
		if err != nil {
			return nil, err
		}
		out = append(out, SearchResult{ByteStart: st, ByteEnd: en, Match: string(matchData)})
		if limit > 0 && len(out) >= limit {
			return out, nil
		}
		if en > st {
			off = en
		} else {
			off = st + 1 // zero-width match: force progress
		}
	}
	return out, nil
}

// findStringForwardInternal returns the first match at or after startPos.
func (g *Garland) findStringForwardInternal(startPos int64, needle string, opts SearchOptions) (*SearchResult, error) {
	matches, err := g.stringMatchesFrom(startPos, needle, opts, 1)
	if err != nil || len(matches) == 0 {
		return nil, err
	}
	return &matches[0], nil
}

// findStringBackwardInternal returns the last match ending at or
// before startPos.
func (g *Garland) findStringBackwardInternal(startPos int64, needle string, opts SearchOptions) (*SearchResult, error) {
	matches, err := g.stringMatchesFrom(0, needle, opts, -1)
	if err != nil {
		return nil, err
	}
	var last *SearchResult
	for i := range matches {
		if matches[i].ByteEnd <= startPos {
			last = &matches[i]
		}
	}
	return last, nil
}

// isWholeWordChunked checks if the match at pos is a whole word. Reads
// up to utf8.UTFMax bytes on each side: reading a single byte would
// decode a multi-byte neighbor (e.g. 中) as RuneError, making every
// non-ASCII word character look like a word boundary.
func (g *Garland) isWholeWordChunked(pos, length int64) bool {
	// Check the rune ending at the match start
	if pos > 0 {
		start := pos - utf8.UTFMax
		if start < 0 {
			start = 0
		}
		before, err := g.readBytesRangeInternal(start, pos-start)
		if err == nil && len(before) > 0 {
			r, _ := utf8.DecodeLastRune(before)
			if isWordChar(r) {
				return false
			}
		}
	}

	// Check the rune starting at the match end
	if pos+length < g.totalBytes {
		n := int64(utf8.UTFMax)
		if pos+length+n > g.totalBytes {
			n = g.totalBytes - pos - length
		}
		after, err := g.readBytesRangeInternal(pos+length, n)
		if err == nil && len(after) > 0 {
			r, _ := utf8.DecodeRune(after)
			if isWordChar(r) {
				return false
			}
		}
	}

	return true
}

func (g *Garland) findStringAllInternal(needle string, opts SearchOptions) ([]SearchResult, error) {
	results, err := g.stringMatchesFrom(0, needle, opts, -1)
	if err != nil {
		return nil, err
	}
	if opts.Backward {
		for i, j := 0, len(results)-1; i < j; i, j = i+1, j-1 {
			results[i], results[j] = results[j], results[i]
		}
	}
	return results, nil
}

func (g *Garland) findRegexInternal(startPos int64, re *regexp.Regexp, opts RegexOptions) (*SearchResult, error) {
	if opts.Backward {
		return g.findRegexBackwardInternal(startPos, re)
	}
	matches, err := g.regexMatchesFrom(startPos, re, false, 1)
	if err != nil || len(matches) == 0 {
		return nil, err
	}
	return &matches[0], nil
}

// findRegexBackwardInternal returns the last match ending at or before
// startPos.
func (g *Garland) findRegexBackwardInternal(startPos int64, re *regexp.Regexp) (*SearchResult, error) {
	matches, err := g.regexMatchesFrom(0, re, false, -1)
	if err != nil {
		return nil, err
	}
	var last *SearchResult
	for i := range matches {
		if matches[i].ByteEnd <= startPos {
			last = &matches[i]
		}
	}
	return last, nil
}

func (g *Garland) findRegexAllInternal(re *regexp.Regexp, opts RegexOptions) ([]SearchResult, error) {
	results, err := g.regexMatchesFrom(0, re, false, -1)
	if err != nil {
		return nil, err
	}
	if opts.Backward {
		for i, j := 0, len(results)-1; i < j; i, j = i+1, j-1 {
			results[i], results[j] = results[j], results[i]
		}
	}
	return results, nil
}

// sortSearchResultsDescending sorts results by ByteStart, highest
// first, so replacements can be applied bottom-up.
func sortSearchResultsDescending(results []SearchResult) {
	// Simple insertion sort - results are mostly sorted already
	for i := 1; i < len(results); i++ {
		j := i
		for j > 0 && results[j-1].ByteStart < results[j].ByteStart {
			results[j-1], results[j] = results[j], results[j-1]
			j--
		}
	}
}

// compileRegex compiles a regex pattern with optional case insensitivity.
func compileRegex(pattern string, caseInsensitive bool) (*regexp.Regexp, error) {
	if caseInsensitive {
		pattern = "(?i)" + pattern
	}
	return regexp.Compile(pattern)
}

// isWholeWord checks if the match at pos is a whole word.
func isWholeWord(data []byte, pos, length int64) bool {
	// Check character before match
	if pos > 0 {
		r, _ := utf8.DecodeLastRune(data[:pos])
		if isWordChar(r) {
			return false
		}
	}

	// Check character after match
	if pos+length < int64(len(data)) {
		r, _ := utf8.DecodeRune(data[pos+length:])
		if isWordChar(r) {
			return false
		}
	}

	return true
}

// isWordChar returns true if r is a word character (letter, digit, or underscore).
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// CountString counts occurrences of needle in the document.
func (c *Cursor) CountString(needle string, opts SearchOptions) (int, error) {
	if c.garland == nil {
		return 0, ErrCursorNotFound
	}
	if len(needle) == 0 {
		return 0, nil
	}

	c.garland.mu.Lock()
	defer c.garland.mu.Unlock()

	matches, err := c.garland.findStringAllInternal(needle, opts)
	if err != nil {
		return 0, err
	}
	return len(matches), nil
}

// CountRegex counts regex matches in the document.
func (c *Cursor) CountRegex(pattern string, caseInsensitive bool) (int, error) {
	if c.garland == nil {
		return 0, ErrCursorNotFound
	}
	if len(pattern) == 0 {
		return 0, nil
	}

	re, err := compileRegex(pattern, caseInsensitive)
	if err != nil {
		return 0, err
	}

	c.garland.mu.Lock()
	defer c.garland.mu.Unlock()

	matches, err := c.garland.findRegexAllInternal(re, RegexOptions{})
	if err != nil {
		return 0, err
	}
	return len(matches), nil
}

// FindNext finds the next occurrence and moves cursor to it.
// Returns the match or nil if not found.
func (c *Cursor) FindNext(needle string, opts SearchOptions) (*SearchResult, error) {
	// Start search from position after cursor (to find "next")
	searchStart := c.bytePos
	if !opts.Backward {
		searchStart = c.bytePos + 1
	}

	if c.garland == nil {
		return nil, ErrCursorNotFound
	}

	c.garland.mu.Lock()
	match, err := c.garland.findStringInternal(searchStart, needle, opts)
	c.garland.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if match != nil {
		c.SeekByte(match.ByteStart)
	}
	return match, nil
}

// FindNextRegex finds the next regex match and moves cursor to it.
func (c *Cursor) FindNextRegex(pattern string, opts RegexOptions) (*SearchResult, error) {
	searchStart := c.bytePos
	if !opts.Backward {
		searchStart = c.bytePos + 1
	}

	if c.garland == nil {
		return nil, ErrCursorNotFound
	}

	re, err := compileRegex(pattern, opts.CaseInsensitive)
	if err != nil {
		return nil, err
	}

	c.garland.mu.Lock()
	match, err := c.garland.findRegexInternal(searchStart, re, opts)
	c.garland.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if match != nil {
		c.SeekByte(match.ByteStart)
	}
	return match, nil
}

// Helper for case-insensitive string contains check
func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
