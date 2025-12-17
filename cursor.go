package garland

import "sync"

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

	// Current position (always kept in sync across all three coordinate systems)
	bytePos  int64
	runePos  int64
	line     int64
	lineRune int64

	// Version tracking for cursor history
	lastFork     ForkID
	lastRevision RevisionID

	// Cursor's own position history (sparse, only recorded when cursor moves after version change)
	positionHistory map[ForkRevision]*CursorPosition

	// Ready state
	ready     bool
	readyMu   sync.Mutex
	readyCond *sync.Cond
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
func (c *Cursor) BytePos() int64 {
	return c.bytePos
}

// RunePos returns the cursor's absolute rune position.
func (c *Cursor) RunePos() int64 {
	return c.runePos
}

// LinePos returns the cursor's line number and rune position within that line.
// Both values are 0-indexed.
func (c *Cursor) LinePos() (line, runeInLine int64) {
	return c.line, c.lineRune
}

// Position returns the cursor's position in all coordinate systems.
func (c *Cursor) Position() CursorPosition {
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

// SeekByte moves the cursor to an absolute byte position.
// Blocks until the position is available during lazy loading.
func (c *Cursor) SeekByte(pos int64) error {
	if c.garland == nil {
		return ErrCursorNotFound
	}

	// Wait for position to be available
	if err := c.garland.waitForBytePosition(pos); err != nil {
		return err
	}

	// Convert byte position to other coordinate systems
	runePos, err := c.garland.byteToRuneInternal(pos)
	if err != nil {
		return err
	}

	line, lineRune, err := c.garland.byteToLineRuneInternal(pos)
	if err != nil {
		return err
	}

	c.updatePosition(pos, runePos, line, lineRune)
	return nil
}

// SeekRune moves the cursor to an absolute rune position.
// Blocks until the position is available during lazy loading.
func (c *Cursor) SeekRune(pos int64) error {
	if c.garland == nil {
		return ErrCursorNotFound
	}

	// Wait for position to be available
	if err := c.garland.waitForRunePosition(pos); err != nil {
		return err
	}

	// Convert rune position to byte position
	bytePos, err := c.garland.runeToByteInternal(pos)
	if err != nil {
		return err
	}

	line, lineRune, err := c.garland.byteToLineRuneInternal(bytePos)
	if err != nil {
		return err
	}

	c.updatePosition(bytePos, pos, line, lineRune)
	return nil
}

// SeekLine moves the cursor to a line and rune-within-line position.
// Line and rune are both 0-indexed. The newline is the last character of its line.
// Blocks until the position is available during lazy loading.
func (c *Cursor) SeekLine(line, runeInLine int64) error {
	if c.garland == nil {
		return ErrCursorNotFound
	}

	// Wait for line to be available
	if err := c.garland.waitForLine(line); err != nil {
		return err
	}

	// Convert line:rune to byte position
	bytePos, err := c.garland.lineRuneToByteInternal(line, runeInLine)
	if err != nil {
		return err
	}

	runePos, err := c.garland.byteToRuneInternal(bytePos)
	if err != nil {
		return err
	}

	c.updatePosition(bytePos, runePos, line, runeInLine)
	return nil
}

// SeekRelativeBytes moves the cursor relative to its current byte position.
// Positive delta moves forward, negative moves backward.
// Clamps to valid range [0, byteCount].
func (c *Cursor) SeekRelativeBytes(delta int64) error {
	if c.garland == nil {
		return ErrCursorNotFound
	}

	newPos := c.bytePos + delta
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

	newPos := c.runePos + delta
	if newPos < 0 {
		newPos = 0
	}
	// Clamp to rune count (will be validated by SeekRune)
	return c.SeekRune(newPos)
}

// updatePosition updates the cursor's position and records history if needed.
func (c *Cursor) updatePosition(bytePos, runePos, line, lineRune int64) {
	c.bytePos = bytePos
	c.runePos = runePos
	c.line = line
	c.lineRune = lineRune

	// Record position in history if version has changed
	if c.garland != nil {
		currentFork := c.garland.currentFork
		currentRev := c.garland.currentRevision

		if c.lastFork != currentFork || c.lastRevision != currentRev {
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
func (c *Cursor) adjustForMutation(mutationPos int64, byteDelta, runeDelta, lineDelta int64) {
	if c.bytePos > mutationPos {
		c.bytePos += byteDelta
		c.runePos += runeDelta
		// Line position adjustment is more complex - only adjust if mutation was on a prior line
		// For simplicity, we adjust lineRune only, as line number changes depend on newline insertions
		// If the mutation added/removed newlines before our line, adjust line number
		if lineDelta != 0 {
			c.line += lineDelta
		}
	} else if c.bytePos == mutationPos && byteDelta > 0 {
		// Insert at cursor position - cursor stays at same logical position
		// but the content shifted, so coordinates shift too
		c.bytePos += byteDelta
		c.runePos += runeDelta
		if lineDelta != 0 {
			c.line += lineDelta
		}
	}
}

// restorePosition restores the cursor to a previously recorded position.
func (c *Cursor) restorePosition(pos *CursorPosition) {
	if pos != nil {
		c.bytePos = pos.BytePos
		c.runePos = pos.RunePos
		c.line = pos.Line
		c.lineRune = pos.LineRune
	}
}

// snapshotPosition returns a copy of the cursor's current position.
func (c *Cursor) snapshotPosition() *CursorPosition {
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
	result, err := c.garland.insertBytesAt(c, c.bytePos, data, decorations, insertBefore)
	if err != nil {
		return result, err
	}
	// Advance cursor to end of inserted content
	c.SeekByte(c.bytePos + int64(len(data)))
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
	result, err := c.garland.insertStringAt(c, c.bytePos, data, decorations, insertBefore)
	if err != nil {
		return result, err
	}
	// Advance cursor to end of inserted content
	c.SeekByte(c.bytePos + int64(len(data)))
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
	return c.garland.deleteBytesAt(c, c.bytePos, length, includeLineDecorations)
}

// DeleteRunes deletes `length` runes starting at cursor position.
// Returns decorations from the deleted range.
// If includeLineDecorations is true, also returns (but does not move)
// decorations from partially affected lines.
func (c *Cursor) DeleteRunes(length int64, includeLineDecorations bool) ([]RelativeDecoration, ChangeResult, error) {
	if c.garland == nil {
		return nil, ChangeResult{}, ErrCursorNotFound
	}
	return c.garland.deleteRunesAt(c, c.runePos, length, includeLineDecorations)
}

// TruncateToEOF deletes everything from cursor position to end of file.
func (c *Cursor) TruncateToEOF() (ChangeResult, error) {
	if c.garland == nil {
		return ChangeResult{}, ErrCursorNotFound
	}
	return c.garland.truncateAt(c, c.bytePos)
}

// ReadBytes reads `length` bytes starting at cursor position.
// After reading, cursor advances past the read data.
func (c *Cursor) ReadBytes(length int64) ([]byte, error) {
	if c.garland == nil {
		return nil, ErrCursorNotFound
	}
	data, err := c.garland.readBytesAt(c.bytePos, length)
	if err != nil {
		return nil, err
	}
	// Advance cursor by actual bytes read
	c.SeekByte(c.bytePos + int64(len(data)))
	return data, nil
}

// ReadString reads `length` runes starting at cursor position as a string.
// After reading, cursor advances past the read data.
func (c *Cursor) ReadString(length int64) (string, error) {
	if c.garland == nil {
		return "", ErrCursorNotFound
	}
	data, err := c.garland.readStringAt(c.runePos, length)
	if err != nil {
		return "", err
	}
	// Advance cursor by actual runes read
	c.SeekRune(c.runePos + int64(len([]rune(data))))
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
	startPos := c.bytePos - length
	if startPos < 0 {
		length = c.bytePos
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
	startRunePos := c.runePos - length
	if startRunePos < 0 {
		length = c.runePos
		startRunePos = 0
	}
	// Move cursor to start of delete range
	c.SeekRune(startRunePos)
	// Perform delete at new position
	return c.garland.deleteRunesAt(c, startRunePos, length, includeLineDecorations)
}
