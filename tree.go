package garland

import "unicode/utf8"

// LeafSearchResult contains information about a leaf node found during tree traversal.
type LeafSearchResult struct {
	Node          *Node         // the leaf node
	Snapshot      *NodeSnapshot // the node's snapshot at current version
	ByteOffset    int64         // byte offset from start of this leaf to target
	RuneOffset    int64         // rune offset from start of this leaf to target
	LeafByteStart int64         // absolute byte position where this leaf starts
	LeafRuneStart int64         // absolute rune position where this leaf starts
}

// findLeafByByte navigates the tree to find the leaf containing the given byte position.
// Returns the leaf node and the offset within that leaf.
// This version acquires a read lock - use findLeafByByteUnlocked if already holding a lock.
func (g *Garland) findLeafByByte(pos int64) (*LeafSearchResult, error) {
	if pos < 0 {
		return nil, ErrInvalidPosition
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	return g.findLeafByByteUnlocked(pos)
}

// findLeafByByteUnlocked is the internal version that assumes caller holds a lock.
func (g *Garland) findLeafByByteUnlocked(pos int64) (*LeafSearchResult, error) {
	if pos < 0 {
		return nil, ErrInvalidPosition
	}

	if g.root == nil {
		return nil, ErrInvalidPosition
	}

	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return nil, ErrInvalidPosition
	}

	// Allow pos == total bytes (EOF position)
	if pos > rootSnap.byteCount {
		return nil, ErrInvalidPosition
	}

	return g.findLeafByByteInternal(g.root, rootSnap, pos, 0, 0)
}

// findLeafByByteInternal is the recursive implementation of findLeafByByte.
func (g *Garland) findLeafByByteInternal(node *Node, snap *NodeSnapshot, pos int64, byteStart int64, runeStart int64) (*LeafSearchResult, error) {
	if snap.isLeaf {
		return &LeafSearchResult{
			Node:          node,
			Snapshot:      snap,
			ByteOffset:    pos,
			RuneOffset:    byteToRuneOffset(snap.data, pos),
			LeafByteStart: byteStart,
			LeafRuneStart: runeStart,
		}, nil
	}

	// Internal node: determine which child to descend into
	leftNode := g.nodeRegistry[snap.leftID]
	if leftNode == nil {
		return nil, ErrInvalidPosition
	}

	leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)
	if leftSnap == nil {
		return nil, ErrInvalidPosition
	}

	// Use < so that when pos equals left's byte count, we go to right subtree
	// This ensures proper leaf boundary handling when reading across leaves
	if pos < leftSnap.byteCount {
		// Target is in left subtree
		return g.findLeafByByteInternal(leftNode, leftSnap, pos, byteStart, runeStart)
	}

	// Target is in right subtree
	rightNode := g.nodeRegistry[snap.rightID]
	if rightNode == nil {
		return nil, ErrInvalidPosition
	}

	rightSnap := rightNode.snapshotAt(g.currentFork, g.currentRevision)
	if rightSnap == nil {
		return nil, ErrInvalidPosition
	}

	return g.findLeafByByteInternal(
		rightNode,
		rightSnap,
		pos-leftSnap.byteCount,
		byteStart+leftSnap.byteCount,
		runeStart+leftSnap.runeCount,
	)
}

// findLeafByRune navigates the tree to find the leaf containing the given rune position.
func (g *Garland) findLeafByRune(pos int64) (*LeafSearchResult, error) {
	if pos < 0 {
		return nil, ErrInvalidPosition
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	return g.findLeafByRuneUnlocked(pos)
}

// findLeafByRuneUnlocked is the internal version that assumes caller holds a lock.
func (g *Garland) findLeafByRuneUnlocked(pos int64) (*LeafSearchResult, error) {
	if pos < 0 {
		return nil, ErrInvalidPosition
	}

	if g.root == nil {
		return nil, ErrInvalidPosition
	}

	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return nil, ErrInvalidPosition
	}

	// Allow pos == total runes (EOF position)
	if pos > rootSnap.runeCount {
		return nil, ErrInvalidPosition
	}

	return g.findLeafByRuneInternal(g.root, rootSnap, pos, 0, 0)
}

// findLeafByRuneInternal is the recursive implementation of findLeafByRune.
func (g *Garland) findLeafByRuneInternal(node *Node, snap *NodeSnapshot, pos int64, byteStart int64, runeStart int64) (*LeafSearchResult, error) {
	if snap.isLeaf {
		byteOffset := runeToByteOffset(snap.data, pos)
		return &LeafSearchResult{
			Node:          node,
			Snapshot:      snap,
			ByteOffset:    byteOffset,
			RuneOffset:    pos,
			LeafByteStart: byteStart,
			LeafRuneStart: runeStart,
		}, nil
	}

	// Internal node: determine which child to descend into
	leftNode := g.nodeRegistry[snap.leftID]
	if leftNode == nil {
		return nil, ErrInvalidPosition
	}

	leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)
	if leftSnap == nil {
		return nil, ErrInvalidPosition
	}

	// Use < so that when pos equals left's rune count, we go to right subtree
	if pos < leftSnap.runeCount {
		// Target is in left subtree
		return g.findLeafByRuneInternal(leftNode, leftSnap, pos, byteStart, runeStart)
	}

	// Target is in right subtree
	rightNode := g.nodeRegistry[snap.rightID]
	if rightNode == nil {
		return nil, ErrInvalidPosition
	}

	rightSnap := rightNode.snapshotAt(g.currentFork, g.currentRevision)
	if rightSnap == nil {
		return nil, ErrInvalidPosition
	}

	return g.findLeafByRuneInternal(
		rightNode,
		rightSnap,
		pos-leftSnap.runeCount,
		byteStart+leftSnap.byteCount,
		runeStart+leftSnap.runeCount,
	)
}

// LineSearchResult contains information about a line found during tree traversal.
type LineSearchResult struct {
	LeafResult    *LeafSearchResult
	LineByteStart int64 // absolute byte position where target line starts
	LineRuneStart int64 // absolute rune position where target line starts
}

// findLeafByLine navigates the tree to find the leaf containing the given line:rune position.
func (g *Garland) findLeafByLine(line, runeInLine int64) (*LineSearchResult, error) {
	if line < 0 || runeInLine < 0 {
		return nil, ErrInvalidPosition
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	return g.findLeafByLineUnlocked(line, runeInLine)
}

// findLeafByLineUnlocked is the internal version that assumes caller holds a lock.
func (g *Garland) findLeafByLineUnlocked(line, runeInLine int64) (*LineSearchResult, error) {
	if line < 0 || runeInLine < 0 {
		return nil, ErrInvalidPosition
	}

	if g.root == nil {
		return nil, ErrInvalidPosition
	}

	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return nil, ErrInvalidPosition
	}

	// Line 0 is always valid (even in empty file)
	// Other lines require that many newlines
	if line > 0 && line > rootSnap.lineCount {
		return nil, ErrInvalidPosition
	}

	return g.findLeafByLineInternal(g.root, rootSnap, line, runeInLine, 0, 0, 0)
}

// findLeafByLineInternal is the recursive implementation of findLeafByLine.
func (g *Garland) findLeafByLineInternal(
	node *Node,
	snap *NodeSnapshot,
	targetLine int64,
	runeInLine int64,
	byteStart int64,
	runeStart int64,
	lineStart int64,
) (*LineSearchResult, error) {
	if snap.isLeaf {
		// Find the line within this leaf
		relLine := targetLine - lineStart
		if relLine < 0 {
			return nil, ErrInvalidPosition
		}

		// Find the byte/rune offset for this line within the leaf
		var byteOffset, runeOffset int64
		if relLine == 0 {
			// Line starts at beginning of this leaf's contribution
			byteOffset = 0
			runeOffset = 0
		} else {
			// Find the line start from lineStarts
			if int(relLine) >= len(snap.lineStarts) {
				return nil, ErrInvalidPosition
			}
			byteOffset = snap.lineStarts[relLine].ByteOffset
			runeOffset = snap.lineStarts[relLine].RuneOffset
		}

		// Add runeInLine offset (need to convert to bytes)
		finalRuneOffset := runeOffset + runeInLine
		finalByteOffset := runeToByteOffset(snap.data, finalRuneOffset)

		// Validate the position is within this leaf
		if finalByteOffset > int64(len(snap.data)) {
			return nil, ErrInvalidPosition
		}

		return &LineSearchResult{
			LeafResult: &LeafSearchResult{
				Node:          node,
				Snapshot:      snap,
				ByteOffset:    finalByteOffset,
				RuneOffset:    finalRuneOffset,
				LeafByteStart: byteStart,
				LeafRuneStart: runeStart,
			},
			LineByteStart: byteStart + byteOffset,
			LineRuneStart: runeStart + runeOffset,
		}, nil
	}

	// Internal node: determine which child to descend into
	leftNode := g.nodeRegistry[snap.leftID]
	if leftNode == nil {
		return nil, ErrInvalidPosition
	}

	leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)
	if leftSnap == nil {
		return nil, ErrInvalidPosition
	}

	// Calculate how many complete lines are in left subtree
	leftLines := leftSnap.lineCount

	// The target line relative to start
	relTargetLine := targetLine - lineStart

	// Use <= because if left has N newlines, line N starts in the left subtree
	// (after the Nth newline) even though the line might extend into the right subtree
	if relTargetLine <= leftLines {
		// Target line starts within left subtree
		return g.findLeafByLineInternal(
			leftNode,
			leftSnap,
			targetLine,
			runeInLine,
			byteStart,
			runeStart,
			lineStart,
		)
	}

	// Target line is in right subtree (or spans the boundary, but we go right)
	rightNode := g.nodeRegistry[snap.rightID]
	if rightNode == nil {
		return nil, ErrInvalidPosition
	}

	rightSnap := rightNode.snapshotAt(g.currentFork, g.currentRevision)
	if rightSnap == nil {
		return nil, ErrInvalidPosition
	}

	return g.findLeafByLineInternal(
		rightNode,
		rightSnap,
		targetLine,
		runeInLine,
		byteStart+leftSnap.byteCount,
		runeStart+leftSnap.runeCount,
		lineStart+leftSnap.lineCount,
	)
}

// splitLeaf splits a leaf node at the given byte position.
// Returns IDs of two new leaf nodes (left, right).
// The original node is not modified (copy-on-write).
func (g *Garland) splitLeaf(node *Node, snap *NodeSnapshot, bytePos int64) (NodeID, NodeID, error) {
	if !snap.isLeaf {
		return 0, 0, ErrNotALeaf
	}

	if bytePos < 0 || bytePos > snap.byteCount {
		return 0, 0, ErrInvalidPosition
	}

	// Ensure we don't split in the middle of a UTF-8 character
	splitPos := alignToRuneBoundary(snap.data, bytePos)

	// Partition data
	leftData := snap.data[:splitPos]
	rightData := snap.data[splitPos:]

	// Partition decorations
	leftDecs, rightDecs := partitionDecorations(snap.decorations, splitPos)

	// Create left leaf
	g.nextNodeID++
	leftNode := newNode(g.nextNodeID, g)
	g.nodeRegistry[leftNode.id] = leftNode

	// Determine original offset for left leaf
	leftOrigOffset := snap.originalFileOffset
	leftSnap := createLeafSnapshot(leftData, leftDecs, leftOrigOffset)
	leftNode.setSnapshot(g.currentFork, g.currentRevision, leftSnap)

	// Create right leaf
	g.nextNodeID++
	rightNode := newNode(g.nextNodeID, g)
	g.nodeRegistry[rightNode.id] = rightNode

	// Determine original offset for right leaf
	rightOrigOffset := int64(-1)
	if snap.originalFileOffset >= 0 {
		rightOrigOffset = snap.originalFileOffset + splitPos
	}
	rightSnap := createLeafSnapshot(rightData, rightDecs, rightOrigOffset)
	rightNode.setSnapshot(g.currentFork, g.currentRevision, rightSnap)

	return leftNode.id, rightNode.id, nil
}

// concatenate creates or reuses an internal node joining two subtrees.
// If a node with the same (leftID, rightID) structure exists, reuses it
// and adds a new snapshot. Otherwise creates a new node.
// Returns the ID of the internal node.
func (g *Garland) concatenate(leftID, rightID NodeID) (NodeID, error) {
	leftNode := g.nodeRegistry[leftID]
	rightNode := g.nodeRegistry[rightID]

	if leftNode == nil || rightNode == nil {
		return 0, ErrInvalidPosition
	}

	leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)
	rightSnap := rightNode.snapshotAt(g.currentFork, g.currentRevision)

	if leftSnap == nil || rightSnap == nil {
		return 0, ErrInvalidPosition
	}

	// Create the new snapshot for this version
	internalSnap := createInternalSnapshot(leftID, rightID, leftSnap, rightSnap)

	// Check if we already have an internal node with this structure
	key := [2]NodeID{leftID, rightID}
	if existingID, ok := g.internalNodesByChildren[key]; ok {
		// Reuse existing node - just add a new snapshot
		existingNode := g.nodeRegistry[existingID]
		if existingNode != nil {
			existingNode.setSnapshot(g.currentFork, g.currentRevision, internalSnap)
			return existingID, nil
		}
	}

	// Create new internal node
	g.nextNodeID++
	internalNode := newNode(g.nextNodeID, g)
	g.nodeRegistry[internalNode.id] = internalNode
	g.internalNodesByChildren[key] = internalNode.id

	internalNode.setSnapshot(g.currentFork, g.currentRevision, internalSnap)

	return internalNode.id, nil
}

// insertInternal recursively navigates to the insertion point and rebuilds the tree.
// This properly preserves the entire tree structure including siblings like EOF nodes.
func (g *Garland) insertInternal(
	node *Node,
	snap *NodeSnapshot,
	insertPos int64,
	offset int64,
	data []byte,
	decorations []RelativeDecoration,
	insertBefore bool,
) (NodeID, error) {
	if snap.isLeaf {
		// We've found the leaf to insert into
		localPos := insertPos - offset
		return g.insertIntoLeaf(snap, localPos, data, decorations, insertBefore)
	}

	// Internal node: determine which child contains the insertion point
	leftNode := g.nodeRegistry[snap.leftID]
	if leftNode == nil {
		return 0, ErrInvalidPosition
	}

	leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)
	if leftSnap == nil {
		return 0, ErrInvalidPosition
	}

	leftEnd := offset + leftSnap.byteCount

	// Use < so insertion at exact boundary goes to right subtree (start of next node)
	if insertPos < leftEnd {
		// Insert into left subtree
		newLeftID, err := g.insertInternal(leftNode, leftSnap, insertPos, offset, data, decorations, insertBefore)
		if err != nil {
			return 0, err
		}

		// Rebuild this internal node with new left child, keeping right child
		return g.concatenate(newLeftID, snap.rightID)
	}

	// Insert into right subtree
	rightNode := g.nodeRegistry[snap.rightID]
	if rightNode == nil {
		return 0, ErrInvalidPosition
	}

	rightSnap := rightNode.snapshotAt(g.currentFork, g.currentRevision)
	if rightSnap == nil {
		return 0, ErrInvalidPosition
	}

	newRightID, err := g.insertInternal(rightNode, rightSnap, insertPos, leftEnd, data, decorations, insertBefore)
	if err != nil {
		return 0, err
	}

	// Rebuild this internal node with new right child, keeping left child
	return g.concatenate(snap.leftID, newRightID)
}

// insertIntoLeaf handles insertion within a leaf node.
// Returns the ID of the new subtree (which may be a single leaf or internal nodes).
func (g *Garland) insertIntoLeaf(
	snap *NodeSnapshot,
	localPos int64,
	data []byte,
	decorations []RelativeDecoration,
	insertBefore bool,
) (NodeID, error) {
	// Convert relative decorations to absolute (within new data)
	absoluteDecs := make([]Decoration, len(decorations))
	for i, rd := range decorations {
		absoluteDecs[i] = Decoration{
			Key:      rd.Key,
			Position: rd.Position,
		}
	}

	// Split at insertion point
	splitPos := localPos
	if splitPos < 0 {
		splitPos = 0
	}
	if splitPos > int64(len(snap.data)) {
		splitPos = int64(len(snap.data))
	}

	leftData := snap.data[:splitPos]
	rightData := snap.data[splitPos:]

	// Partition existing decorations
	leftDecs, rightDecs := partitionDecorations(snap.decorations, splitPos)

	// Adjust right decorations for insertion - but only if there's actual right data.
	// For EOF nodes (empty right data), decorations stay at position 0 (the new EOF).
	if len(rightData) > 0 {
		insertLen := int64(len(data))
		for i := range rightDecs {
			rightDecs[i].Position += insertLen
		}
	}

	// Create new left leaf (original content before insertion point)
	var leftID NodeID
	if len(leftData) > 0 || len(leftDecs) > 0 {
		g.nextNodeID++
		leftNode := newNode(g.nextNodeID, g)
		g.nodeRegistry[leftNode.id] = leftNode
		leftSnap := createLeafSnapshot(leftData, leftDecs, -1)
		leftNode.setSnapshot(g.currentFork, g.currentRevision, leftSnap)
		leftID = leftNode.id
	}

	// Create new middle leaf (inserted content)
	g.nextNodeID++
	middleNode := newNode(g.nextNodeID, g)
	g.nodeRegistry[middleNode.id] = middleNode
	middleSnap := createLeafSnapshot(data, absoluteDecs, -1)
	middleNode.setSnapshot(g.currentFork, g.currentRevision, middleSnap)
	middleID := middleNode.id

	// Create new right leaf (original content after insertion point)
	var rightID NodeID
	if len(rightData) > 0 || len(rightDecs) > 0 {
		g.nextNodeID++
		rightNode := newNode(g.nextNodeID, g)
		g.nodeRegistry[rightNode.id] = rightNode
		rightSnap := createLeafSnapshot(rightData, rightDecs, -1)
		rightNode.setSnapshot(g.currentFork, g.currentRevision, rightSnap)
		rightID = rightNode.id
	}

	// Build the result subtree
	var resultID NodeID
	var err error

	if leftID == 0 && rightID == 0 {
		// Just the inserted content
		resultID = middleID
	} else if leftID == 0 {
		// middle + right
		resultID, err = g.concatenate(middleID, rightID)
	} else if rightID == 0 {
		// left + middle
		resultID, err = g.concatenate(leftID, middleID)
	} else {
		// left + middle + right
		leftMiddleID, err := g.concatenate(leftID, middleID)
		if err != nil {
			return 0, err
		}
		resultID, err = g.concatenate(leftMiddleID, rightID)
	}

	if err != nil {
		return 0, err
	}

	return resultID, nil
}

// deleteRange deletes bytes from startPos to startPos+length.
// Returns decorations from the deleted range and the new subtree root ID.
func (g *Garland) deleteRange(startPos, length int64) ([]Decoration, NodeID, error) {
	if length <= 0 {
		return nil, g.root.id, nil
	}

	endPos := startPos + length

	// Collect decorations from the deleted range
	var deletedDecs []Decoration

	// Build new tree excluding the deleted range
	newRootID, err := g.deleteRangeInternal(g.root, g.root.snapshotAt(g.currentFork, g.currentRevision),
		startPos, endPos, 0, &deletedDecs)
	if err != nil {
		return nil, 0, err
	}

	return deletedDecs, newRootID, nil
}

// deleteRangeInternal recursively rebuilds the tree excluding the deleted range.
func (g *Garland) deleteRangeInternal(
	node *Node,
	snap *NodeSnapshot,
	deleteStart, deleteEnd int64,
	offset int64,
	deletedDecs *[]Decoration,
) (NodeID, error) {
	nodeStart := offset
	nodeEnd := offset + snap.byteCount

	// No overlap with this node
	if deleteEnd <= nodeStart || deleteStart >= nodeEnd {
		return node.id, nil
	}

	if snap.isLeaf {
		// Calculate local delete range
		localStart := deleteStart - nodeStart
		if localStart < 0 {
			localStart = 0
		}
		localEnd := deleteEnd - nodeStart
		if localEnd > snap.byteCount {
			localEnd = snap.byteCount
		}

		// Collect decorations from deleted range
		for _, d := range snap.decorations {
			if d.Position >= localStart && d.Position < localEnd {
				*deletedDecs = append(*deletedDecs, Decoration{
					Key:      d.Key,
					Position: d.Position + nodeStart, // absolute position
				})
			}
		}

		// Build new data excluding deleted range
		var newData []byte
		if localStart > 0 {
			newData = append(newData, snap.data[:localStart]...)
		}
		if localEnd < snap.byteCount {
			newData = append(newData, snap.data[localEnd:]...)
		}

		// Build new decorations (adjust positions)
		var newDecs []Decoration
		for _, d := range snap.decorations {
			if d.Position < localStart {
				newDecs = append(newDecs, d)
			} else if d.Position >= localEnd {
				newDecs = append(newDecs, Decoration{
					Key:      d.Key,
					Position: d.Position - (localEnd - localStart),
				})
			}
		}

		// Create new leaf
		g.nextNodeID++
		newNode := newNode(g.nextNodeID, g)
		g.nodeRegistry[newNode.id] = newNode
		newSnap := createLeafSnapshot(newData, newDecs, -1)
		newNode.setSnapshot(g.currentFork, g.currentRevision, newSnap)

		return newNode.id, nil
	}

	// Internal node
	leftNode := g.nodeRegistry[snap.leftID]
	rightNode := g.nodeRegistry[snap.rightID]

	leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)
	rightSnap := rightNode.snapshotAt(g.currentFork, g.currentRevision)

	leftEnd := nodeStart + leftSnap.byteCount

	// Recursively process children
	newLeftID, err := g.deleteRangeInternal(leftNode, leftSnap, deleteStart, deleteEnd, nodeStart, deletedDecs)
	if err != nil {
		return 0, err
	}

	newRightID, err := g.deleteRangeInternal(rightNode, rightSnap, deleteStart, deleteEnd, leftEnd, deletedDecs)
	if err != nil {
		return 0, err
	}

	// Get new snapshots
	newLeftSnap := g.nodeRegistry[newLeftID].snapshotAt(g.currentFork, g.currentRevision)
	newRightSnap := g.nodeRegistry[newRightID].snapshotAt(g.currentFork, g.currentRevision)

	// Handle empty children
	if newLeftSnap.byteCount == 0 && len(newLeftSnap.decorations) == 0 {
		return newRightID, nil
	}
	if newRightSnap.byteCount == 0 && len(newRightSnap.decorations) == 0 {
		return newLeftID, nil
	}

	// Create new internal node
	return g.concatenate(newLeftID, newRightID)
}

// Helper functions for byte/rune conversion within a data slice

// byteToRuneOffset converts a byte offset to a rune offset within data.
func byteToRuneOffset(data []byte, byteOffset int64) int64 {
	if byteOffset <= 0 {
		return 0
	}
	if byteOffset >= int64(len(data)) {
		return int64(utf8.RuneCount(data))
	}
	return int64(utf8.RuneCount(data[:byteOffset]))
}

// runeToByteOffset converts a rune offset to a byte offset within data.
func runeToByteOffset(data []byte, runeOffset int64) int64 {
	if runeOffset <= 0 {
		return 0
	}

	var bytePos int64 = 0
	var runeCount int64 = 0

	for bytePos < int64(len(data)) && runeCount < runeOffset {
		_, size := utf8.DecodeRune(data[bytePos:])
		bytePos += int64(size)
		runeCount++
	}

	return bytePos
}

// alignToRuneBoundary adjusts a byte position to not split a UTF-8 character.
// Returns the nearest valid byte position (same or earlier).
func alignToRuneBoundary(data []byte, pos int64) int64 {
	if pos <= 0 || pos >= int64(len(data)) {
		return pos
	}

	// Check if we're at a valid rune start
	if utf8.RuneStart(data[pos]) {
		return pos
	}

	// Walk back to find the start of this rune
	for pos > 0 && !utf8.RuneStart(data[pos]) {
		pos--
	}
	return pos
}

// getHeight returns the height of a subtree (for balancing).
func (g *Garland) getHeight(nodeID NodeID) int {
	if nodeID == 0 {
		return 0
	}

	node := g.nodeRegistry[nodeID]
	if node == nil {
		return 0
	}

	snap := node.snapshotAt(g.currentFork, g.currentRevision)
	if snap == nil || snap.isLeaf {
		return 1
	}

	leftHeight := g.getHeight(snap.leftID)
	rightHeight := g.getHeight(snap.rightID)

	if leftHeight > rightHeight {
		return leftHeight + 1
	}
	return rightHeight + 1
}

// rebalanceIfNeeded checks if a subtree needs rebalancing and performs it.
// Returns the (possibly new) root of the balanced subtree.
func (g *Garland) rebalanceIfNeeded(nodeID NodeID) NodeID {
	node := g.nodeRegistry[nodeID]
	if node == nil {
		return nodeID
	}

	snap := node.snapshotAt(g.currentFork, g.currentRevision)
	if snap == nil || snap.isLeaf {
		return nodeID
	}

	leftHeight := g.getHeight(snap.leftID)
	rightHeight := g.getHeight(snap.rightID)

	balance := leftHeight - rightHeight

	// Check if balance factor exceeds threshold (using 2 for basic rope)
	if balance > 2 {
		// Left-heavy: rotate right
		return g.rotateRight(nodeID)
	} else if balance < -2 {
		// Right-heavy: rotate left
		return g.rotateLeft(nodeID)
	}

	return nodeID
}

// rotateRight performs a right rotation.
func (g *Garland) rotateRight(nodeID NodeID) NodeID {
	node := g.nodeRegistry[nodeID]
	snap := node.snapshotAt(g.currentFork, g.currentRevision)

	if snap.isLeaf {
		return nodeID
	}

	leftNode := g.nodeRegistry[snap.leftID]
	leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)

	if leftSnap.isLeaf {
		return nodeID
	}

	// Left's right child becomes node's new left child
	// Left becomes new parent
	newRightID, _ := g.concatenate(leftSnap.rightID, snap.rightID)
	newRootID, _ := g.concatenate(leftSnap.leftID, newRightID)

	return newRootID
}

// rotateLeft performs a left rotation.
func (g *Garland) rotateLeft(nodeID NodeID) NodeID {
	node := g.nodeRegistry[nodeID]
	snap := node.snapshotAt(g.currentFork, g.currentRevision)

	if snap.isLeaf {
		return nodeID
	}

	rightNode := g.nodeRegistry[snap.rightID]
	rightSnap := rightNode.snapshotAt(g.currentFork, g.currentRevision)

	if rightSnap.isLeaf {
		return nodeID
	}

	// Right's left child becomes node's new right child
	// Right becomes new parent
	newLeftID, _ := g.concatenate(snap.leftID, rightSnap.leftID)
	newRootID, _ := g.concatenate(newLeftID, rightSnap.rightID)

	return newRootID
}

// collectLeaves traverses the tree and collects all leaf data in order.
func (g *Garland) collectLeaves(nodeID NodeID) []byte {
	node := g.nodeRegistry[nodeID]
	if node == nil {
		return nil
	}

	snap := node.snapshotAt(g.currentFork, g.currentRevision)
	if snap == nil {
		return nil
	}

	if snap.isLeaf {
		return snap.data
	}

	leftData := g.collectLeaves(snap.leftID)
	rightData := g.collectLeaves(snap.rightID)

	result := make([]byte, 0, len(leftData)+len(rightData))
	result = append(result, leftData...)
	result = append(result, rightData...)
	return result
}
