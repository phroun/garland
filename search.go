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

	// Thaw if needed (cold/warm storage -> memory)
	if snap.storageState != StorageMemory {
		forkRev := ForkRevision{r.g.currentFork, r.g.currentRevision}
		if err := r.g.thawSnapshot(leafResult.Node.id, forkRev, snap); err != nil {
			return err
		}
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
	if opts.Backward {
		return g.findStringBackwardInternal(startPos, needle, opts)
	}
	return g.findStringForwardInternal(startPos, needle, opts)
}

// findStringForwardInternal searches forward using chunked loading.
func (g *Garland) findStringForwardInternal(startPos int64, needle string, opts SearchOptions) (*SearchResult, error) {
	const chunkSize = 1024 * 1024 // 1MB chunks

	needleBytes := []byte(needle)
	needleLen := int64(len(needleBytes))

	if !opts.CaseSensitive {
		needleBytes = bytes.ToLower(needleBytes)
	}

	// Process document in chunks from startPos
	offset := startPos
	for offset < g.totalBytes {
		// Calculate chunk bounds - include overlap for boundary-spanning matches
		chunkEnd := offset + chunkSize
		if chunkEnd > g.totalBytes {
			chunkEnd = g.totalBytes
		}

		// Read this chunk
		chunkLen := chunkEnd - offset
		if chunkLen <= 0 {
			break
		}
		chunkData, err := g.readBytesRangeInternal(offset, chunkLen)
		if err != nil {
			return nil, err
		}

		searchData := chunkData
		if !opts.CaseSensitive {
			searchData = bytes.ToLower(chunkData)
		}

		// Search within chunk
		localOffset := int64(0)
		for localOffset < int64(len(searchData)) {
			idx := bytes.Index(searchData[localOffset:], needleBytes)
			if idx == -1 {
				break
			}

			matchPos := offset + localOffset + int64(idx)

			// Check whole word if required
			if opts.WholeWord {
				if !g.isWholeWordChunked(matchPos, needleLen, opts.CaseSensitive) {
					localOffset += int64(idx) + 1
					continue
				}
			}

			// Found a match
			return &SearchResult{
				ByteStart: matchPos,
				ByteEnd:   matchPos + needleLen,
				Match:     string(chunkData[localOffset+int64(idx) : localOffset+int64(idx)+needleLen]),
			}, nil
		}

		// Move to next chunk, but overlap by needle length - 1 to catch boundary matches
		// Ensure we always make forward progress
		nextOffset := chunkEnd - needleLen + 1
		if nextOffset <= offset {
			nextOffset = chunkEnd
		}
		offset = nextOffset
	}

	return nil, nil
}

// findStringBackwardInternal searches backward using chunked loading.
func (g *Garland) findStringBackwardInternal(startPos int64, needle string, opts SearchOptions) (*SearchResult, error) {
	const chunkSize = 1024 * 1024 // 1MB chunks

	needleBytes := []byte(needle)
	needleLen := int64(len(needleBytes))

	if !opts.CaseSensitive {
		needleBytes = bytes.ToLower(needleBytes)
	}

	var lastMatch *SearchResult

	// Process document in chunks from beginning to startPos
	for offset := int64(0); offset < startPos; {
		chunkEnd := offset + chunkSize
		if chunkEnd > startPos {
			chunkEnd = startPos
		}

		chunkData, err := g.readBytesRangeInternal(offset, chunkEnd-offset)
		if err != nil {
			return nil, err
		}

		searchData := chunkData
		if !opts.CaseSensitive {
			searchData = bytes.ToLower(chunkData)
		}

		// Find all matches in chunk, keep the last valid one
		localOffset := int64(0)
		for localOffset < int64(len(searchData)) {
			idx := bytes.Index(searchData[localOffset:], needleBytes)
			if idx == -1 {
				break
			}

			matchPos := offset + localOffset + int64(idx)
			matchEnd := matchPos + needleLen

			// Only consider matches that end before or at startPos
			if matchEnd <= startPos {
				if opts.WholeWord {
					if !g.isWholeWordChunked(matchPos, needleLen, opts.CaseSensitive) {
						localOffset += int64(idx) + 1
						continue
					}
				}
				lastMatch = &SearchResult{
					ByteStart: matchPos,
					ByteEnd:   matchEnd,
					Match:     string(chunkData[localOffset+int64(idx) : localOffset+int64(idx)+needleLen]),
				}
			}

			localOffset += int64(idx) + 1
		}

		// Handle boundary-spanning matches
		if chunkEnd < startPos && len(chunkData) >= int(needleLen) {
			overlapStart := chunkEnd - needleLen + 1
			overlapEnd := min(chunkEnd+needleLen-1, startPos)

			if overlapEnd > overlapStart {
				overlapData, err := g.readBytesRangeInternal(overlapStart, overlapEnd-overlapStart)
				if err == nil {
					searchOverlap := overlapData
					if !opts.CaseSensitive {
						searchOverlap = bytes.ToLower(overlapData)
					}

					localOffset := int64(0)
					for localOffset < int64(len(searchOverlap))-needleLen+1 {
						idx := bytes.Index(searchOverlap[localOffset:], needleBytes)
						if idx == -1 {
							break
						}

						matchPos := overlapStart + localOffset + int64(idx)
						matchEnd := matchPos + needleLen

						// Only count if it spans the boundary
						if matchPos < chunkEnd && matchEnd > chunkEnd && matchEnd <= startPos {
							if opts.WholeWord {
								if !g.isWholeWordChunked(matchPos, needleLen, opts.CaseSensitive) {
									localOffset += int64(idx) + 1
									continue
								}
							}
							lastMatch = &SearchResult{
								ByteStart: matchPos,
								ByteEnd:   matchEnd,
								Match:     string(overlapData[localOffset+int64(idx) : localOffset+int64(idx)+needleLen]),
							}
						}
						localOffset += int64(idx) + 1
					}
				}
			}
		}

		offset = chunkEnd
	}

	return lastMatch, nil
}

// isWholeWordChunked checks if match at position is a whole word using chunked reads.
func (g *Garland) isWholeWordChunked(pos, length int64, caseSensitive bool) bool {
	// Check character before match
	if pos > 0 {
		before, err := g.readBytesRangeInternal(pos-1, 1)
		if err == nil && len(before) > 0 {
			r, _ := utf8.DecodeRune(before)
			if isWordChar(r) {
				return false
			}
		}
	}

	// Check character after match
	if pos+length < g.totalBytes {
		after, err := g.readBytesRangeInternal(pos+length, 1)
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
	const chunkSize = 1024 * 1024 // 1MB chunks

	needleBytes := []byte(needle)
	needleLen := int64(len(needleBytes))

	if !opts.CaseSensitive {
		needleBytes = bytes.ToLower(needleBytes)
	}

	var results []SearchResult
	seenPositions := make(map[int64]bool)

	// Process document in chunks
	offset := int64(0)
	for offset < g.totalBytes {
		chunkEnd := offset + chunkSize
		if chunkEnd > g.totalBytes {
			chunkEnd = g.totalBytes
		}

		chunkLen := chunkEnd - offset
		if chunkLen <= 0 {
			break
		}
		chunkData, err := g.readBytesRangeInternal(offset, chunkLen)
		if err != nil {
			return nil, err
		}

		searchData := chunkData
		if !opts.CaseSensitive {
			searchData = bytes.ToLower(chunkData)
		}

		// Find all matches in chunk
		localOffset := int64(0)
		for localOffset < int64(len(searchData)) {
			idx := bytes.Index(searchData[localOffset:], needleBytes)
			if idx == -1 {
				break
			}

			matchPos := offset + localOffset + int64(idx)

			if !seenPositions[matchPos] {
				if opts.WholeWord {
					if !g.isWholeWordChunked(matchPos, needleLen, opts.CaseSensitive) {
						localOffset += int64(idx) + 1
						continue
					}
				}

				seenPositions[matchPos] = true
				results = append(results, SearchResult{
					ByteStart: matchPos,
					ByteEnd:   matchPos + needleLen,
					Match:     string(chunkData[localOffset+int64(idx) : localOffset+int64(idx)+needleLen]),
				})
			}

			localOffset += int64(idx) + 1
		}

		// Handle boundary-spanning matches
		if chunkEnd < g.totalBytes && len(chunkData) >= int(needleLen) {
			overlapStart := chunkEnd - needleLen + 1
			overlapEnd := min(chunkEnd+needleLen-1, g.totalBytes)

			if overlapEnd > overlapStart {
				overlapData, err := g.readBytesRangeInternal(overlapStart, overlapEnd-overlapStart)
				if err == nil {
					searchOverlap := overlapData
					if !opts.CaseSensitive {
						searchOverlap = bytes.ToLower(overlapData)
					}

					localOffset := int64(0)
					for localOffset < int64(len(searchOverlap))-needleLen+1 {
						idx := bytes.Index(searchOverlap[localOffset:], needleBytes)
						if idx == -1 {
							break
						}

						matchPos := overlapStart + localOffset + int64(idx)

						// Only count if it spans the boundary and not seen
						if matchPos < chunkEnd && matchPos+needleLen > chunkEnd && !seenPositions[matchPos] {
							if opts.WholeWord {
								if !g.isWholeWordChunked(matchPos, needleLen, opts.CaseSensitive) {
									localOffset += int64(idx) + 1
									continue
								}
							}

							seenPositions[matchPos] = true
							results = append(results, SearchResult{
								ByteStart: matchPos,
								ByteEnd:   matchPos + needleLen,
								Match:     string(overlapData[localOffset+int64(idx) : localOffset+int64(idx)+needleLen]),
							})
						}
						localOffset += int64(idx) + 1
					}
				}
			}
		}

		offset = chunkEnd
	}

	// Sort results by position
	sortSearchResults(results)

	if opts.Backward {
		// Reverse results for backward search
		for i, j := 0, len(results)-1; i < j; i, j = i+1, j-1 {
			results[i], results[j] = results[j], results[i]
		}
	}

	return results, nil
}

func (g *Garland) findRegexInternal(startPos int64, re *regexp.Regexp, opts RegexOptions) (*SearchResult, error) {
	if opts.Backward {
		// Backward regex search requires scanning from start to find last match before startPos.
		// This uses chunked loading to avoid loading entire document at once.
		return g.findRegexBackwardInternal(startPos, re)
	}

	// Forward search using streaming reader
	reader := g.newRopeRuneReader(startPos)
	loc := re.FindReaderIndex(reader)
	if loc == nil {
		return nil, nil
	}

	// loc contains byte offsets relative to startPos
	matchStart := startPos + int64(loc[0])
	matchEnd := startPos + int64(loc[1])

	// Read the matched text
	matchData, err := g.readBytesRangeInternal(matchStart, matchEnd-matchStart)
	if err != nil {
		return nil, err
	}

	return &SearchResult{
		ByteStart: matchStart,
		ByteEnd:   matchEnd,
		Match:     string(matchData),
	}, nil
}

// findRegexBackwardInternal finds the last regex match before startPos.
// Uses chunked scanning to avoid loading entire document into memory.
func (g *Garland) findRegexBackwardInternal(startPos int64, re *regexp.Regexp) (*SearchResult, error) {
	const chunkSize = 1024 * 1024 // 1MB chunks

	var lastMatch *SearchResult

	// Scan in chunks from beginning up to startPos
	for offset := int64(0); offset < startPos; {
		// Calculate chunk bounds
		chunkEnd := offset + chunkSize
		if chunkEnd > startPos {
			chunkEnd = startPos
		}

		// Read this chunk
		chunkData, err := g.readBytesRangeInternal(offset, chunkEnd-offset)
		if err != nil {
			return nil, err
		}

		// Find all matches in this chunk
		matches := re.FindAllIndex(chunkData, -1)
		for _, loc := range matches {
			matchStart := offset + int64(loc[0])
			matchEnd := offset + int64(loc[1])

			// Only consider matches that end before startPos
			if matchEnd <= startPos {
				lastMatch = &SearchResult{
					ByteStart: matchStart,
					ByteEnd:   matchEnd,
					Match:     string(chunkData[loc[0]:loc[1]]),
				}
			}
		}

		// Check for matches spanning chunk boundaries
		// Look for partial match at end of chunk that might continue
		if chunkEnd < startPos && len(chunkData) > 0 {
			// Read overlap region to catch boundary-spanning matches
			overlapStart := chunkEnd - int64(min(len(chunkData), 1024))
			overlapEnd := min(chunkEnd+1024, startPos)
			if overlapEnd > overlapStart {
				overlapData, err := g.readBytesRangeInternal(overlapStart, overlapEnd-overlapStart)
				if err == nil {
					overlapMatches := re.FindAllIndex(overlapData, -1)
					for _, loc := range overlapMatches {
						matchStart := overlapStart + int64(loc[0])
						matchEnd := overlapStart + int64(loc[1])
						// Only consider matches that span the boundary and end before startPos
						if matchStart < chunkEnd && matchEnd > chunkEnd && matchEnd <= startPos {
							lastMatch = &SearchResult{
								ByteStart: matchStart,
								ByteEnd:   matchEnd,
								Match:     string(overlapData[loc[0]:loc[1]]),
							}
						}
					}
				}
			}
		}

		offset = chunkEnd
	}

	return lastMatch, nil
}

func (g *Garland) findRegexAllInternal(re *regexp.Regexp, opts RegexOptions) ([]SearchResult, error) {
	const chunkSize = 1024 * 1024 // 1MB chunks

	var results []SearchResult
	seenPositions := make(map[int64]bool) // Track seen match starts to avoid duplicates from overlap

	// Process document in chunks
	for offset := int64(0); offset < g.totalBytes; {
		// Calculate chunk bounds with overlap for boundary-spanning matches
		chunkEnd := offset + chunkSize
		if chunkEnd > g.totalBytes {
			chunkEnd = g.totalBytes
		}

		// Read this chunk
		chunkData, err := g.readBytesRangeInternal(offset, chunkEnd-offset)
		if err != nil {
			return nil, err
		}

		// Find all matches in this chunk
		matches := re.FindAllIndex(chunkData, -1)
		for _, loc := range matches {
			matchStart := offset + int64(loc[0])
			matchEnd := offset + int64(loc[1])

			// Skip if we've seen this match start position (from overlap processing)
			if seenPositions[matchStart] {
				continue
			}
			seenPositions[matchStart] = true

			results = append(results, SearchResult{
				ByteStart: matchStart,
				ByteEnd:   matchEnd,
				Match:     string(chunkData[loc[0]:loc[1]]),
			})
		}

		// Handle matches spanning chunk boundaries by reading overlap region
		if chunkEnd < g.totalBytes && len(chunkData) > 0 {
			// Look back 1KB and forward 1KB from boundary
			overlapStart := chunkEnd - int64(min(len(chunkData), 1024))
			overlapEnd := min(chunkEnd+1024, g.totalBytes)

			if overlapEnd > overlapStart {
				overlapData, err := g.readBytesRangeInternal(overlapStart, overlapEnd-overlapStart)
				if err == nil {
					overlapMatches := re.FindAllIndex(overlapData, -1)
					for _, loc := range overlapMatches {
						matchStart := overlapStart + int64(loc[0])
						matchEnd := overlapStart + int64(loc[1])

						// Only add matches that span the boundary and haven't been seen
						if matchStart < chunkEnd && matchEnd > chunkEnd && !seenPositions[matchStart] {
							seenPositions[matchStart] = true

							results = append(results, SearchResult{
								ByteStart: matchStart,
								ByteEnd:   matchEnd,
								Match:     string(overlapData[loc[0]:loc[1]]),
							})
						}
					}
				}
			}
		}

		offset = chunkEnd
	}

	// Sort results by position (overlap processing may have added out of order)
	sortSearchResults(results)

	if opts.Backward {
		// Reverse for backward iteration
		for i, j := 0, len(results)-1; i < j; i, j = i+1, j-1 {
			results[i], results[j] = results[j], results[i]
		}
	}

	return results, nil
}

// sortSearchResults sorts results by ByteStart position.
func sortSearchResults(results []SearchResult) {
	// Simple insertion sort - results are mostly sorted already
	for i := 1; i < len(results); i++ {
		j := i
		for j > 0 && results[j-1].ByteStart > results[j].ByteStart {
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
