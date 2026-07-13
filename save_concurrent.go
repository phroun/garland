package garland

import "sync"

// save_concurrent.go - the opt-in lock-free save (SaveOptions.Concurrent).
//
// The locked save holds the buffer lock for the whole rewrite because
// the file doubles as warm backing: its two-phase write ordering only
// works while the saver is the sole reader. To let the app keep
// editing during a long save, the file must stop being a read
// dependency for anything the rewrite disturbs. Three phases:
//
//   PLAN (locked, brief): scarify placeholders, compute the span
//   layout at the current revision R, and EVACUATE every moving warm
//   span - its bytes are read into memory now, and its stale file
//   offset cleared so nothing can re-warm against a region about to
//   be overwritten. Cold spans stay cold (the plan records their
//   block names; blocks are never deleted mid-save because Prune /
//   DeleteFork wait on the save). History intersecting the disturbed
//   region is migrated / invalidated exactly like the locked path.
//   After this phase the plan's sources are: untouched file regions
//   below protectFrom (skip spans - nobody writes them), immutable
//   memory slices, and pinned cold blocks. None can change under a
//   concurrent editor, so...
//
//   REWRITE (no lock): ...the writes need no ordering dance at all -
//   plain ascending writes on the saver's own handle, then the
//   truncate. Concurrent reads hit memory, cold, or undisturbed warm
//   (file bytes below protectFrom are never written); concurrent
//   edits build new revisions in the persistent tree; concurrent
//   chills are safe because every displaced span's offset is already
//   cleared (chill-to-warm is ineligible) and chilling to cold only
//   nils a data pointer the plan does not read (the plan holds its
//   own slice of the immutable backing array).
//
//   RE-HOME (locked, brief): stamp the new offsets onto the plan's
//   snapshots (their content is immutable, so the statement "the file
//   holds these bytes at newOff" is valid for every revision sharing
//   them), flip file-proven cold spans to warm, re-baseline source
//   tracking, release the waiters.
//
// COST (the reason this is opt-in): evacuation temporarily holds the
// moved warm bytes in memory. With a cold backend, history migration
// absorbs part of that on disk; without one, a save after edits near
// the top of the file can hold nearly the whole file resident. When
// there is no cold backend, a hard memory limit is configured, and
// the evacuation would exceed it, the save transparently falls back
// to the locked zero-copy path (SaveReport.Concurrent = false).
//
// The app-side "hot-write mode" pairs with this: MemoryPressure()
// reports how many resident bytes only a Save can make evictable, so
// the app can decide when "please save when possible" is the last
// resort before running out of RAM.

// MemoryPressureInfo describes where this buffer's resident memory
// stands and what can relieve it. It is the signal for an app-side
// "hot-write mode": when SaveableBytes dominates ResidentBytes and the
// total approaches the hard limit, saving is the only relief left -
// "in order to keep editing, I need to save before I run out of RAM."
// The library reports; the app decides when to prompt or act.
type MemoryPressureInfo struct {
	// ResidentBytes is the leaf data currently held in memory.
	ResidentBytes int64
	// EvictableBytes could be chilled to warm or cold storage right
	// now without any save.
	EvictableBytes int64
	// SaveableBytes are current-revision bytes that only a Save can
	// make evictable: they have no file backing yet and no cold
	// backend exists, so saving (which gives them warm backing) is
	// the only way to get them out of memory.
	SaveableBytes int64
	// SoftLimitBytes / HardLimitBytes echo the configured library
	// limits (0 = none).
	SoftLimitBytes int64
	HardLimitBytes int64
}

// MemoryPressure reports the buffer's memory standing. See
// MemoryPressureInfo for the intended app-side use.
func (g *Garland) MemoryPressure() MemoryPressureInfo {
	g.mu.RLock()
	defer g.mu.RUnlock()

	info := MemoryPressureInfo{
		SoftLimitBytes: g.lib.memorySoftLimit,
		HardLimitBytes: g.lib.memoryHardLimit,
	}
	current := make(map[*NodeSnapshot]bool)
	for _, sp := range g.currentLeafSpans() {
		current[sp.snap] = true
	}
	coldOK := g.lib.coldStorageBackend != nil && g.loadingStyle != MemoryOnly
	warmOK := g.loadingStyle == AllStorage && g.sourceHandle != nil
	seen := make(map[*NodeSnapshot]bool)
	for _, node := range g.nodeRegistry {
		if node == nil {
			continue
		}
		for _, snap := range node.history {
			if snap == nil || !snap.isLeaf || seen[snap] {
				continue
			}
			seen[snap] = true
			if snap.storageState != StorageMemory || len(snap.data) == 0 {
				continue
			}
			n := int64(len(snap.data))
			info.ResidentBytes += n
			switch {
			case coldOK || (warmOK && snap.originalFileOffset >= 0):
				info.EvictableBytes += n
			case current[snap] && g.sourcePath != "" && g.loadingStyle == AllStorage:
				info.SaveableBytes += n
			}
		}
	}
	return info
}

// planSpan is one leaf of the pinned revision with a lock-independent
// content source.
type planSpan struct {
	node   *Node
	snap   *NodeSnapshot
	newOff int64
	length int64
	skip   bool   // file already holds the bytes at newOff - never written
	data   []byte // resident source (evacuated warm, or memory leaf)
	block  string // cold-storage block name when sourced from cold
	hash   []byte // expected hash for the cold source
}

func (g *Garland) saveConcurrent(fs FileSystemInterface, opts SaveOptions) (SaveReport, error) {
	// ---- PLAN: everything below happens under the lock ----
	g.mu.Lock()

	scars, err := g.scarifyPlaceholders()
	if err != nil {
		g.mu.Unlock()
		return SaveReport{}, err
	}
	report := SaveReport{Scars: scars, Concurrent: true}

	// Evacuation budget: without a cold backend the moving warm bytes
	// land in memory. If that would blow the configured hard limit,
	// run the locked zero-copy save instead.
	if g.lib.coldStorageBackend == nil && g.lib.memoryHardLimit > 0 {
		var evac int64
		var oldCursor int64
		for _, sp := range g.currentLeafSpans() {
			snap := sp.snap
			if snap.byteCount == 0 || snap.storageState != StorageWarm {
				continue
			}
			if snap.originalFileOffset >= 0 && snap.originalFileOffset >= oldCursor &&
				snap.originalFileOffset == sp.bufOff {
				oldCursor = snap.originalFileOffset + snap.byteCount
				continue // unmoved: stays warm, no evacuation
			}
			evac += snap.byteCount
		}
		// This buffer's own residency approximates the budget - do NOT
		// call lib.TotalMemoryUsage() here: it RLocks every garland
		// including this one, which we hold write-locked (deadlock).
		if g.memoryBytes+evac > g.lib.memoryHardLimit {
			defer g.mu.Unlock()
			rep, err := g.saveInPlace(fs, opts)
			rep.Concurrent = false
			return rep, err
		}
	}

	// Collect the plan. Every moving span's source is made
	// lock-independent NOW; every displaced span's file offset is
	// cleared so no concurrent chill can re-warm it against bytes the
	// rewrite is about to replace (RE-HOME restores the offsets).
	var spans []planSpan
	var walkErr error
	var newCursor, oldCursor int64
	currentSnaps := make(map[*NodeSnapshot]bool)
	var collect func(nodeID NodeID)
	collect = func(nodeID NodeID) {
		if walkErr != nil {
			return
		}
		node := g.nodeRegistry[nodeID]
		if node == nil {
			return
		}
		snap := node.snapshotAt(g.currentFork, g.currentRevision)
		if snap == nil {
			return
		}
		if !snap.isLeaf {
			collect(snap.leftID)
			collect(snap.rightID)
			return
		}
		if snap.byteCount == 0 {
			return
		}
		currentSnaps[snap] = true

		sp := planSpan{node: node, snap: snap, newOff: newCursor, length: snap.byteCount}
		if snap.storageState == StoragePlaceholder {
			walkErr = ErrColdStorageFailure // scarify left one behind?
			return
		}
		if snap.originalFileOffset >= 0 && snap.originalFileOffset >= oldCursor &&
			snap.originalFileOffset == newCursor {
			// Unmoved file-backed bytes: never written, never read -
			// warm reads of this region stay valid throughout.
			sp.skip = true
			oldCursor = snap.originalFileOffset + snap.byteCount
		} else {
			switch snap.storageState {
			case StorageWarm:
				// EVACUATE: the only copy is in the file, and the file
				// is about to change under it.
				if err := g.readFromWarmStorageWithTrust(node.id, snap); err != nil {
					walkErr = err
					return
				}
				sp.data = snap.data
				snap.originalFileOffset = -1
			case StorageCold:
				// Stays cold; the block name is stable (chill-time
				// key) and Prune/DeleteFork wait on the save.
				key := ForkRevision{g.currentFork, g.currentRevision}
				for k, s := range node.history {
					if s == snap {
						key = k
						break
					}
				}
				sp.block = formatBlockName(node.id, key)
				sp.hash = snap.dataHash
				snap.originalFileOffset = -1
			default: // memory
				sp.data = snap.data
				snap.originalFileOffset = -1
			}
		}
		newCursor += sp.length
		spans = append(spans, sp)
	}
	if g.root != nil {
		collect(g.root.id)
	}
	if walkErr != nil {
		g.mu.Unlock()
		return report, walkErr
	}
	newTotal := newCursor

	protectFrom := newTotal
	for _, sp := range spans {
		if !sp.skip && sp.newOff < protectFrom {
			protectFrom = sp.newOff
		}
	}

	writeHandle, err := fs.Open(g.sourcePath, OpenModeReadWrite)
	if err != nil {
		g.mu.Unlock()
		return report, err
	}
	var oldSize int64 = -1
	if sz, err := fs.FileSize(writeHandle); err == nil {
		oldSize = sz
	}
	if newTotal < oldSize && newTotal < protectFrom {
		protectFrom = newTotal // truncation destroys the tail too
	}

	if err := g.invalidateDisturbedHistory(currentSnaps, protectFrom, opts); err != nil {
		fs.Close(writeHandle)
		g.mu.Unlock()
		return report, err
	}

	if g.saveCond == nil {
		g.saveCond = sync.NewCond(&g.mu)
	}
	g.saveInFlight = true
	g.mu.Unlock()

	finish := func(err error) (SaveReport, error) {
		fs.Close(writeHandle)
		g.mu.Lock()
		g.saveInFlight = false
		g.saveCond.Broadcast()
		g.mu.Unlock()
		return report, err
	}

	// ---- REWRITE: no lock held; the app keeps working ----
	for i := range spans {
		sp := &spans[i]
		if sp.skip {
			continue
		}
		data := sp.data
		if data == nil && sp.block != "" {
			d, err := g.lib.coldStorageBackend.Get(g.id, sp.block)
			if err != nil {
				return finish(err)
			}
			if len(sp.hash) > 0 && !hashesEqual(sp.hash, computeHash(d)) {
				return finish(ErrColdStorageFailure)
			}
			data = d
		}
		if int64(len(data)) != sp.length {
			return finish(ErrInternal)
		}
		if err := fs.SeekByte(writeHandle, sp.newOff); err != nil {
			return finish(err)
		}
		if err := fs.WriteBytes(writeHandle, data); err != nil {
			return finish(err)
		}
	}
	if oldSize >= 0 && newTotal < oldSize {
		if err := fs.Truncate(writeHandle, newTotal); err != nil {
			return finish(err)
		}
	}
	fs.Close(writeHandle)

	// ---- RE-HOME: brief lock to stamp the new layout ----
	g.mu.Lock()
	for i := range spans {
		sp := &spans[i]
		sp.snap.originalFileOffset = sp.newOff
		switch {
		case sp.skip && sp.snap.storageState == StorageWarm:
			g.updateWarmVerification(sp.node.id)
		case sp.snap.storageState == StorageCold &&
			g.loadingStyle == AllStorage && g.sourceHandle != nil:
			// Bytes are provably in the file at newOff (either skipped
			// in place or just written from this very block) - the
			// file is the better backing store.
			sp.snap.storageState = StorageWarm
			g.updateWarmVerification(sp.node.id)
		default:
			g.updateWarmVerification(sp.node.id)
		}
	}
	if g.sourceState != nil {
		g.sourceState.status = SourceStatusNormal
		_ = g.captureSourceInfo()
	}
	report.Integrity = g.drainIntegrityEvents()
	g.saveInFlight = false
	g.saveCond.Broadcast()
	g.mu.Unlock()

	return report, nil
}
