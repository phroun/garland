package garland

// region_ops.go contains methods for managing optimized regions attached to cursors.

// Checkpoint commits all active optimized regions across all cursors.
// This creates a single revision containing all pending region changes.
// Call this to establish an undo point after a burst of edits.
func (g *Garland) Checkpoint() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.checkpointUnlocked()
}

// checkpointUnlocked commits all regions without acquiring the lock.
func (g *Garland) checkpointUnlocked() error {
	// Collect all cursors with active regions
	hasChanges := false
	for _, cursor := range g.cursors {
		if cursor.region != nil {
			hasChanges = true
			if err := g.dissolveRegionUnlocked(cursor); err != nil {
				return err
			}
		}
	}

	// Only record a mutation if there were actual changes
	if hasChanges {
		g.recordMutation()
	}

	return nil
}

// dissolveAllRegions dissolves all active regions, flushing their content to the tree.
// Used before operations that need a consistent tree state.
func (g *Garland) dissolveAllRegions() error {
	for _, cursor := range g.cursors {
		if cursor.region != nil {
			if err := g.dissolveRegionUnlocked(cursor); err != nil {
				return err
			}
		}
	}
	return nil
}

// discardAllRegions abandons all active regions without flushing to tree.
// Used during transaction rollback.
func (g *Garland) discardAllRegions() {
	for _, cursor := range g.cursors {
		if cursor.region != nil {
			cursor.region = nil
		}
	}
}

// dissolveRegionUnlocked dissolves a single cursor's region into the tree.
// Does not create a revision - caller is responsible for that.
func (g *Garland) dissolveRegionUnlocked(cursor *Cursor) error {
	if cursor.region == nil {
		return nil
	}

	handle := cursor.region

	// Get the region's content
	content := handle.buffer.Content()

	// Delete the original content from the tree at the region's position
	contentStart, contentEnd := handle.ContentBounds()
	originalLen := contentEnd - contentStart

	if originalLen > 0 {
		// Delete without creating revision (we'll handle that at checkpoint)
		_, err := g.deleteFromTreeNoRevision(contentStart, originalLen)
		if err != nil {
			return err
		}
	}

	// Insert the new content with decorations
	if len(content) > 0 || len(handle.decorations) > 0 {
		// Convert decorations to relative format
		relDecs := make([]RelativeDecoration, len(handle.decorations))
		for i, d := range handle.decorations {
			relDecs[i] = RelativeDecoration{
				Key:      d.Key,
				Position: d.Position,
			}
		}

		err := g.insertIntoTreeNoRevision(contentStart, content, relDecs, true)
		if err != nil {
			return err
		}
	}

	// Clear the region
	cursor.region = nil

	return nil
}

// deleteFromTreeNoRevision deletes bytes without recording a mutation.
func (g *Garland) deleteFromTreeNoRevision(pos, length int64) ([]Decoration, error) {
	if length == 0 {
		return nil, nil
	}

	// Find the leaf containing the start position
	leafResult, err := g.findLeafByByteUnlocked(pos)
	if err != nil {
		return nil, err
	}

	// Perform the deletion
	var deletedDecs []Decoration
	newRootID, err := g.deleteRangeInternal(
		g.root,
		g.root.snapshotAt(g.currentFork, g.currentRevision),
		pos,
		pos+length,
		0,
		&deletedDecs,
	)
	if err != nil {
		return nil, err
	}

	// Update root
	g.root = g.nodeRegistry[newRootID]

	// Update cursors (shift positions after deletion)
	for _, c := range g.cursors {
		if c.bytePos > pos {
			if c.bytePos <= pos+length {
				// Cursor was in deleted range, move to deletion point
				c.bytePos = pos
			} else {
				// Cursor was after deleted range, shift back
				c.bytePos -= length
			}
			// Recalculate other coordinates
			c.runePos, _ = g.byteToRuneInternalUnlocked(c.bytePos)
			c.line, c.lineRune, _ = g.byteToLineRuneInternalUnlocked(c.bytePos)
		}
	}

	_ = leafResult // silence unused warning
	return deletedDecs, nil
}

// insertIntoTreeNoRevision inserts bytes without recording a mutation.
func (g *Garland) insertIntoTreeNoRevision(pos int64, data []byte, decorations []RelativeDecoration, insertBefore bool) error {
	if len(data) == 0 && len(decorations) == 0 {
		return nil
	}

	// Find the leaf containing the position
	leafResult, err := g.findLeafByByteUnlocked(pos)
	if err != nil {
		return err
	}

	// Perform the insertion
	newRootID, err := g.insertInternal(
		g.root,
		g.root.snapshotAt(g.currentFork, g.currentRevision),
		pos,
		0,
		data,
		decorations,
		insertBefore,
	)
	if err != nil {
		return err
	}

	// Update root
	g.root = g.nodeRegistry[newRootID]

	// Update cursors (shift positions after insertion)
	insertLen := int64(len(data))
	for _, c := range g.cursors {
		if c.bytePos > pos || (c.bytePos == pos && !insertBefore) {
			c.bytePos += insertLen
			// Recalculate other coordinates
			c.runePos, _ = g.byteToRuneInternalUnlocked(c.bytePos)
			c.line, c.lineRune, _ = g.byteToLineRuneInternalUnlocked(c.bytePos)
		}
	}

	_ = leafResult // silence unused warning
	return nil
}

// beginOptimizedRegionForCursor creates an optimized region for a cursor.
// If the cursor already has a region, it is dissolved first.
func (g *Garland) beginOptimizedRegionForCursor(cursor *Cursor, startByte, endByte int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Dissolve existing region if any
	if cursor.region != nil {
		if err := g.dissolveRegionUnlocked(cursor); err != nil {
			return err
		}
	}

	return g.createRegionForCursorUnlocked(cursor, startByte, endByte)
}

// createRegionForCursorUnlocked creates a new region without dissolving existing.
func (g *Garland) createRegionForCursorUnlocked(cursor *Cursor, startByte, endByte int64) error {
	// Validate bounds
	totalBytes := g.calculateTotalBytesUnlocked()
	if startByte < 0 {
		startByte = 0
	}
	if endByte > totalBytes {
		endByte = totalBytes
	}
	if startByte > endByte {
		startByte = endByte
	}

	// Read content from tree
	length := endByte - startByte
	var content []byte
	var err error
	if length > 0 {
		content, err = g.readBytesRangeInternal(startByte, length)
		if err != nil {
			return err
		}
	}

	// Collect decorations in the range (use internal method to avoid lock)
	var decorations []Decoration
	if length > 0 {
		rawDecs := g.collectDecorationsInRange(startByte, endByte)
		for _, d := range rawDecs {
			// Convert to relative position within region
			decorations = append(decorations, Decoration{
				Key:      d.Key,
				Position: d.Position - startByte,
			})
		}
	}

	// Create the buffer
	buffer := NewByteBufferRegion(content)

	// Calculate grace window (centered on the specified range)
	graceStart := startByte - g.graceWindowSize/2
	graceEnd := endByte + g.graceWindowSize/2
	if graceStart < 0 {
		graceStart = 0
	}
	if graceEnd > totalBytes {
		graceEnd = totalBytes
	}

	handle := &OptimizedRegionHandle{
		serial:       nextRegionSerial(),
		graceStart:   graceStart,
		graceEnd:     graceEnd,
		contentStart: startByte,
		buffer:       buffer,
		decorations:  decorations,
		cursor:       cursor,
	}

	cursor.region = handle
	return nil
}

// ensureRegionForEdit creates or updates a region for a human cursor before an edit.
// Returns true if the edit should proceed into the region, false if it should go to tree.
func (g *Garland) ensureRegionForEdit(cursor *Cursor, editPos int64) (bool, error) {
	// Process cursors don't auto-create regions
	if cursor.mode == CursorModeProcess {
		// If there's an existing region and edit is inside, use it
		if cursor.region != nil {
			graceStart, graceEnd := cursor.region.GraceWindow()
			if editPos >= graceStart && editPos <= graceEnd {
				return true, nil
			}
			// Edit outside grace window - dissolve and proceed to tree
			if err := g.dissolveRegionUnlocked(cursor); err != nil {
				return false, err
			}
		}
		return false, nil
	}

	// Human cursor - auto-create/manage regions
	if cursor.region != nil {
		graceStart, graceEnd := cursor.region.GraceWindow()
		if editPos >= graceStart && editPos <= graceEnd {
			// Edit is within grace window - use region
			return true, nil
		}
		// Edit outside grace window - dissolve old region, create new one
		if err := g.dissolveRegionUnlocked(cursor); err != nil {
			return false, err
		}
	}

	// Create new region centered on edit position
	totalBytes := g.calculateTotalBytesUnlocked()
	startByte := editPos - g.graceWindowSize/2
	endByte := editPos + g.graceWindowSize/2
	if startByte < 0 {
		startByte = 0
	}
	if endByte > totalBytes {
		endByte = totalBytes
	}

	if err := g.createRegionForCursorUnlocked(cursor, startByte, endByte); err != nil {
		return false, err
	}

	return true, nil
}

// flushRegionToTree flushes a region's content to tree without creating a revision.
// Called when region hits max size. Creates a new region to continue editing.
func (g *Garland) flushRegionToTree(cursor *Cursor) error {
	if cursor.region == nil {
		return nil
	}

	handle := cursor.region

	// Remember where the region was
	_, _ = handle.ContentBounds() // contentStart not needed, just graceWindow
	graceStart, graceEnd := handle.GraceWindow()

	// Dissolve the region (moves content to tree)
	if err := g.dissolveRegionUnlocked(cursor); err != nil {
		return err
	}

	// Create a new region with the same grace window
	// (The content bounds will be recalculated based on current tree state)
	return g.createRegionForCursorUnlocked(cursor, graceStart, graceEnd)
}

// insertIntoRegion handles insertion into a cursor's optimized region.
func (g *Garland) insertIntoRegion(cursor *Cursor, docPos int64, data []byte, insertBefore bool) error {
	handle := cursor.region

	// Convert document position to region-relative position
	contentStart, _ := handle.ContentBounds()
	regionPos := docPos - contentStart

	// Check if we need to flush due to max size
	if handle.buffer.ByteCount()+int64(len(data)) > g.maxLeafSize {
		if err := g.flushRegionToTree(cursor); err != nil {
			return err
		}
		// After flush, we have a new region - recalculate position
		handle = cursor.region
		contentStart, _ = handle.ContentBounds()
		regionPos = docPos - contentStart
	}

	// Insert into the buffer
	if err := handle.buffer.InsertBytes(regionPos, data); err != nil {
		return err
	}

	// Adjust decorations
	handle.adjustDecorationsForInsert(regionPos, int64(len(data)), insertBefore)

	return nil
}

// deleteFromRegion handles deletion from a cursor's optimized region.
func (g *Garland) deleteFromRegion(cursor *Cursor, docPos int64, length int64) ([]Decoration, error) {
	handle := cursor.region

	// Convert document position to region-relative position
	contentStart, contentEnd := handle.ContentBounds()
	regionPos := docPos - contentStart

	// Validate the deletion is within region bounds
	if regionPos < 0 || regionPos+length > contentEnd-contentStart {
		return nil, ErrInvalidPosition
	}

	// Delete from the buffer
	if err := handle.buffer.DeleteBytes(regionPos, length); err != nil {
		return nil, err
	}

	// Adjust decorations and collect removed ones
	removed := handle.adjustDecorationsForDelete(regionPos, length)

	return removed, nil
}
