package garland

import (
	"sync"
	"time"
)

// CursorPosition stores a cursor's position in all coordinate systems.
type CursorPosition struct {
	BytePos  int64
	RunePos  int64
	Line     int64
	LineRune int64
}

// Cursor represents a position within a Garland with its own ready state.
// Cursors automatically update when content changes before their position.
type Cursor struct {
	garland *Garland

	// Current position. bytePos, runePos, and line are always kept in
	// sync (they shift linearly under mutations elsewhere in the
	// buffer). lineRune is maintained lazily: a mutation earlier on
	// this cursor's own line re-anchors the column non-linearly, so
	// adjustForMutation only marks it dirty and it is recomputed from
	// bytePos on the next read - edits never pay a tree walk per
	// passive cursor.
	bytePos       int64
	runePos       int64
	line          int64
	lineRune      int64
	lineRuneDirty bool

	// Version tracking for cursor history
	lastFork     ForkID
	lastRevision RevisionID

	// Cursor's own position history (sparse, only recorded when cursor moves after version change)
	positionHistory map[ForkRevision]*CursorPosition

	// Ready state
	ready     bool
	readyMu   sync.Mutex
	readyCond *sync.Cond

	// Cursor mode determines auto-region behavior
	mode CursorMode

	// Active optimized region (nil if none)
	region *OptimizedRegionHandle
}

// newCursor creates a new cursor at position 0.
func newCursor(g *Garland) *Cursor {
	c := &Cursor{
		garland:         g,
		bytePos:         0,
		runePos:         0,
		line:            0,
		lineRune:        0,
		lastFork:        g.currentFork,
		lastRevision:    g.currentRevision,
		positionHistory: make(map[ForkRevision]*CursorPosition),
		ready:           false,
		mode:            CursorModeHuman,
		region:          nil,
	}
	c.readyCond = sync.NewCond(&c.readyMu)

	// Record initial position
	c.positionHistory[ForkRevision{g.currentFork, g.currentRevision}] = &CursorPosition{
		BytePos:  0,
		RunePos:  0,
		Line:     0,
		LineRune: 0,
	}

	return c
}

// BytePos returns the cursor's absolute byte position.
// Concurrency: cursor fields are only written under the garland write
// lock (seeks, edits adjusting passive cursors), so accessors
// synchronize through it.
func (c *Cursor) BytePos() int64 {
	return c.posByte()
}

// posByte reads the byte position under the read lock.
func (c *Cursor) posByte() int64 {
	if c.garland == nil {
		return c.bytePos
	}
	c.garland.mu.RLock()
	defer c.garland.mu.RUnlock()
	return c.bytePos
}

// RunePos returns the cursor's absolute rune position.
func (c *Cursor) RunePos() int64 {
	return c.posRune()
}

// posRune reads the rune position under the read lock.
func (c *Cursor) posRune() int64 {
	if c.garland == nil {
		return c.runePos
	}
	c.garland.mu.RLock()
	defer c.garland.mu.RUnlock()
	return c.runePos
}

// LinePos returns the cursor's line number and rune position within that line.
// Both values are 0-indexed.
func (c *Cursor) LinePos() (line, runeInLine int64) {
	if c.garland == nil {
		return c.line, c.lineRune
	}
	c.garland.mu.Lock() // may lazily recompute the stale column
	defer c.garland.mu.Unlock()
	c.resolveStaleLineRuneLocked()
	return c.line, c.lineRune
}

// Position returns the cursor's position in all coordinate systems.
func (c *Cursor) Position() CursorPosition {
	if c.garland == nil {
		return CursorPosition{BytePos: c.bytePos, RunePos: c.runePos, Line: c.line, LineRune: c.lineRune}
	}
	c.garland.mu.Lock() // may lazily recompute the stale column
	defer c.garland.mu.Unlock()
	c.resolveStaleLineRuneLocked()
	return CursorPosition{
		BytePos:  c.bytePos,
		RunePos:  c.runePos,
		Line:     c.line,
		LineRune: c.lineRune,
	}
}

// IsReady returns true if the read-ahead threshold has been met
// relative to this cursor's position.
func (c *Cursor) IsReady() bool {
	c.readyMu.Lock()
	defer c.readyMu.Unlock()
	return c.ready
}

// WaitReady blocks until the cursor becomes ready.
func (c *Cursor) WaitReady() error {
	c.readyMu.Lock()
	defer c.readyMu.Unlock()

	for !c.ready {
		c.readyCond.Wait()
	}
	return nil
}

// setReady marks the cursor as ready and wakes any waiting goroutines.
func (c *Cursor) setReady(ready bool) {
	c.readyMu.Lock()
	defer c.readyMu.Unlock()

	c.ready = ready
	if ready {
		c.readyCond.Broadcast()
	}
}

// Mode returns the cursor's current mode.
func (c *Cursor) Mode() CursorMode {
	return c.mode
}

// SetMode sets the cursor's mode.
// Changing mode does not affect any currently active optimized region.
func (c *Cursor) SetMode(mode CursorMode) {
	c.mode = mode
}

// HasOptimizedRegion returns true if the cursor has an active optimized region.
func (c *Cursor) HasOptimizedRegion() bool {
	return c.region != nil
}

// OptimizedRegionSerial returns the serial number of the cursor's active region,
// or -1 if no region is active. Useful for debugging region lifecycle.
func (c *Cursor) OptimizedRegionSerial() int64 {
	if c.region == nil {
		return -1
	}
	return int64(c.region.serial)
}

// OptimizedRegionBounds returns the content bounds of the active region.
// Returns (0, 0, false) if no region is active.
func (c *Cursor) OptimizedRegionBounds() (start, end int64, ok bool) {
	if c.region == nil {
		return 0, 0, false
	}
	start, end = c.region.ContentBounds()
	return start, end, true
}

// OptimizedRegionGraceWindow returns the grace window bounds of the active region.
// Returns (0, 0, false) if no region is active.
func (c *Cursor) OptimizedRegionGraceWindow() (start, end int64, ok bool) {
	if c.region == nil {
		return 0, 0, false
	}
	start, end = c.region.GraceWindow()
	return start, end, true
}

// BeginOptimizedRegion explicitly starts an optimized region at the specified bounds.
//
// QUARANTINED: optimized regions are unfinished scaffolding. The edit
// paths never route through an active region, so a region created here
// holds FIXED bounds that go stale on the next checkpoint/transaction -
// a live corruption hazard. Until the feature is either fully wired in
// or removed, this returns ErrNotSupported. (RULING 2026-07-12: keep
// the scaffolding, block the entry point.)
func (c *Cursor) BeginOptimizedRegion(startByte, endByte int64) error {
	if c.garland == nil {
		return ErrCursorNotFound
	}
	return ErrNotSupported
}

// SeekByte moves the cursor to an absolute byte position.
// Blocks indefinitely until the position is available during lazy loading.
// Use SeekByteWithTimeout for timeout control, or check IsByteReady first for non-blocking.
func (c *Cursor) SeekByte(pos int64) error {
	return c.SeekByteWithTimeout(pos, -1) // -1 = block indefinitely
}

// SeekByteWithTimeout moves the cursor to an absolute byte position with timeout control.
// If timeout is 0, returns ErrNotReady immediately if position not available.
// If timeout is negative, blocks indefinitely.
// If timeout is positive, waits up to that duration before returning ErrTimeout.
func (c *Cursor) SeekByteWithTimeout(pos int64, timeout time.Duration) error {
	if c.garland == nil {
		return ErrCursorNotFound
	}

	// Wait for position to be available
	if err := c.garland.waitForBytePosition(pos, timeout); err != nil {
		return err
	}

	// Compute all coordinates and update the cursor under one lock.
	return c.garland.setCursorFromByte(c, pos)
}

// SeekRune moves the cursor to an absolute rune position.
// Blocks indefinitely until the position is available during lazy loading.
// Use SeekRuneWithTimeout for timeout control, or check IsRuneReady first for non-blocking.
func (c *Cursor) SeekRune(pos int64) error {
	return c.SeekRuneWithTimeout(pos, -1) // -1 = block indefinitely
}

// SeekRuneWithTimeout moves the cursor to an absolute rune position with timeout control.
// If timeout is 0, returns ErrNotReady immediately if position not available.
// If timeout is negative, blocks indefinitely.
// If timeout is positive, waits up to that duration before returning ErrTimeout.
func (c *Cursor) SeekRuneWithTimeout(pos int64, timeout time.Duration) error {
	if c.garland == nil {
		return ErrCursorNotFound
	}

	// Wait for position to be available
	if err := c.garland.waitForRunePosition(pos, timeout); err != nil {
		return err
	}

	// Compute all coordinates and update the cursor under one lock.
	return c.garland.setCursorFromRune(c, pos)
}

// SeekLine moves the cursor to a line and rune-within-line position.
// Line and rune are both 0-indexed. The newline is the last character of its line.
// Blocks indefinitely until the position is available during lazy loading.
// Use SeekLineWithTimeout for timeout control, or check IsLineReady first for non-blocking.
func (c *Cursor) SeekLine(line, runeInLine int64) error {
	return c.SeekLineWithTimeout(line, runeInLine, -1) // -1 = block indefinitely
}

// SeekLineWithTimeout moves the cursor to a line and rune-within-line position with timeout control.
// If timeout is 0, returns ErrNotReady immediately if position not available.
// If timeout is negative, blocks indefinitely.
// If timeout is positive, waits up to that duration before returning ErrTimeout.
func (c *Cursor) SeekLineWithTimeout(line, runeInLine int64, timeout time.Duration) error {
	if c.garland == nil {
		return ErrCursorNotFound
	}

	// Wait for line to be available
	if err := c.garland.waitForLine(line, timeout); err != nil {
		return err
	}

	// Compute all coordinates and update the cursor under one lock.
	return c.garland.setCursorFromLine(c, line, runeInLine)
}

// SeekRelativeBytes moves the cursor relative to its current byte position.
// Positive delta moves forward, negative moves backward.
// Clamps to valid range [0, byteCount].
func (c *Cursor) SeekRelativeBytes(delta int64) error {
	if c.garland == nil {
		return ErrCursorNotFound
	}

	newPos := c.posByte() + delta
	if newPos < 0 {
		newPos = 0
	}
	// Clamp to byte count (will be validated by SeekByte)
	return c.SeekByte(newPos)
}

// SeekRelativeRunes moves the cursor relative to its current rune position.
// Positive delta moves forward, negative moves backward.
// Clamps to valid range [0, runeCount].
func (c *Cursor) SeekRelativeRunes(delta int64) error {
	if c.garland == nil {
		return ErrCursorNotFound
	}

	newPos := c.posRune() + delta
	if newPos < 0 {
		newPos = 0
	}
	// Clamp to rune count (will be validated by SeekRune)
	return c.SeekRune(newPos)
}

// WordStyle selects the word-boundary semantics for word motions.
type WordStyle int

const (
	// WordStyleSimple: a word is a run of letters/digits/underscore;
	// punctuation and whitespace are separators (never stops).
	WordStyleSimple WordStyle = iota

	// WordStyleVi: like vi's w/b - punctuation runs are words of
	// their own, so both word-character runs and punctuation runs are
	// stops; only whitespace separates.
	WordStyleVi
)

// SeekByWord moves the cursor by n words using WordStyleSimple.
// Positive n moves forward, negative n moves backward.
// Returns the number of words actually moved (may be less than
// requested at the buffer boundaries). Use SeekByWordStyle to choose
// different word semantics.
func (c *Cursor) SeekByWord(n int) (int, error) {
	return c.SeekByWordStyle(n, WordStyleSimple)
}

// SeekByWordStyle moves the cursor by n words under the given
// WordStyle. Positive n moves forward, negative n moves backward.
// Returns the number of words actually moved.
func (c *Cursor) SeekByWordStyle(n int, style WordStyle) (int, error) {
	if c.garland == nil {
		return 0, ErrCursorNotFound
	}
	return c.garland.seekByWordAt(c, n, style)
}

// SeekLineStart moves the cursor to the beginning of the current line.
func (c *Cursor) SeekLineStart() error {
	if c.garland == nil {
		return ErrCursorNotFound
	}
	// Simply set lineRune to 0 and recalculate byte/rune positions
	return c.SeekLine(c.line, 0)
}

// SeekLineEnd moves the cursor to the end of the current line.
// The cursor is positioned after the last character before the newline (or at EOF).
func (c *Cursor) SeekLineEnd() error {
	if c.garland == nil {
		return ErrCursorNotFound
	}
	return c.garland.seekLineEndAt(c)
}

// updatePosition updates the cursor's position and records history if needed.
func (c *Cursor) updatePosition(bytePos, runePos, line, lineRune int64) {
	c.bytePos = bytePos
	c.runePos = runePos
	c.line = line
	c.lineRune = lineRune
	c.lineRuneDirty = false

	// Record position in history if version has changed. NEVER while a
	// transaction holds uncommitted mutations: currentRevision is still
	// the PRE-transaction revision then, but this position was computed
	// against the mid-transaction tree - stamping it under that key
	// hands UndoSeek coordinates that are incoherent with the revision's
	// real content. (TransactionStart already recorded the coherent
	// pre-transaction positions under this key.)
	if c.garland != nil {
		currentFork := c.garland.currentFork
		currentRev := c.garland.currentRevision
		inMutatedTx := c.garland.transaction != nil && c.garland.transaction.hasMutations

		if !inMutatedTx && (c.lastFork != currentFork || c.lastRevision != currentRev) {
			c.positionHistory[ForkRevision{currentFork, currentRev}] = &CursorPosition{
				BytePos:  bytePos,
				RunePos:  runePos,
				Line:     line,
				LineRune: lineRune,
			}
			c.lastFork = currentFork
			c.lastRevision = currentRev
		}
	}

	// Update highest seek position
	if c.garland != nil && bytePos > c.garland.highestSeekPos {
		c.garland.highestSeekPos = bytePos
	}
}

// adjustForMutation adjusts cursor position after a mutation.
// mutationPos is where the mutation occurred (byte position).
// byteDelta, runeDelta, lineDelta are the size changes (positive for insert, negative for delete).
// includeAtPos governs a cursor sitting EXACTLY at the mutation
// position on an insert: true (insertBefore) shifts it past the new
// content, false leaves it anchored before the insert. Cursors beyond
// the position always shift.
func (c *Cursor) adjustForMutation(mutationPos int64, byteDelta, runeDelta, lineDelta int64, includeAtPos bool) {
	if c.bytePos > mutationPos || (includeAtPos && c.bytePos == mutationPos && byteDelta > 0) {
		// Byte, rune, and line all shift LINEARLY when content changes
		// before this cursor - O(1) with no tree access.
		c.bytePos += byteDelta
		c.runePos += runeDelta
		c.line += lineDelta
		// lineRune is NOT delta-safe: a newline inserted or removed
		// earlier on this cursor's own line re-anchors the column
		// against a new last-newline. Resolve lazily on next read.
		c.lineRuneDirty = true
	}
}

// resolveStaleLineRune recomputes lineRune (and line, authoritatively)
// from bytePos when a mutation left it stale. locked selects the
// internal conversion for callers already holding the garland lock.
// resolveStaleLineRuneLocked recomputes the lazily-maintained
// line:rune coordinates. Caller must hold the garland write lock.
func (c *Cursor) resolveStaleLineRuneLocked() {
	if !c.lineRuneDirty || c.garland == nil {
		return
	}
	line, lineRune, err := c.garland.byteToLineRuneInternalUnlocked(c.bytePos)
	if err == nil {
		c.line, c.lineRune = line, lineRune
		c.lineRuneDirty = false
	}
}

// restorePosition restores the cursor to a previously recorded position.
func (c *Cursor) restorePosition(pos *CursorPosition) {
	if pos != nil {
		c.bytePos = pos.BytePos
		c.runePos = pos.RunePos
		c.line = pos.Line
		c.lineRune = pos.LineRune
		c.lineRuneDirty = false
	}
}

// snapshotPosition returns a copy of the cursor's current position.
// Callers hold the garland lock (transaction start).
func (c *Cursor) snapshotPosition() *CursorPosition {
	c.resolveStaleLineRuneLocked()
	return &CursorPosition{
		BytePos:  c.bytePos,
		RunePos:  c.runePos,
		Line:     c.line,
		LineRune: c.lineRune,
	}
}

// InsertBytes inserts raw bytes at the cursor position.
// If insertBefore is true, insertion occurs before any existing
// cursors/decorations at this position; otherwise after.
// After insertion, cursor advances to the end of the inserted content.
func (c *Cursor) InsertBytes(data []byte, decorations []RelativeDecoration, insertBefore bool) (ChangeResult, error) {
	if c.garland == nil {
		return ChangeResult{}, ErrCursorNotFound
	}
	if err := validateRelativeDecorations(decorations); err != nil {
		return ChangeResult{}, err
	}
	result, err := c.garland.insertBytesAt(c, c.posByte(), data, decorations, insertBefore)
	if err != nil {
		return result, err
	}
	// Advance cursor to end of inserted content
	c.SeekByte(c.posByte() + int64(len(data)))
	return result, nil
}

// InsertString inserts a string at the cursor position.
// Relative decoration positions are measured in runes.
// If insertBefore is true, insertion occurs before any existing
// cursors/decorations at this position; otherwise after.
// After insertion, cursor advances to the end of the inserted content.
func (c *Cursor) InsertString(data string, decorations []RelativeDecoration, insertBefore bool) (ChangeResult, error) {
	if c.garland == nil {
		return ChangeResult{}, ErrCursorNotFound
	}
	if err := validateRelativeDecorations(decorations); err != nil {
		return ChangeResult{}, err
	}
	result, err := c.garland.insertStringAt(c, c.posByte(), data, decorations, insertBefore)
	if err != nil {
		return result, err
	}
	// Advance cursor to end of inserted content
	c.SeekByte(c.posByte() + int64(len(data)))
	return result, nil
}

// DeleteBytes deletes `length` bytes starting at cursor position.
// Returns decorations from the deleted range.
// If includeLineDecorations is true, also returns (but does not move)
// decorations from partially affected lines.
func (c *Cursor) DeleteBytes(length int64, includeLineDecorations bool) ([]RelativeDecoration, ChangeResult, error) {
	if c.garland == nil {
		return nil, ChangeResult{}, ErrCursorNotFound
	}
	return c.garland.deleteBytesAt(c, c.posByte(), length, includeLineDecorations)
}

// OverwriteBytes replaces `length` bytes at cursor position with new data.
// This is more efficient than separate delete + insert for binary editing.
// The operation properly accounts for changes in line counts (newlines)
// and rune counts (UTF-8 sequences).
// Returns decorations that were in the overwritten range.
// Cursor position is not changed after the operation.
func (c *Cursor) OverwriteBytes(length int64, newData []byte) ([]RelativeDecoration, ChangeResult, error) {
	if c.garland == nil {
		return nil, ChangeResult{}, ErrCursorNotFound
	}
	return c.garland.overwriteBytesAt(c, c.posByte(), length, newData)
}

// OverwriteBytesWithDecorations replaces bytes with new data, adding decorations.
// - decorationsToAdd: decorations to add to the new content (relative to new content start)
// - insertBefore: if true, displaced decorations consolidate to end; if false, to start
// Returns the original decorations from the overwritten range with their original relative positions.
func (c *Cursor) OverwriteBytesWithDecorations(length int64, newData []byte, decorationsToAdd []RelativeDecoration, insertBefore bool) ([]RelativeDecoration, ChangeResult, error) {
	if c.garland == nil {
		return nil, ChangeResult{}, ErrCursorNotFound
	}
	if err := validateRelativeDecorations(decorationsToAdd); err != nil {
		return nil, ChangeResult{}, err
	}
	return c.garland.overwriteBytesAtInternal(c, c.posByte(), length, newData, decorationsToAdd, insertBefore)
}

// MoveBytes moves a byte range to a new location.
// All four addresses are interpreted in the document AS IT STANDS AT
// THE MOMENT OF THIS CALL: the operation is internally composite
// (extract, delete destination, insert), and the implementation
// adjusts for its own intermediate shifts - the caller never
// compensates. (Not "as opened": prior edits are already reflected.)
// Source and destination ranges cannot overlap for Move.
// Decorations in the source range move with the content.
// Decorations in the destination range are consolidated and returned.
// - srcStart, srcEnd: source byte range [srcStart, srcEnd)
// - dstStart, dstEnd: destination byte range to replace [dstStart, dstEnd)
// - insertBefore: if true, displaced decorations consolidate to end of new content
// Returns MoveResult with the displaced decorations from the destination range.
func (c *Cursor) MoveBytes(srcStart, srcEnd, dstStart, dstEnd int64, insertBefore bool) (MoveResult, error) {
	if c.garland == nil {
		return MoveResult{}, ErrCursorNotFound
	}
	return c.garland.moveBytesAt(c, srcStart, srcEnd, dstStart, dstEnd, insertBefore)
}

// CopyBytes copies a byte range to a new location.
// All four addresses are interpreted in the document AS IT STANDS AT
// THE MOMENT OF THIS CALL (see MoveBytes; the operation compensates
// for its own intermediate shifts, never the caller).
// Source and destination ranges may overlap for Copy (source is snapshotted first).
// - srcStart, srcEnd: source byte range [srcStart, srcEnd)
// - dstStart, dstEnd: destination byte range to replace [dstStart, dstEnd)
// - decorationsToAdd: decorations to add to the copied content (like Insert)
// - insertBefore: if true, displaced decorations consolidate to end of new content
// Returns CopyResult with the displaced decorations from the destination range.
func (c *Cursor) CopyBytes(srcStart, srcEnd, dstStart, dstEnd int64, decorationsToAdd []RelativeDecoration, insertBefore bool) (CopyResult, error) {
	if c.garland == nil {
		return CopyResult{}, ErrCursorNotFound
	}
	if err := validateRelativeDecorations(decorationsToAdd); err != nil {
		return CopyResult{}, err
	}
	return c.garland.copyBytesAt(c, srcStart, srcEnd, dstStart, dstEnd, decorationsToAdd, insertBefore)
}

// DeleteRunes deletes `length` runes starting at cursor position.
// Returns decorations from the deleted range.
// If includeLineDecorations is true, also returns (but does not move)
// decorations from partially affected lines.
func (c *Cursor) DeleteRunes(length int64, includeLineDecorations bool) ([]RelativeDecoration, ChangeResult, error) {
	if c.garland == nil {
		return nil, ChangeResult{}, ErrCursorNotFound
	}
	return c.garland.deleteRunesAt(c, c.posRune(), length, includeLineDecorations)
}

// TruncateToEOF deletes everything from cursor position to end of file.
func (c *Cursor) TruncateToEOF() (ChangeResult, error) {
	if c.garland == nil {
		return ChangeResult{}, ErrCursorNotFound
	}
	return c.garland.truncateAt(c, c.posByte())
}

// ReadBytes reads `length` bytes starting at cursor position.
// After reading, cursor advances past the read data.
func (c *Cursor) ReadBytes(length int64) ([]byte, error) {
	if c.garland == nil {
		return nil, ErrCursorNotFound
	}
	data, err := c.garland.readBytesAt(c.posByte(), length)
	if err != nil {
		return nil, err
	}
	// Advance cursor by actual bytes read
	c.SeekByte(c.posByte() + int64(len(data)))
	return data, nil
}

// ReadString reads `length` runes starting at cursor position as a string.
// After reading, cursor advances past the read data.
func (c *Cursor) ReadString(length int64) (string, error) {
	if c.garland == nil {
		return "", ErrCursorNotFound
	}
	data, err := c.garland.readStringAt(c.posRune(), length)
	if err != nil {
		return "", err
	}
	// Advance cursor by actual runes read
	c.SeekRune(c.posRune() + int64(len([]rune(data))))
	return data, nil
}

// ReadLine reads the entire line the cursor is on.
// Note: Does NOT advance cursor (line-oriented reading is typically peek-like).
func (c *Cursor) ReadLine() (string, error) {
	if c.garland == nil {
		return "", ErrCursorNotFound
	}
	return c.garland.readLineAt(c.line)
}

// BackDeleteBytes deletes `length` bytes BEFORE the cursor position.
// Cursor moves to the start of the deleted range (its new position).
// Returns decorations from the deleted range.
func (c *Cursor) BackDeleteBytes(length int64, includeLineDecorations bool) ([]RelativeDecoration, ChangeResult, error) {
	if c.garland == nil {
		return nil, ChangeResult{}, ErrCursorNotFound
	}
	if length <= 0 {
		return nil, ChangeResult{Fork: c.garland.currentFork, Revision: c.garland.currentRevision}, nil
	}
	// Calculate start position (clamp to 0)
	startPos := c.posByte() - length
	if startPos < 0 {
		length = c.posByte()
		startPos = 0
	}
	// Move cursor to start of delete range
	c.SeekByte(startPos)
	// Perform delete at new position
	return c.garland.deleteBytesAt(c, startPos, length, includeLineDecorations)
}

// BackDeleteRunes deletes `length` runes BEFORE the cursor position.
// Cursor moves to the start of the deleted range (its new position).
// Returns decorations from the deleted range.
func (c *Cursor) BackDeleteRunes(length int64, includeLineDecorations bool) ([]RelativeDecoration, ChangeResult, error) {
	if c.garland == nil {
		return nil, ChangeResult{}, ErrCursorNotFound
	}
	if length <= 0 {
		return nil, ChangeResult{Fork: c.garland.currentFork, Revision: c.garland.currentRevision}, nil
	}
	// Calculate start position (clamp to 0)
	startRunePos := c.posRune() - length
	if startRunePos < 0 {
		length = c.posRune()
		startRunePos = 0
	}
	// Move cursor to start of delete range
	c.SeekRune(startRunePos)
	// Perform delete at new position
	return c.garland.deleteRunesAt(c, startRunePos, length, includeLineDecorations)
}
