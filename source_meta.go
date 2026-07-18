package garland

import (
	"os"
	"time"
)

// source_meta.go - source metadata tracking, consistency queries, save
// history, and source (warm-storage) switching/recovery.
//
// DESIGN: whenever a file is opened - memory, warm, or cold mode - its
// metadata (size, mtime, identity) is captured through the filesystem
// hook, so the app can ask at any time whether the file was modified by
// another program before deciding how to save (silently, prompt to
// overwrite, fork away to a copy, merge, or abandon and reload). Two
// feeding styles coexist:
//
//   - callback style: Garland asks the filesystem hook (Stat) whenever
//     SourceConsistency / CheckSourceMetadata runs;
//   - volunteer style: the app hands fresh facts to Garland via
//     ReportSourceMetadata as it learns them (its own watcher, a sync
//     client, a VFS event).
//
// Either way Garland tracks state internally and answers consistency
// queries from whatever information it has.
//
// Every successful save records a SavePoint (path, metadata as written,
// and the fork/revision the save captured), enabling:
//
//   - "revert to last saved version" as a pure history seek
//     (RevertToLastSave);
//   - swift source switching (AdoptWarmSource) with the cheapest
//     sufficient consistency check;
//   - automatic recovery (TryRecoverSource) by exploring alternate
//     known save locations when the current source goes bad.

// SourceConsistencyState summarizes the relationship between the
// buffer's baseline (the file as last known to agree with the buffer's
// origin) and the most recent observation of the file.
type SourceConsistencyState int

const (
	// ConsistencyUntracked: no metadata baseline exists - there is no
	// source path, or the filesystem hook does not support Stat and the
	// application never volunteered metadata.
	ConsistencyUntracked SourceConsistencyState = iota

	// ConsistencyClean: the file looks exactly as baselined.
	ConsistencyClean

	// ConsistencyAppended: the file grew; existing content may be intact
	// (verify with VerifyBoundaryForAppend).
	ConsistencyAppended

	// ConsistencyModified: same size but the modification time changed.
	ConsistencyModified

	// ConsistencyTruncated: the file shrank.
	ConsistencyTruncated

	// ConsistencyReplaced: the path is bound to a different storage
	// object (the file was replaced by rename).
	ConsistencyReplaced

	// ConsistencyMissing: the file no longer exists.
	ConsistencyMissing
)

// String returns a human-readable description of the state.
func (s SourceConsistencyState) String() string {
	switch s {
	case ConsistencyUntracked:
		return "untracked"
	case ConsistencyClean:
		return "clean"
	case ConsistencyAppended:
		return "appended"
	case ConsistencyModified:
		return "modified"
	case ConsistencyTruncated:
		return "truncated"
	case ConsistencyReplaced:
		return "replaced"
	case ConsistencyMissing:
		return "missing"
	default:
		return "unknown"
	}
}

// consistencyFromChange maps a change classification onto the
// consistency state it implies.
func consistencyFromChange(t SourceChangeType) SourceConsistencyState {
	switch t {
	case SourceUnchanged:
		return ConsistencyClean
	case SourceAppended:
		return ConsistencyAppended
	case SourceModified:
		return ConsistencyModified
	case SourceTruncated:
		return ConsistencyTruncated
	case SourceReplaced:
		return ConsistencyReplaced
	case SourceDeleted:
		return ConsistencyMissing
	default:
		return ConsistencyUntracked
	}
}

// SourceConsistencyReport answers "can I trust the source file right
// now?" with everything the app needs for its UI decision.
type SourceConsistencyReport struct {
	State SourceConsistencyState

	// Baseline is the metadata recorded when buffer and file last
	// agreed (open, save, adoption). Zero when State is Untracked.
	Baseline FileMetadata

	// Observed is the most recent observation (stat through the hook or
	// volunteered), Zero when nothing was ever observed past baseline.
	Observed FileMetadata

	// ObservedAt is when Observed was recorded (zero: never).
	ObservedAt time.Time

	// LockedBy is the owner string of a foreign emacs-style lock file
	// seen on the source ("user@host.pid"), empty when none is known.
	// Only populated when FileOptions.UseEmacsLocks was enabled.
	LockedBy string
}

// SourceConsistency performs a fresh metadata check through the
// filesystem hook and reports the source file's consistency state.
// With a metadata-less hook it reports from the last volunteered
// observation instead (never an error - the report just says what is
// actually known). When emacs locks are enabled the lock file is also
// re-probed, so a foreign editor grabbing the file shows up here.
func (g *Garland) SourceConsistency() (SourceConsistencyReport, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.sourcePath != "" && g.sourceState != nil && g.sourceState.metaTracked {
		meta, err := g.statSourceLocked()
		if err == nil {
			g.absorbSourceObservationLocked(meta)
		} else if err != ErrNotSupported {
			return SourceConsistencyReport{}, err
		}
	}
	g.probeEmacsLockLocked()
	return g.consistencyReportLocked(), nil
}

// SourceConsistencyCached reports the consistency state from what
// Garland already knows, with NO filesystem access - safe to call from
// a paint path. Pair it with volunteered metadata (ReportSourceMetadata)
// or periodic SourceConsistency / EnableSourceWatch calls.
func (g *Garland) SourceConsistencyCached() SourceConsistencyReport {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.consistencyReportLocked()
}

// consistencyReportLocked builds the report from tracked state only.
// Caller must hold at least the read lock.
func (g *Garland) consistencyReportLocked() SourceConsistencyReport {
	rep := SourceConsistencyReport{State: ConsistencyUntracked}
	if el := g.emacsLock; el != nil {
		rep.LockedBy = el.foreignOwner
	}
	st := g.sourceState
	if g.sourcePath == "" || st == nil || !st.metaTracked {
		return rep
	}
	rep.Baseline = FileMetadata{
		Exists:   true,
		Size:     st.originalSize,
		ModTime:  st.originalMtime,
		Identity: st.originalIdentity,
	}
	rep.State = ConsistencyClean
	if st.observedValid {
		rep.Observed = st.observedMeta
		rep.ObservedAt = st.observedAt
		rep.State = consistencyFromChange(g.classifySourceMeta(st.observedMeta).Type)
	}
	return rep
}

// ReportSourceMetadata volunteers fresh metadata for the source file -
// the push-style counterpart to SourceConsistency's pull. Use it when
// the app learns facts Garland cannot observe itself (its own file
// watcher, a sync client, a virtualized filesystem event). The
// observation is recorded exactly as a stat result would be, and the
// resulting consistency state is returned. When no baseline exists yet
// (a metadata-less filesystem hook), the first volunteered observation
// becomes the baseline.
func (g *Garland) ReportSourceMetadata(meta FileMetadata) SourceConsistencyState {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.sourceState == nil {
		g.initSourceState()
	}
	info := g.absorbSourceObservationLocked(meta)
	return consistencyFromChange(info.Type)
}

// ---- Save history ----

// maxSavePoints bounds the save-history ring. Old entries fall off the
// front; the newest save is always retained.
const maxSavePoints = 8

// SavePoint records one successful save: where the content went, what
// the file looked like immediately afterwards, and which fork/revision
// the save captured - the anchor for "revert to last saved version"
// (a pure history seek) and for source recovery/adoption.
type SavePoint struct {
	// Fork and Revision identify the buffer state the save wrote.
	Fork     ForkID
	Revision RevisionID

	// Path is where the content was written.
	Path string

	// Meta is the file's metadata observed right after the save (zero
	// when the destination filesystem cannot report metadata).
	Meta FileMetadata

	// SavedAt is when the save completed.
	SavedAt time.Time

	// AdoptedAsSource reports whether this location became (or already
	// was) the buffer's source / warm-storage backing at save time.
	AdoptedAsSource bool

	// fs reaches Path for later verification (TryRecoverSource).
	fs FileSystemInterface
}

// recordSavePointLocked appends a SavePoint for a save that just
// completed successfully, anchored at the current fork/revision.
// Caller must hold the write lock.
func (g *Garland) recordSavePointLocked(fs FileSystemInterface, path string, adopted bool) {
	g.recordSavePointAtLocked(fs, path, adopted, g.currentFork, g.currentRevision)
}

// recordSavePointAtLocked is recordSavePointLocked with explicit
// coordinates, for the concurrent save (whose plan pins a state the
// live head may since have moved past). Caller must hold the write lock.
func (g *Garland) recordSavePointAtLocked(fs FileSystemInterface, path string, adopted bool, fork ForkID, rev RevisionID) {
	meta, err := fs.Stat(path)
	if err != nil {
		meta = FileMetadata{} // unknown metadata still anchors the revision
	}
	g.saveHistory = append(g.saveHistory, SavePoint{
		Fork:            fork,
		Revision:        rev,
		Path:            path,
		Meta:            meta,
		SavedAt:         time.Now(),
		AdoptedAsSource: adopted,
		fs:              fs,
	})
	if len(g.saveHistory) > maxSavePoints {
		g.saveHistory = g.saveHistory[len(g.saveHistory)-maxSavePoints:]
	}
}

// SaveHistory returns the recorded save points, oldest first (bounded
// to the most recent few).
func (g *Garland) SaveHistory() []SavePoint {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]SavePoint, len(g.saveHistory))
	copy(out, g.saveHistory)
	return out
}

// LastSave returns the most recent save point, if any.
func (g *Garland) LastSave() (SavePoint, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if len(g.saveHistory) == 0 {
		return SavePoint{}, false
	}
	return g.saveHistory[len(g.saveHistory)-1], true
}

// RevertToLastSave rewinds the buffer to the state captured by the most
// recent save - "revert to last saved version" as a pure history seek.
// Nothing is destroyed: the abandoned edits remain reachable as redo /
// fork history until the app prunes them. Returns ErrRevisionNotFound
// when no save was recorded (or the revision was pruned away).
func (g *Garland) RevertToLastSave() error {
	g.mu.RLock()
	var sp SavePoint
	ok := len(g.saveHistory) > 0
	if ok {
		sp = g.saveHistory[len(g.saveHistory)-1]
	}
	g.mu.RUnlock()
	if !ok {
		return ErrRevisionNotFound
	}

	// ForkSeek/UndoSeek take their own locks (and refuse mid-
	// transaction). The emacs lock, when enabled, is synchronized by the
	// seek hooks: landing on the save point releases it.
	if err := g.ForkSeek(sp.Fork); err != nil {
		return err
	}
	return g.UndoSeek(sp.Revision)
}

// ---- Source switching, adoption, recovery ----

// VerifyLevel selects how much evidence AdoptWarmSource requires that a
// candidate file's content matches the buffer, trading speed for proof.
type VerifyLevel int

const (
	// VerifyMetadata is the swiftest check: the candidate must carry
	// the exact metadata recorded by a save of THIS buffer state (a
	// SavePoint at the current fork/revision for that path). No content
	// is read. With a metadata-less filesystem the save point alone is
	// accepted as the best available evidence.
	VerifyMetadata VerifyLevel = iota

	// VerifySample also hash-checks a bounded sample of content spans
	// (first, last, and a few evenly spaced blocks).
	VerifySample

	// VerifyFull hash-checks every span before adopting.
	VerifyFull
)

// AdoptWarmSource switches the buffer's source (warm-storage backing)
// to another file that is believed to hold exactly the current
// content - swiftly and seamlessly, with the cheapest sufficient
// consistency check chosen by level. On success every current leaf is
// re-homed onto the new file, undo history that referenced the old
// source is migrated off it (cold storage when available, memory
// otherwise; unreachable history is marked lost, never silently
// corrupted), and change detection re-baselines against the new file.
//
// A nil fs resolves like SaveAs: the current source filesystem, else
// the library default. Returns ErrWarmStorageMismatch when the
// candidate cannot be proven consistent at the requested level.
func (g *Garland) AdoptWarmSource(fs FileSystemInterface, name string, level VerifyLevel) error {
	if name == "" {
		return ErrNoDataSource
	}

	// Serialize against saves; their rewrite would race the adoption.
	g.saveMu.Lock()
	defer g.saveMu.Unlock()

	g.mu.Lock()
	defer g.mu.Unlock()
	g.awaitNoSaveLocked()

	if fs == nil {
		fs = g.sourceFS
		if fs == nil {
			fs = g.lib.defaultFS
		}
	}

	// ---- Verify the candidate against the buffer ----
	meta, statErr := fs.Stat(name)
	if statErr != nil && statErr != ErrNotSupported {
		return statErr
	}
	haveMeta := statErr == nil
	if haveMeta {
		if !meta.Exists {
			return os.ErrNotExist
		}
		if meta.Size != g.totalBytes {
			return ErrWarmStorageMismatch
		}
	}

	var handle FileHandle
	switch level {
	case VerifyMetadata:
		if !g.savePointMatchesLocked(name, meta, haveMeta) {
			return ErrWarmStorageMismatch
		}
	default:
		h, err := fs.Open(name, OpenModeRead)
		if err != nil {
			return err
		}
		if err := g.verifySpansAgainstFile(fs, h, level == VerifyFull); err != nil {
			fs.Close(h)
			return err
		}
		handle = h
	}

	if handle == nil {
		h, err := fs.Open(name, OpenModeRead)
		if err != nil {
			return err
		}
		handle = h
	}

	g.adoptSourceLocked(fs, name, handle, true)
	return nil
}

// savePointMatchesLocked reports whether a save point exists proving
// the file at path holds the CURRENT buffer state: recorded at the
// current fork/revision, and (when both sides have metadata) the file
// still carries exactly the metadata recorded right after that save.
func (g *Garland) savePointMatchesLocked(path string, meta FileMetadata, haveMeta bool) bool {
	for i := len(g.saveHistory) - 1; i >= 0; i-- {
		sp := g.saveHistory[i]
		if sp.Path != path || sp.Fork != g.currentFork || sp.Revision != g.currentRevision {
			continue
		}
		if !haveMeta || !sp.Meta.Exists {
			// No metadata to compare on one side or the other: the save
			// point itself is the best available evidence.
			return true
		}
		if sp.Meta.Size != meta.Size || !sp.Meta.ModTime.Equal(meta.ModTime) {
			return false
		}
		if sp.Meta.Identity != "" && meta.Identity != "" &&
			sp.Meta.Identity != meta.Identity {
			return false
		}
		return true
	}
	return false
}

// verifySpansAgainstFile checks that the file behind handle holds the
// buffer's content: every span when full, else a bounded sample
// (first, last, and evenly spaced interior blocks). Spans whose
// expected hash is unavailable without storage access (chilled blocks
// outside the sample) are trusted at sample level and verified at full
// level (which thaws them as needed). Placeholder leaves (lost data)
// always fail - unknown content cannot be proven consistent.
func (g *Garland) verifySpansAgainstFile(fs FileSystemInterface, handle FileHandle, full bool) error {
	spans := g.currentLeafSpans()

	// Choose which spans to check.
	checkIdx := make(map[int]bool, len(spans))
	if full {
		for i := range spans {
			checkIdx[i] = true
		}
	} else {
		const sampleTarget = 8
		n := len(spans)
		if n <= sampleTarget {
			for i := 0; i < n; i++ {
				checkIdx[i] = true
			}
		} else {
			checkIdx[0] = true
			checkIdx[n-1] = true
			step := float64(n-1) / float64(sampleTarget-1)
			for k := 1; k < sampleTarget-1; k++ {
				checkIdx[int(float64(k)*step)] = true
			}
		}
	}

	for i, sp := range spans {
		if sp.snap.byteCount == 0 {
			continue
		}
		if sp.snap.storageState == StoragePlaceholder {
			return ErrWarmStorageMismatch
		}
		if !checkIdx[i] {
			continue
		}
		want := expectedLeafHash(sp.snap)
		if want == nil {
			if !full {
				continue // sample level trusts what it cannot cheaply hash
			}
			// Full level: make the leaf resident to learn its hash.
			if err := g.ensureLeafDataResident(sp.node, sp.snap); err != nil {
				return err
			}
			want = expectedLeafHash(sp.snap)
			if want == nil {
				return ErrWarmStorageMismatch
			}
		}
		if err := fs.SeekByte(handle, sp.bufOff); err != nil {
			return err
		}
		got, err := fs.ReadBytes(handle, int(sp.snap.byteCount))
		if err != nil || int64(len(got)) != sp.snap.byteCount {
			return ErrWarmStorageMismatch
		}
		if !hashesEqual(want, computeHash(got)) {
			return ErrWarmStorageMismatch
		}
	}
	return nil
}

// adoptSourceLocked makes (fs, path, handle) the buffer's source. The
// file is trusted to hold exactly the current content (the caller
// verified as much). Steps, in an order that never loses data:
//
//  1. Undo history referencing the OLD source by file offset is
//     migrated off it while it is still reachable (preserve semantics:
//     cold storage when available, else memory; unreadable blocks are
//     marked lost with a reason - amputated, never silently corrupted).
//  2. Every current leaf is re-homed at its buffer offset in the new
//     file, with a content hash so warm verification keeps working.
//  3. Source bookkeeping switches over and change detection
//     re-baselines (rebaseSourceBookkeeping).
//
// Caller must hold the write lock and own handle (open on fs/path).
func (g *Garland) adoptSourceLocked(fs FileSystemInterface, path string, handle FileHandle, preserveHistory bool) {
	// A held emacs lock names the OLD path - release it before the
	// switch, while g.sourcePath still points there.
	g.releaseEmacsLockLocked()

	spans := g.currentLeafSpans()
	currentSnaps := make(map[*NodeSnapshot]bool, len(spans))
	for _, sp := range spans {
		currentSnaps[sp.snap] = true
	}
	g.detachOldSourceHistory(currentSnaps, preserveHistory)

	for _, sp := range spans {
		sp.snap.originalFileOffset = sp.bufOff
		if len(sp.snap.dataHash) == 0 && sp.snap.data != nil {
			sp.snap.dataHash = computeHash(sp.snap.data)
		}
	}

	g.rebaseSourceBookkeeping(fs, path, handle, true, true)

	// The new source holds exactly the current content: this is a
	// clean point (emacs lock released above stays released), and any
	// foreign-lock knowledge from the old path is stale - re-probe.
	if el := g.emacsLock; el != nil {
		el.foreignOwner = ""
		g.emacsLockSavedLocked()
		g.probeEmacsLockLocked()
	}
}

// detachOldSourceHistory strips old-source file references from every
// history snapshot outside the current view, because the source they
// point into is being abandoned. Warm history (whose ONLY copy is the
// old source) is read back and migrated while the old source is still
// attached; blocks that cannot be read are marked lost with a reason
// (the recovery scenario: the old source is corrupt - that is WHY the
// source is being switched). Unlike invalidateDisturbedHistory this
// never fails: adoption must be able to proceed away from a broken
// source. Caller must hold the write lock.
func (g *Garland) detachOldSourceHistory(currentSnaps map[*NodeSnapshot]bool, preserve bool) {
	for _, node := range g.nodeRegistry {
		if node == nil {
			continue
		}
		for key, snap := range node.history {
			if snap == nil || !snap.isLeaf || currentSnaps[snap] {
				continue
			}
			if snap.originalFileOffset < 0 {
				continue
			}
			if snap.storageState == StorageWarm {
				migrated := false
				if preserve {
					if err := g.readFromWarmStorageWithTrust(node.id, snap); err == nil {
						migrated = true
						if g.lib.coldStorageBackend != nil && g.loadingStyle != MemoryOnly {
							// Best-effort: on chill failure the data
							// simply stays in memory.
							_ = g.chillSnapshot(node.id, key, snap)
						}
					}
				}
				if !migrated && snap.storageState == StorageWarm {
					g.markSnapshotLost(snap,
						"previous source abandoned during source switch")
				}
			}
			snap.originalFileOffset = -1
		}
	}
}

// TryRecoverSource walks the save history newest-first looking for an
// alternate known location whose file can be adopted as the source -
// automatic recovery for when the current source becomes corrupt or
// unreachable. Each candidate is checked at the given level (see
// AdoptWarmSource); the first that verifies is adopted and its save
// point returned. Returns ErrNoRecoverySource when no alternate
// location works.
func (g *Garland) TryRecoverSource(level VerifyLevel) (SavePoint, error) {
	g.mu.RLock()
	hist := make([]SavePoint, len(g.saveHistory))
	copy(hist, g.saveHistory)
	curPath := g.sourcePath
	g.mu.RUnlock()

	tried := make(map[string]bool)
	for i := len(hist) - 1; i >= 0; i-- {
		sp := hist[i]
		if sp.Path == curPath || tried[sp.Path] {
			continue
		}
		tried[sp.Path] = true
		fs := sp.fs
		if err := g.AdoptWarmSource(fs, sp.Path, level); err == nil {
			return sp, nil
		}
	}
	return SavePoint{}, ErrNoRecoverySource
}

// ---- Device information ----

// DeviceInfoFor reports the storage device behind a path (identity and
// free space) through the given filesystem, for free-space warnings
// before a save. A nil fs uses the library default (local disk).
func (lib *Library) DeviceInfoFor(fs FileSystemInterface, name string) (DeviceInfo, error) {
	if fs == nil {
		fs = lib.defaultFS
	}
	return fs.DeviceInfo(name)
}

// SourceDeviceInfo reports the storage device behind this buffer's
// source file. Returns ErrNoDataSource when there is no source path.
func (g *Garland) SourceDeviceInfo() (DeviceInfo, error) {
	g.mu.RLock()
	fs := g.sourceFS
	path := g.sourcePath
	g.mu.RUnlock()

	if path == "" {
		return DeviceInfo{}, ErrNoDataSource
	}
	if fs == nil {
		fs = g.lib.defaultFS
	}
	return fs.DeviceInfo(path)
}
