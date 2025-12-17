package garland

import (
	"bytes"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

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

	c.garland.mu.RLock()
	defer c.garland.mu.RUnlock()

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

	c.garland.mu.RLock()
	defer c.garland.mu.RUnlock()

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
	c.garland.mu.RLock()
	match, err := c.garland.findStringInternal(c.bytePos, needle, opts)
	c.garland.mu.RUnlock()

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
	// Start a transaction for atomic replacement
	err := c.garland.TransactionStart("replace")
	if err != nil {
		return 0, ChangeResult{}, err
	}

	var lastResult ChangeResult

	// Find all matches first (to avoid issues with changing positions)
	c.garland.mu.RLock()
	matches, err := c.garland.findStringAllInternal(needle, opts)
	c.garland.mu.RUnlock()

	if err != nil {
		c.garland.TransactionRollback()
		return 0, ChangeResult{}, err
	}

	// Limit to count matches (first N for forward, last N for backward)
	if count >= 0 && count < len(matches) {
		matches = matches[:count]
	}

	// Process from end to start to preserve positions (reverse the limited set)
	for i, j := 0, len(matches)-1; i < j; i, j = i+1, j-1 {
		matches[i], matches[j] = matches[j], matches[i]
	}

	replacements := 0
	for _, match := range matches {
		_, result, err := c.garland.overwriteBytesAtInternal(c, match.ByteStart, match.ByteEnd-match.ByteStart, []byte(replacement), nil, false)
		if err != nil {
			c.garland.TransactionRollback()
			return replacements, ChangeResult{}, err
		}
		lastResult = result
		replacements++
	}

	result, err := c.garland.TransactionCommit()
	if err != nil {
		return replacements, ChangeResult{}, err
	}

	if replacements > 0 {
		return replacements, result, nil
	}
	return 0, lastResult, nil
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

	c.garland.mu.RLock()
	defer c.garland.mu.RUnlock()

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

	c.garland.mu.RLock()
	defer c.garland.mu.RUnlock()

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

	c.garland.mu.RLock()
	defer c.garland.mu.RUnlock()

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
	c.garland.mu.RLock()
	match, err := c.garland.findRegexInternal(c.bytePos, re, opts)
	c.garland.mu.RUnlock()

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

	// Start transaction
	err = c.garland.TransactionStart("regex-replace")
	if err != nil {
		return 0, ChangeResult{}, err
	}

	var lastResult ChangeResult

	// Find all matches first
	c.garland.mu.RLock()
	matches, err := c.garland.findRegexAllInternal(re, opts)
	c.garland.mu.RUnlock()

	if err != nil {
		c.garland.TransactionRollback()
		return 0, ChangeResult{}, err
	}

	// Limit to count matches (first N for forward, last N for backward)
	if count >= 0 && count < len(matches) {
		matches = matches[:count]
	}

	// Process from end to start to preserve positions (reverse the limited set)
	for i, j := 0, len(matches)-1; i < j; i, j = i+1, j-1 {
		matches[i], matches[j] = matches[j], matches[i]
	}

	replacements := 0
	for _, match := range matches {
		// Expand replacement for this specific match
		expanded := re.ReplaceAllString(match.Match, replacement)

		_, result, err := c.garland.overwriteBytesAtInternal(c, match.ByteStart, match.ByteEnd-match.ByteStart, []byte(expanded), nil, false)
		if err != nil {
			c.garland.TransactionRollback()
			return replacements, ChangeResult{}, err
		}
		lastResult = result
		replacements++
	}

	result, err := c.garland.TransactionCommit()
	if err != nil {
		return replacements, ChangeResult{}, err
	}

	if replacements > 0 {
		return replacements, result, nil
	}
	return 0, lastResult, nil
}

// Internal implementation methods

func (g *Garland) findStringInternal(startPos int64, needle string, opts SearchOptions) (*SearchResult, error) {
	// Read all content (could be optimized for large files)
	data, err := g.readBytesRangeInternal(0, g.totalBytes)
	if err != nil {
		return nil, err
	}

	searchData := data
	searchNeedle := []byte(needle)

	if !opts.CaseSensitive {
		searchData = bytes.ToLower(data)
		searchNeedle = bytes.ToLower(searchNeedle)
	}

	if opts.Backward {
		// Search backward from startPos
		searchEnd := startPos
		if searchEnd > int64(len(searchData)) {
			searchEnd = int64(len(searchData))
		}

		// Find last occurrence before startPos
		pos := int64(-1)
		offset := int64(0)
		for {
			idx := bytes.Index(searchData[offset:searchEnd], searchNeedle)
			if idx == -1 {
				break
			}
			matchPos := offset + int64(idx)
			if opts.WholeWord && !isWholeWord(data, matchPos, int64(len(needle))) {
				offset = matchPos + 1
				continue
			}
			pos = matchPos
			offset = matchPos + 1
		}

		if pos == -1 {
			return nil, nil
		}

		return &SearchResult{
			ByteStart: pos,
			ByteEnd:   pos + int64(len(needle)),
			Match:     string(data[pos : pos+int64(len(needle))]),
		}, nil
	}

	// Search forward from startPos
	offset := startPos
	for offset < int64(len(searchData)) {
		idx := bytes.Index(searchData[offset:], searchNeedle)
		if idx == -1 {
			return nil, nil
		}

		matchPos := offset + int64(idx)
		if opts.WholeWord && !isWholeWord(data, matchPos, int64(len(needle))) {
			offset = matchPos + 1
			continue
		}

		return &SearchResult{
			ByteStart: matchPos,
			ByteEnd:   matchPos + int64(len(needle)),
			Match:     string(data[matchPos : matchPos+int64(len(needle))]),
		}, nil
	}

	return nil, nil
}

func (g *Garland) findStringAllInternal(needle string, opts SearchOptions) ([]SearchResult, error) {
	data, err := g.readBytesRangeInternal(0, g.totalBytes)
	if err != nil {
		return nil, err
	}

	searchData := data
	searchNeedle := []byte(needle)

	if !opts.CaseSensitive {
		searchData = bytes.ToLower(data)
		searchNeedle = bytes.ToLower(searchNeedle)
	}

	var results []SearchResult
	offset := int64(0)

	for offset < int64(len(searchData)) {
		idx := bytes.Index(searchData[offset:], searchNeedle)
		if idx == -1 {
			break
		}

		matchPos := offset + int64(idx)
		if opts.WholeWord && !isWholeWord(data, matchPos, int64(len(needle))) {
			offset = matchPos + 1
			continue
		}

		results = append(results, SearchResult{
			ByteStart: matchPos,
			ByteEnd:   matchPos + int64(len(needle)),
			Match:     string(data[matchPos : matchPos+int64(len(needle))]),
		})
		offset = matchPos + int64(len(needle))
	}

	if opts.Backward {
		// Reverse results for backward search
		for i, j := 0, len(results)-1; i < j; i, j = i+1, j-1 {
			results[i], results[j] = results[j], results[i]
		}
	}

	return results, nil
}

func (g *Garland) findRegexInternal(startPos int64, re *regexp.Regexp, opts RegexOptions) (*SearchResult, error) {
	data, err := g.readBytesRangeInternal(0, g.totalBytes)
	if err != nil {
		return nil, err
	}

	if opts.Backward {
		// Find all matches before startPos, return last one
		searchData := data[:startPos]
		matches := re.FindAllIndex(searchData, -1)
		if len(matches) == 0 {
			return nil, nil
		}

		last := matches[len(matches)-1]
		return &SearchResult{
			ByteStart: int64(last[0]),
			ByteEnd:   int64(last[1]),
			Match:     string(data[last[0]:last[1]]),
		}, nil
	}

	// Forward search from startPos
	searchData := data[startPos:]
	loc := re.FindIndex(searchData)
	if loc == nil {
		return nil, nil
	}

	return &SearchResult{
		ByteStart: startPos + int64(loc[0]),
		ByteEnd:   startPos + int64(loc[1]),
		Match:     string(searchData[loc[0]:loc[1]]),
	}, nil
}

func (g *Garland) findRegexAllInternal(re *regexp.Regexp, opts RegexOptions) ([]SearchResult, error) {
	data, err := g.readBytesRangeInternal(0, g.totalBytes)
	if err != nil {
		return nil, err
	}

	matches := re.FindAllIndex(data, -1)
	if len(matches) == 0 {
		return nil, nil
	}

	results := make([]SearchResult, len(matches))
	for i, loc := range matches {
		results[i] = SearchResult{
			ByteStart: int64(loc[0]),
			ByteEnd:   int64(loc[1]),
			Match:     string(data[loc[0]:loc[1]]),
		}
	}

	if opts.Backward {
		// Reverse for backward iteration
		for i, j := 0, len(results)-1; i < j; i, j = i+1, j-1 {
			results[i], results[j] = results[j], results[i]
		}
	}

	return results, nil
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

	c.garland.mu.RLock()
	defer c.garland.mu.RUnlock()

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

	c.garland.mu.RLock()
	defer c.garland.mu.RUnlock()

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

	c.garland.mu.RLock()
	match, err := c.garland.findStringInternal(searchStart, needle, opts)
	c.garland.mu.RUnlock()

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

	c.garland.mu.RLock()
	match, err := c.garland.findRegexInternal(searchStart, re, opts)
	c.garland.mu.RUnlock()

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
