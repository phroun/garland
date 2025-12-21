package garland

import (
	"sort"
	"time"
)

// MemoryStats contains current memory usage statistics.
type MemoryStats struct {
	MemoryBytes      int64 // bytes of in-memory leaf data
	SoftLimit        int64 // configured soft limit (0 = disabled)
	HardLimit        int64 // configured hard limit (0 = disabled)
	InMemoryLeaves   int   // count of leaves with data in memory
	ColdStoredLeaves int   // count of leaves with data in cold storage
	WarmStoredLeaves int   // count of leaves with data in warm storage
	UnderPressure    bool  // true if hard limit exceeded and can't reduce
}

// MaintenanceStats contains statistics from a maintenance run.
type MaintenanceStats struct {
	NodesChilled       int   // number of nodes moved to cold storage
	BytesChilled       int64 // bytes moved to cold storage
	RotationsPerformed int   // number of tree rotations performed
}

// MemoryUsage returns current memory statistics for this Garland.
func (g *Garland) MemoryUsage() MemoryStats {
	g.mu.RLock()
	defer g.mu.RUnlock()

	stats := MemoryStats{
		MemoryBytes: g.memoryBytes,
	}
	if g.lib != nil {
		stats.SoftLimit = g.lib.memorySoftLimit
		stats.HardLimit = g.lib.memoryHardLimit
		g.lib.mu.RLock()
		stats.UnderPressure = g.lib.memoryPressure
		g.lib.mu.RUnlock()
	}

	// Count leaves by storage state
	for _, node := range g.nodeRegistry {
		snap := node.snapshotAt(g.currentFork, g.currentRevision)
		if snap == nil || !snap.isLeaf {
			continue
		}
		switch snap.storageState {
		case StorageMemory:
			stats.InMemoryLeaves++
		case StorageCold:
			stats.ColdStoredLeaves++
		case StorageWarm:
			stats.WarmStoredLeaves++
		}
	}

	return stats
}

// TotalMemoryUsage returns the total memory usage across all Garlands in the library.
func (lib *Library) TotalMemoryUsage() int64 {
	lib.mu.RLock()
	defer lib.mu.RUnlock()

	var total int64
	for _, g := range lib.activeGarlands {
		g.mu.RLock()
		total += g.memoryBytes
		g.mu.RUnlock()
	}
	return total
}

// lruCandidate represents a node that could be chilled, with its access time.
type lruCandidate struct {
	garland    *Garland
	nodeID     NodeID
	forkRev    ForkRevision
	snap       *NodeSnapshot
	accessTime time.Time
	bytes      int64
}

// collectLRUCandidates finds all in-memory leaves that could be chilled,
// sorted by last access time (oldest first).
func (lib *Library) collectLRUCandidates() []lruCandidate {
	lib.mu.RLock()
	defer lib.mu.RUnlock()

	var candidates []lruCandidate

	for _, g := range lib.activeGarlands {
		g.mu.RLock()
		// Skip memory-only garlands
		if g.loadingStyle == MemoryOnly {
			g.mu.RUnlock()
			continue
		}

		for _, node := range g.nodeRegistry {
			for forkRev, snap := range node.history {
				if snap.isLeaf && snap.storageState == StorageMemory && len(snap.data) > 0 {
					candidates = append(candidates, lruCandidate{
						garland:    g,
						nodeID:     node.id,
						forkRev:    forkRev,
						snap:       snap,
						accessTime: snap.lastAccessTime,
						bytes:      int64(len(snap.data)),
					})
				}
			}
		}
		g.mu.RUnlock()
	}

	// Sort by access time (oldest first - zero time sorts first)
	sort.Slice(candidates, func(i, j int) bool {
		// Zero time (never accessed) should come first
		if candidates[i].accessTime.IsZero() && !candidates[j].accessTime.IsZero() {
			return true
		}
		if !candidates[i].accessTime.IsZero() && candidates[j].accessTime.IsZero() {
			return false
		}
		return candidates[i].accessTime.Before(candidates[j].accessTime)
	})

	return candidates
}

// IncrementalChill performs budgeted LRU-based chilling across all Garlands.
// It chills at most `budget` nodes, prioritizing least-recently-used.
// Returns the number of nodes chilled and bytes freed.
func (lib *Library) IncrementalChill(budget int) MaintenanceStats {
	if lib.coldStorageBackend == nil {
		return MaintenanceStats{}
	}

	candidates := lib.collectLRUCandidates()
	if len(candidates) == 0 {
		return MaintenanceStats{}
	}

	stats := MaintenanceStats{}

	for i := 0; i < len(candidates) && stats.NodesChilled < budget; i++ {
		c := candidates[i]

		// Lock the specific garland
		c.garland.mu.Lock()

		// Verify the snapshot is still valid and in memory
		node := c.garland.nodeRegistry[c.nodeID]
		if node == nil {
			c.garland.mu.Unlock()
			continue
		}
		snap, ok := node.history[c.forkRev]
		if !ok || snap.storageState != StorageMemory || len(snap.data) == 0 {
			c.garland.mu.Unlock()
			continue
		}

		// Chill it using trust-aware eviction
		err := c.garland.chillSnapshotWithTrust(c.nodeID, c.forkRev, snap)
		if err == nil {
			stats.NodesChilled++
			stats.BytesChilled += c.bytes
		}

		c.garland.mu.Unlock()
	}

	return stats
}

// ChillToTarget performs incremental chilling until memory is below the soft limit.
// It respects the budget per tick and returns when either:
// - Memory is below soft limit
// - No more candidates to chill
// - Budget exhausted for this tick
func (lib *Library) ChillToTarget() MaintenanceStats {
	if lib.memorySoftLimit <= 0 {
		return MaintenanceStats{}
	}

	totalStats := MaintenanceStats{}

	for {
		currentUsage := lib.TotalMemoryUsage()
		if currentUsage <= lib.memorySoftLimit {
			break
		}

		stats := lib.IncrementalChill(lib.chillBudgetPerTick)
		if stats.NodesChilled == 0 {
			// No more candidates
			break
		}

		totalStats.NodesChilled += stats.NodesChilled
		totalStats.BytesChilled += stats.BytesChilled

		// Only do one budget's worth per call to keep it incremental
		break
	}

	return totalStats
}

// startMaintenanceWorker starts the background maintenance goroutine.
func (lib *Library) startMaintenanceWorker() {
	lib.maintenanceStop = make(chan struct{})
	lib.maintenanceWg.Add(1)

	go func() {
		defer lib.maintenanceWg.Done()

		ticker := time.NewTicker(lib.backgroundInterval)
		defer ticker.Stop()

		for {
			select {
			case <-lib.maintenanceStop:
				return
			case <-ticker.C:
				lib.runMaintenanceTick()
			}
		}
	}()
}

// StopMaintenance stops the background maintenance worker.
func (lib *Library) StopMaintenance() {
	if lib.maintenanceStop != nil {
		close(lib.maintenanceStop)
		lib.maintenanceWg.Wait()
		lib.maintenanceStop = nil
	}
}

// runMaintenanceTick performs one tick of background maintenance.
func (lib *Library) runMaintenanceTick() {
	// Check memory pressure and chill if needed
	if lib.memorySoftLimit > 0 {
		currentUsage := lib.TotalMemoryUsage()
		if currentUsage > lib.memorySoftLimit {
			lib.IncrementalChill(lib.chillBudgetPerTick)
		}
	}

	// TODO: Add incremental rebalancing here
}

// CheckMemoryPressure checks if memory limits are exceeded and performs
// appropriate maintenance. Called after mutations.
// Sets memoryPressure flag if hard limit exceeded and can't be reduced.
func (g *Garland) CheckMemoryPressure() MaintenanceStats {
	if g.lib == nil {
		return MaintenanceStats{}
	}

	stats := MaintenanceStats{}

	// Check hard limit first (immediate action needed)
	if g.lib.memoryHardLimit > 0 {
		currentUsage := g.lib.TotalMemoryUsage()
		if currentUsage > g.lib.memoryHardLimit {
			// Do multiple rounds until under limit or no progress
			for currentUsage > g.lib.memoryHardLimit {
				s := g.lib.IncrementalChill(g.lib.chillBudgetPerTick)
				if s.NodesChilled == 0 {
					// Can't reduce memory - set pressure flag
					g.lib.mu.Lock()
					g.lib.memoryPressure = true
					g.lib.mu.Unlock()
					break
				}
				stats.NodesChilled += s.NodesChilled
				stats.BytesChilled += s.BytesChilled
				currentUsage = g.lib.TotalMemoryUsage()
			}

			// Clear pressure flag if we got under the limit
			if currentUsage <= g.lib.memoryHardLimit {
				g.lib.mu.Lock()
				g.lib.memoryPressure = false
				g.lib.mu.Unlock()
			}
		} else {
			// Under hard limit - clear pressure flag
			g.lib.mu.Lock()
			g.lib.memoryPressure = false
			g.lib.mu.Unlock()
		}
	}

	// Check soft limit (opportunistic action)
	if g.lib.memorySoftLimit > 0 && stats.NodesChilled == 0 {
		currentUsage := g.lib.TotalMemoryUsage()
		if currentUsage > g.lib.memorySoftLimit {
			s := g.lib.IncrementalChill(g.lib.chillBudgetPerTick)
			stats.NodesChilled += s.NodesChilled
			stats.BytesChilled += s.BytesChilled
		}
	}

	return stats
}

// MemoryPressure returns true if the library is under memory pressure
// (hard limit exceeded and cannot be reduced). Applications should check
// this before performing memory-intensive operations.
func (lib *Library) MemoryPressure() bool {
	lib.mu.RLock()
	defer lib.mu.RUnlock()
	return lib.memoryPressure
}

// CheckMemoryPressureError returns ErrMemoryPressure if the library is under
// memory pressure, nil otherwise. Useful for checking before mutations.
func (lib *Library) CheckMemoryPressureError() error {
	if lib.MemoryPressure() {
		return ErrMemoryPressure
	}
	return nil
}

// touchSnapshot marks a snapshot as recently accessed.
func (g *Garland) touchSnapshot(snap *NodeSnapshot) {
	if snap != nil {
		snap.lastAccessTime = time.Now()
	}
}

// updateMemoryTracking adjusts the memory tracking when data is loaded or chilled.
// Note: This is called while holding g.mu, so it must not acquire any other locks
// that could cause deadlock. Memory pressure checks are done separately via
// CheckMemoryPressure which is called after mutations complete.
func (g *Garland) updateMemoryTracking(delta int64) {
	g.memoryBytes += delta
}

// recalculateMemoryUsage recalculates the total memory usage from scratch.
// Useful after complex operations or for verification.
func (g *Garland) recalculateMemoryUsage() int64 {
	var total int64
	for _, node := range g.nodeRegistry {
		for _, snap := range node.history {
			if snap.isLeaf && snap.storageState == StorageMemory && snap.data != nil {
				total += int64(len(snap.data))
			}
		}
	}
	g.memoryBytes = total
	return total
}

// IncrementalRebalance performs budgeted tree rebalancing along a path.
// Called after mutations with the path of affected nodes.
func (g *Garland) IncrementalRebalance(affectedPath []NodeID) MaintenanceStats {
	if g.lib == nil || g.lib.rebalanceBudget <= 0 {
		return MaintenanceStats{}
	}

	stats := MaintenanceStats{}
	budget := g.lib.rebalanceBudget

	// Process nodes along the path from bottom to top
	for i := len(affectedPath) - 1; i >= 0 && stats.RotationsPerformed < budget; i-- {
		nodeID := affectedPath[i]
		newNodeID := g.rebalanceIfNeeded(nodeID)

		if newNodeID != nodeID {
			stats.RotationsPerformed++

			// If this was the root, update root
			if g.root != nil && g.root.id == nodeID {
				g.root = g.nodeRegistry[newNodeID]
			}

			// If we have a parent in the path, we need to update its reference
			if i > 0 {
				parentID := affectedPath[i-1]
				g.updateChildReference(parentID, nodeID, newNodeID)
			}
		}
	}

	// Reset manipulation counter when rebalancing occurs
	if stats.RotationsPerformed > 0 {
		g.nodeManipulations = 0
	}

	return stats
}

// updateChildReference updates a parent node's child reference after rebalancing.
func (g *Garland) updateChildReference(parentID, oldChildID, newChildID NodeID) {
	parent := g.nodeRegistry[parentID]
	if parent == nil {
		return
	}

	snap := parent.snapshotAt(g.currentFork, g.currentRevision)
	if snap == nil || snap.isLeaf {
		return
	}

	// Create new snapshot with updated reference
	newSnap := &NodeSnapshot{
		isLeaf:    false,
		leftID:    snap.leftID,
		rightID:   snap.rightID,
		byteCount: snap.byteCount,
		runeCount: snap.runeCount,
		lineCount: snap.lineCount,
	}

	if snap.leftID == oldChildID {
		newSnap.leftID = newChildID
	} else if snap.rightID == oldChildID {
		newSnap.rightID = newChildID
	}

	parent.setSnapshot(g.currentFork, g.currentRevision, newSnap)
}

// NeedsRebalancing checks if the tree is significantly unbalanced.
func (g *Garland) NeedsRebalancing() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.root == nil {
		return false
	}

	snap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if snap == nil || snap.isLeaf {
		return false
	}

	leftHeight := g.getHeight(snap.leftID)
	rightHeight := g.getHeight(snap.rightID)

	balance := leftHeight - rightHeight
	if balance < 0 {
		balance = -balance
	}

	// Consider rebalancing needed if balance factor > 2
	return balance > 2
}

// ForceRebalance performs a full tree rebalance (not incremental).
// Use sparingly as this can be expensive for large trees.
func (g *Garland) ForceRebalance() MaintenanceStats {
	g.mu.Lock()
	defer g.mu.Unlock()

	stats := MaintenanceStats{}

	// Collect all leaf data in order
	var leaves []*NodeSnapshot
	g.collectLeafSnapshots(g.root, &leaves)

	if len(leaves) <= 1 {
		return stats
	}

	// Rebuild as a balanced tree
	// This is expensive but guarantees balance
	newRootID := g.rebuildBalanced(leaves, 0, len(leaves))
	if newRootID != 0 && newRootID != g.root.id {
		g.root = g.nodeRegistry[newRootID]
		stats.RotationsPerformed = -1 // indicates full rebuild
		g.nodeManipulations = 0       // reset counter after rebalance
	}

	return stats
}

// collectLeafSnapshots gathers all leaf snapshots in order for rebalancing.
func (g *Garland) collectLeafSnapshots(node *Node, leaves *[]*NodeSnapshot) {
	if node == nil {
		return
	}

	snap := node.snapshotAt(g.currentFork, g.currentRevision)
	if snap == nil {
		return
	}

	if snap.isLeaf {
		*leaves = append(*leaves, snap)
		return
	}

	// Internal node - recurse
	if leftNode := g.nodeRegistry[snap.leftID]; leftNode != nil {
		g.collectLeafSnapshots(leftNode, leaves)
	}
	if rightNode := g.nodeRegistry[snap.rightID]; rightNode != nil {
		g.collectLeafSnapshots(rightNode, leaves)
	}
}

// rebuildBalanced rebuilds a balanced tree from a slice of leaves.
func (g *Garland) rebuildBalanced(leaves []*NodeSnapshot, start, end int) NodeID {
	if start >= end {
		return 0
	}

	if end-start == 1 {
		// Single leaf - create a node for it
		nodeID := g.nextNodeID
		g.nextNodeID++
		node := newNode(nodeID, g)
		node.setSnapshot(g.currentFork, g.currentRevision, leaves[start])
		g.nodeRegistry[nodeID] = node
		return nodeID
	}

	// Split and recurse
	mid := (start + end) / 2
	leftID := g.rebuildBalanced(leaves, start, mid)
	rightID := g.rebuildBalanced(leaves, mid, end)

	if leftID == 0 || rightID == 0 {
		if leftID != 0 {
			return leftID
		}
		return rightID
	}

	// Create internal node
	leftSnap := g.nodeRegistry[leftID].snapshotAt(g.currentFork, g.currentRevision)
	rightSnap := g.nodeRegistry[rightID].snapshotAt(g.currentFork, g.currentRevision)

	nodeID := g.nextNodeID
	g.nextNodeID++
	node := newNode(nodeID, g)
	node.setSnapshot(g.currentFork, g.currentRevision, createInternalSnapshot(leftID, rightID, leftSnap, rightSnap))
	g.nodeRegistry[nodeID] = node

	return nodeID
}

// Close properly shuts down a Garland, including stopping maintenance.
func (lib *Library) Close() error {
	lib.StopMaintenance()
	return nil
}
