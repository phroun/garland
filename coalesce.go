package garland

import "time"

// coalesce.go - undo coalescing: grouping a run of adjacent edits into
// a single history entry.
//
// DESIGN: an editor wants "typing a word" to be ONE undo step, not one
// per keystroke. When coalescing is enabled, an insert that lands at
// the beginning or end of the chunk the previous insert built, or a
// delete that keeps consuming from the same caret location (forward
// delete at the point, or backspace walking left into it), AMENDS the
// current revision instead of minting a new one: the revision's root
// is re-pointed at the new tree and the revision number does not move.
// This is safe precisely because mutations path-copy into fresh node
// IDs - a revision's identity lives entirely in revisionInfo[rev].
// RootID, so re-pointing it atomically replaces the revision's content
// while every other revision keeps resolving through its own root.
//
// Runs end ("bake") at a HARD EDGE:
//   - Bake() - the app forces one (menu action completed, focus lost,
//     whatever policy it likes);
//   - AutoBakeTime - the new edit arrived more than this long after
//     the previous one (0 disables time-based baking);
//   - any non-continuation: different kind (insert vs delete),
//     non-adjacent position, or any other mutation (overwrite, move,
//     copy, decorate, scarify, rebase, ...);
//   - history navigation (UndoSeek, ForkSeek) - resuming an old run
//     after looking around would rewrite what the user just inspected;
//   - a successful save - the save point pins the revision it wrote;
//     amending it afterwards would corrupt "revert to last save";
//   - TransactionStart - a transaction is its own (stronger) grouping:
//     everything inside collapses to one revision already, so
//     coalescing runs may freely exist within a bigger pending
//     transaction (they simply dissolve into it) and a fresh run
//     starts after it commits.
//
// Cursor history: the run's revision records cursor positions only
// when it FINISHES (the next non-amending mutation records them, as
// always, just before creating the next revision) - so undoing back
// onto a coalesced revision restores the end-of-run positions, and
// undoing past it restores the pre-run positions recorded when the
// run started.
//
// Adjacency is POSITION-based, not cursor-identity-based: whatever
// cursor performs the edit, what matters is where the bytes land.

// coalesceKind distinguishes the two coalescible operations.
type coalesceKind int

const (
	coalesceInsert coalesceKind = iota + 1
	coalesceDelete
)

// coalesceState tracks the active run (if any) for one garland.
type coalesceState struct {
	enabled  bool
	autoBake time.Duration // 0 = no time-based baking

	active bool
	fork   ForkID
	rev    RevisionID
	kind   coalesceKind
	// For insert runs, [start, end) is the region the run has built so
	// far; a new insert continues it at start (prepend) or end
	// (append). For delete runs, start == end is the caret anchor the
	// run is consuming at; a forward delete hits it exactly, a
	// backspace ends exactly on it.
	start, end int64
	lastOp     time.Time
}

// coalescePending carries one mutation's coalescing decision from the
// op entry point to recordMutation, which consumes it. Ops that never
// call coalesceDecideLocked leave it zero, so recordMutation treats
// them as run-breaking. The deciding ops also clear it on their error
// paths (deferred), so a failed mutation can never leak a stale
// decision into the next one.
type coalescePending struct {
	valid  bool // set by a coalescible op (insert/delete)
	amend  bool // this op amends the current revision
	kind   coalesceKind
	pos    int64
	length int64
}

// SetUndoCoalescing enables or disables undo coalescing and sets the
// auto-bake window. While enabled, runs of adjacent inserts (typing)
// and adjacent deletes (backspacing / forward-deleting at one caret)
// collapse into a single revision. autoBakeTime > 0 bakes the run
// automatically when the next edit arrives more than that long after
// the previous one; 0 disables time-based baking. Changing the
// configuration is itself a hard edge. Disabled by default - every
// mutation is its own revision unless this is turned on.
func (g *Garland) SetUndoCoalescing(enabled bool, autoBakeTime time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.coalesce.enabled = enabled
	g.coalesce.autoBake = autoBakeTime
	g.coalesce.active = false
}

// UndoCoalescing reports the current coalescing configuration.
func (g *Garland) UndoCoalescing() (enabled bool, autoBakeTime time.Duration) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.coalesce.enabled, g.coalesce.autoBake
}

// Bake forces a hard edge: the current coalescing run (if any) is
// finalized, and the next edit starts a fresh history entry no matter
// how adjacent it is. Safe to call at any time, including when
// coalescing is disabled or no run is active.
func (g *Garland) Bake() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.coalesce.active = false
}

// coalesceDecideLocked decides whether the mutation about to run
// continues the active run (amends the current revision) and stashes
// the decision for recordMutation. Returns the amend verdict so the
// op can skip the pre-mutation cursor-position record. Caller must
// hold the write lock and must clear coalescePending on error paths.
func (g *Garland) coalesceDecideLocked(kind coalesceKind, pos, length int64) bool {
	cs := &g.coalesce
	if !cs.enabled || g.transaction != nil || length <= 0 {
		g.coalescePending = coalescePending{}
		return false
	}

	amend := cs.active &&
		cs.fork == g.currentFork && cs.rev == g.currentRevision &&
		cs.kind == kind && g.isAtHead() &&
		(cs.autoBake <= 0 || time.Since(cs.lastOp) <= cs.autoBake)
	if amend {
		switch kind {
		case coalesceInsert:
			// Beginning or end of the run's chunk (RULING: interior
			// insertions bake - moving the caret into the middle of
			// what you just typed is navigation, and navigation ends
			// the group).
			amend = pos == cs.start || pos == cs.end
		case coalesceDelete:
			// Forward delete at the anchor, or backspace ending on it.
			amend = pos == cs.start || pos+length == cs.start
		}
	}

	g.coalescePending = coalescePending{
		valid:  true,
		amend:  amend,
		kind:   kind,
		pos:    pos,
		length: length,
	}
	return amend
}

// coalesceStartRunLocked begins a run at the just-created revision.
// Caller must hold the write lock.
func (g *Garland) coalesceStartRunLocked(pc coalescePending) {
	cs := &g.coalesce
	cs.active = true
	cs.fork = g.currentFork
	cs.rev = g.currentRevision
	cs.kind = pc.kind
	switch pc.kind {
	case coalesceInsert:
		cs.start, cs.end = pc.pos, pc.pos+pc.length
	case coalesceDelete:
		// After deleting at pos, the gap sits at pos: a forward delete
		// repeats there, a backspace ends there.
		cs.start, cs.end = pc.pos, pc.pos
	}
	cs.lastOp = time.Now()
}

// coalesceExtendRunLocked grows the run to cover an amending op.
// Caller must hold the write lock.
func (g *Garland) coalesceExtendRunLocked(pc coalescePending) {
	cs := &g.coalesce
	switch pc.kind {
	case coalesceInsert:
		// Append at end or prepend at start: either way the chunk is
		// now length longer and still starts at cs.start.
		cs.end += pc.length
	case coalesceDelete:
		if pc.pos+pc.length == cs.start {
			// Backspace: the anchor walks left.
			cs.start, cs.end = pc.pos, pc.pos
		}
		// Forward delete (pc.pos == cs.start): anchor unchanged.
	}
	cs.lastOp = time.Now()
}
