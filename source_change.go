package garland

import (
	"os"
	"sync"
	"time"
)

// SourceChangeType indicates the type of change detected in the source file.
type SourceChangeType int

const (
	// SourceUnchanged indicates no change was detected.
	SourceUnchanged SourceChangeType = iota

	// SourceAppended indicates the file grew but existing content is intact.
	SourceAppended

	// SourceModified indicates existing content was altered.
	SourceModified

	// SourceTruncated indicates the file was shortened.
	SourceTruncated

	// SourceReplaced indicates the file was replaced (different inode).
	SourceReplaced

	// SourceDeleted indicates the file no longer exists.
	SourceDeleted
)

// String returns a human-readable description of the change type.
func (t SourceChangeType) String() string {
	switch t {
	case SourceUnchanged:
		return "unchanged"
	case SourceAppended:
		return "appended"
	case SourceModified:
		return "modified"
	case SourceTruncated:
		return "truncated"
	case SourceReplaced:
		return "replaced"
	case SourceDeleted:
		return "deleted"
	default:
		return "unknown"
	}
}

// SourceChangeInfo contains details about a detected source file change.
type SourceChangeInfo struct {
	Type          SourceChangeType
	PreviousSize  int64
	CurrentSize   int64
	AppendedBytes int64 // Only valid if Type == SourceAppended
}

// AppendPolicy controls how the library handles detected appends.
type AppendPolicy int

const (
	// AppendPolicyAsk notifies the application and waits for a decision.
	AppendPolicyAsk AppendPolicy = iota

	// AppendPolicyIgnore ignores this append but asks again next time.
	AppendPolicyIgnore

	// AppendPolicyNever ignores all future appends permanently.
	AppendPolicyNever

	// AppendPolicyOnce loads this append but asks again next time.
	AppendPolicyOnce

	// AppendPolicyContinuous keeps loading appends automatically (tail mode).
	AppendPolicyContinuous
)

// SourceChangeStatus indicates the current status of source file tracking.
type SourceChangeStatus int

const (
	// SourceStatusNormal indicates no changes have been detected.
	SourceStatusNormal SourceChangeStatus = iota

	// SourceStatusAppendAvailable indicates an append was detected and verified.
	SourceStatusAppendAvailable

	// SourceStatusSuspectChange indicates metadata changed but not yet verified.
	SourceStatusSuspectChange

	// SourceStatusModified indicates a checksum mismatch was confirmed.
	SourceStatusModified
)

// WarmTrustLevel indicates how much we trust warm storage for a given block.
type WarmTrustLevel int

const (
	// WarmTrustFull indicates no changes ever detected, fully trusted.
	WarmTrustFull WarmTrustLevel = iota

	// WarmTrustVerified indicates changes detected but block verified since.
	WarmTrustVerified

	// WarmTrustStale indicates changes detected and block not verified.
	WarmTrustStale

	// WarmTrustSuspended indicates user notified but hasn't responded.
	WarmTrustSuspended
)

// SourceChangeHandler is called when a source file change is detected.
type SourceChangeHandler func(g *Garland, status SourceChangeStatus, info SourceChangeInfo)

// sourceState tracks the state of the source file for change detection.
type sourceState struct {
	// Baseline file metadata: what the file looked like the last time
	// buffer and file were known to agree (open, save, source adoption).
	originalMtime    time.Time
	originalSize     int64
	originalIdentity string

	// metaTracked reports whether a baseline was ever captured. False
	// when there is no source path, or the filesystem hook does not
	// support Stat and the application never volunteered metadata.
	metaTracked bool

	// Most recent observation of the file (from a stat through the
	// filesystem hook or volunteered via ReportSourceMetadata).
	observedMeta  FileMetadata
	observedAt    time.Time
	observedValid bool

	// Change tracking
	changeCounter  uint64    // Incremented on any detected metadata change
	lastChangeTime time.Time // When we last detected a change

	// User notification state
	status               SourceChangeStatus
	userNotifiedPending  bool // User has been notified but hasn't responded
	appendAvailableBytes int64

	// Policy settings
	appendPolicy AppendPolicy
	verifyOnRead bool // Whether to verify checksums on warm reads (default true)

	// Callback
	changeHandler SourceChangeHandler

	// Watch state
	watchEnabled bool
	watchStop    chan struct{}
	watchWg      sync.WaitGroup
}

// warmVerificationState tracks when a block was last verified.
type warmVerificationState struct {
	verifiedAtCounter uint64    // sourceChangeCounter when last verified
	verifiedTime      time.Time // When last verified or loaded into memory
}

// initSourceState initializes source file tracking for a Garland.
func (g *Garland) initSourceState() {
	g.sourceState = &sourceState{
		verifyOnRead: true, // Default to verifying warm reads
		appendPolicy: AppendPolicyAsk,
	}
	g.warmVerification = make(map[NodeID]*warmVerificationState)
}

// statSourceLocked observes the source file's current metadata through
// the configured filesystem hook - VFS-aware, never os.Stat directly.
// Returns ErrNotSupported when the hook has no metadata support; the
// application can still volunteer metadata via ReportSourceMetadata.
// Caller must hold at least the read lock.
func (g *Garland) statSourceLocked() (FileMetadata, error) {
	if g.sourcePath == "" {
		return FileMetadata{}, ErrNoDataSource
	}
	fs := g.sourceFS
	if fs == nil && g.lib != nil {
		fs = g.lib.defaultFS
	}
	if fs == nil {
		return FileMetadata{}, ErrNotSupported
	}
	return fs.Stat(g.sourcePath)
}

// captureSourceInfo (re-)baselines change detection at the file's
// current metadata: buffer and file are known to agree right now
// (open, save, source adoption, user-acknowledged reload).
func (g *Garland) captureSourceInfo() error {
	if g.sourcePath == "" {
		return nil
	}

	meta, err := g.statSourceLocked()
	if err == ErrNotSupported {
		// No metadata from this filesystem: tracking stays off until
		// the application volunteers a baseline (ReportSourceMetadata).
		return nil
	}
	if err != nil {
		return err
	}
	if !meta.Exists {
		return os.ErrNotExist
	}

	g.setSourceBaselineLocked(meta)
	return nil
}

// setSourceBaselineLocked installs meta as the agreement baseline and
// the freshest observation.
func (g *Garland) setSourceBaselineLocked(meta FileMetadata) {
	st := g.sourceState
	st.originalMtime = meta.ModTime
	st.originalSize = meta.Size
	st.originalIdentity = meta.Identity
	st.metaTracked = true
	st.observedMeta = meta
	st.observedAt = time.Now()
	st.observedValid = true
}

// classifySourceMeta compares an observation against the baseline
// WITHOUT mutating any state. Pure classification, shared by the
// stat-driven and volunteered paths.
func (g *Garland) classifySourceMeta(meta FileMetadata) SourceChangeInfo {
	st := g.sourceState
	result := SourceChangeInfo{
		Type:         SourceUnchanged,
		PreviousSize: st.originalSize,
		CurrentSize:  meta.Size,
	}

	if !meta.Exists {
		result.Type = SourceDeleted
		result.CurrentSize = 0
		return result
	}

	// File replacement: the path is bound to a different object.
	if st.originalIdentity != "" && meta.Identity != "" &&
		st.originalIdentity != meta.Identity {
		result.Type = SourceReplaced
		return result
	}

	if meta.Size < st.originalSize {
		result.Type = SourceTruncated
		return result
	}

	if meta.Size > st.originalSize {
		result.Type = SourceAppended
		result.AppendedBytes = meta.Size - st.originalSize
		return result
	}

	if !meta.ModTime.Equal(st.originalMtime) {
		result.Type = SourceModified
		return result
	}

	return result
}

// absorbSourceObservationLocked records an observation (stat result or
// volunteered metadata), classifies it against the baseline, and
// updates the change counter / status accordingly. If no baseline was
// ever captured (metadata-less VFS), the first observation BECOMES the
// baseline. Caller must hold the write lock.
func (g *Garland) absorbSourceObservationLocked(meta FileMetadata) SourceChangeInfo {
	st := g.sourceState

	if !st.metaTracked {
		if meta.Exists {
			g.setSourceBaselineLocked(meta)
		}
		return SourceChangeInfo{Type: SourceUnchanged,
			PreviousSize: meta.Size, CurrentSize: meta.Size}
	}

	st.observedMeta = meta
	st.observedAt = time.Now()
	st.observedValid = true

	info := g.classifySourceMeta(meta)
	switch info.Type {
	case SourceUnchanged:
		// nothing to record
	case SourceAppended, SourceModified:
		g.incrementChangeCounter()
		st.status = SourceStatusSuspectChange
	default: // Replaced, Truncated, Deleted
		g.incrementChangeCounter()
	}
	return info
}

// CheckSourceMetadata performs a cheap metadata check on the source file.
// This only stats the file, it doesn't read any content.
func (g *Garland) CheckSourceMetadata() (SourceChangeInfo, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.checkSourceMetadataUnlocked()
}

func (g *Garland) checkSourceMetadataUnlocked() (SourceChangeInfo, error) {
	if g.sourcePath == "" {
		return SourceChangeInfo{Type: SourceUnchanged}, nil
	}

	if g.sourceState == nil {
		return SourceChangeInfo{Type: SourceUnchanged}, nil
	}

	meta, err := g.statSourceLocked()
	if err == ErrNotSupported {
		// Metadata-less filesystem: report from the last volunteered
		// observation, if any.
		if g.sourceState.metaTracked && g.sourceState.observedValid {
			return g.classifySourceMeta(g.sourceState.observedMeta), nil
		}
		return SourceChangeInfo{Type: SourceUnchanged}, ErrNotSupported
	}
	if err != nil {
		return SourceChangeInfo{}, err
	}

	return g.absorbSourceObservationLocked(meta), nil
}

// incrementChangeCounter bumps the change counter and records the time.
func (g *Garland) incrementChangeCounter() {
	g.sourceState.changeCounter++
	g.sourceState.lastChangeTime = time.Now()
}

// SourceStatus returns the current source change status.
func (g *Garland) SourceStatus() SourceChangeStatus {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.sourceState == nil {
		return SourceStatusNormal
	}
	return g.sourceState.status
}

// SourcePath returns the path to the source file, if any.
func (g *Garland) SourcePath() string {
	return g.sourcePath
}

// SetAppendPolicy sets the policy for handling detected appends.
func (g *Garland) SetAppendPolicy(policy AppendPolicy) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.sourceState != nil {
		g.sourceState.appendPolicy = policy
	}
}

// SetVerifyOnRead sets whether warm storage reads should verify checksums.
func (g *Garland) SetVerifyOnRead(enabled bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.sourceState != nil {
		g.sourceState.verifyOnRead = enabled
	}
}

// SetSourceChangeHandler sets a callback for source file changes.
func (g *Garland) SetSourceChangeHandler(handler SourceChangeHandler) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.sourceState != nil {
		g.sourceState.changeHandler = handler
	}
}

// getWarmTrustLevel returns the trust level for a specific leaf's warm storage.
func (g *Garland) getWarmTrustLevel(nodeID NodeID) WarmTrustLevel {
	if g.sourceState == nil {
		return WarmTrustFull
	}

	// No changes ever detected
	if g.sourceState.changeCounter == 0 {
		return WarmTrustFull
	}

	// User has been notified but hasn't responded
	if g.sourceState.userNotifiedPending {
		return WarmTrustSuspended
	}

	// Check if this block has been verified since last change
	verification := g.warmVerification[nodeID]
	if verification != nil && verification.verifiedAtCounter >= g.sourceState.changeCounter {
		return WarmTrustVerified
	}

	return WarmTrustStale
}

// updateWarmVerification records that a block was verified.
func (g *Garland) updateWarmVerification(nodeID NodeID) {
	if g.sourceState == nil {
		return
	}

	g.warmVerification[nodeID] = &warmVerificationState{
		verifiedAtCounter: g.sourceState.changeCounter,
		verifiedTime:      time.Now(),
	}
}

// VerifyBoundaryForAppend verifies just the boundary block to confirm an append.
// Returns nil if the boundary is intact (safe to treat as append).
func (g *Garland) VerifyBoundaryForAppend() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.verifyBoundaryForAppendUnlocked()
}

func (g *Garland) verifyBoundaryForAppendUnlocked() error {
	if g.sourceState == nil || g.sourcePath == "" {
		return nil
	}

	// Find the warm leaf whose range includes the last byte of original content
	boundaryPos := g.sourceState.originalSize - 1
	if boundaryPos < 0 {
		// Empty file - no boundary to verify
		return nil
	}

	// Find the leaf at the boundary position
	leaf, err := g.findLeafByByteUnlocked(boundaryPos)
	if err != nil {
		return err
	}

	snap := leaf.Node.snapshotAt(g.currentFork, g.currentRevision)
	if snap == nil {
		return nil
	}

	// If it's in memory, we already know it's correct
	if snap.storageState == StorageMemory {
		return nil
	}

	// If it's warm storage, verify it
	if snap.storageState == StorageWarm && snap.originalFileOffset >= 0 {
		return g.verifyWarmBlock(leaf.Node.id, snap)
	}

	// Cold storage or other - no verification needed for append detection
	return nil
}

// verifyWarmBlock reads a warm block from disk and verifies its checksum.
func (g *Garland) verifyWarmBlock(nodeID NodeID, snap *NodeSnapshot) error {
	if g.sourceHandle == nil || g.sourceFS == nil {
		return ErrWarmStorageMismatch
	}

	// Seek to the original position
	err := g.sourceFS.SeekByte(g.sourceHandle, snap.originalFileOffset)
	if err != nil {
		return err
	}

	// Read the data
	data, err := g.sourceFS.ReadBytes(g.sourceHandle, int(snap.byteCount))
	if err != nil {
		return err
	}

	// Verify hash
	if len(snap.dataHash) > 0 {
		actualHash := computeHash(data)
		if !hashesEqual(snap.dataHash, actualHash) {
			return ErrWarmStorageMismatch
		}
	}

	// Verification passed - update tracking
	g.updateWarmVerification(nodeID)

	return nil
}

// LoadAppendedContent loads newly appended content from the source file.
// Only valid after VerifyBoundaryForAppend succeeds.
func (g *Garland) LoadAppendedContent() (int64, error) {
	// Phase 1 (locked): observe the file and read the appended tail.
	// The lock is NOT held across the cursor operations below - cursor
	// APIs take the lock themselves (holding it here would deadlock).
	g.mu.Lock()
	if g.sourceState == nil || g.sourcePath == "" {
		g.mu.Unlock()
		return 0, nil
	}

	meta, err := g.statSourceLocked()
	if err != nil {
		g.mu.Unlock()
		return 0, err
	}
	if !meta.Exists {
		g.mu.Unlock()
		return 0, os.ErrNotExist
	}

	appendedBytes := meta.Size - g.sourceState.originalSize
	if appendedBytes <= 0 {
		g.mu.Unlock()
		return 0, nil
	}

	if g.sourceHandle == nil || g.sourceFS == nil {
		g.mu.Unlock()
		return 0, ErrWarmStorageMismatch
	}

	if err := g.sourceFS.SeekByte(g.sourceHandle, g.sourceState.originalSize); err != nil {
		g.mu.Unlock()
		return 0, err
	}
	data, err := g.sourceFS.ReadBytes(g.sourceHandle, int(appendedBytes))
	if err != nil {
		g.mu.Unlock()
		return 0, err
	}
	endPos := g.totalBytes
	g.mu.Unlock()

	// Phase 2 (unlocked): append via a cursor. Ephemeral - a tail-load
	// utility cursor has no business in undo history.
	cursor := g.NewEphemeralCursor()
	defer g.RemoveCursor(cursor)

	if err := cursor.SeekByte(endPos); err != nil {
		return 0, err
	}
	if _, err := cursor.InsertBytes(data, nil, false); err != nil {
		return 0, err
	}

	// Phase 3 (locked): the appended tail is now part of the buffer -
	// advance the agreement baseline to the file state we just loaded.
	g.mu.Lock()
	g.setSourceBaselineLocked(meta)
	g.sourceState.status = SourceStatusNormal
	g.sourceState.appendAvailableBytes = 0
	g.mu.Unlock()

	return int64(len(data)), nil
}

// EnableSourceWatch starts periodic monitoring of the source file.
func (g *Garland) EnableSourceWatch(interval time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.sourceState == nil || g.sourcePath == "" {
		return
	}

	if g.sourceState.watchEnabled {
		return // Already watching
	}

	g.sourceState.watchEnabled = true
	g.sourceState.watchStop = make(chan struct{})
	g.sourceState.watchWg.Add(1)

	go func() {
		defer g.sourceState.watchWg.Done()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-g.sourceState.watchStop:
				return
			case <-ticker.C:
				g.checkSourceAndNotify()
			}
		}
	}()
}

// DisableSourceWatch stops periodic monitoring of the source file.
func (g *Garland) DisableSourceWatch() {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.sourceState == nil || !g.sourceState.watchEnabled {
		return
	}

	close(g.sourceState.watchStop)
	g.sourceState.watchWg.Wait()
	g.sourceState.watchEnabled = false
}

// checkSourceAndNotify checks for source changes and notifies the handler.
func (g *Garland) checkSourceAndNotify() {
	g.mu.Lock()

	info, err := g.checkSourceMetadataUnlocked()
	if err != nil {
		g.mu.Unlock()
		return
	}

	if info.Type == SourceUnchanged {
		g.mu.Unlock()
		return
	}

	// Handle append specially
	if info.Type == SourceAppended {
		// Verify boundary
		if err := g.verifyBoundaryForAppendUnlocked(); err == nil {
			g.sourceState.status = SourceStatusAppendAvailable
			g.sourceState.appendAvailableBytes = info.AppendedBytes

			// Auto-load if continuous mode
			if g.sourceState.appendPolicy == AppendPolicyContinuous {
				g.mu.Unlock()
				g.LoadAppendedContent()
				return
			}
		} else {
			// Boundary verification failed - treat as modification
			info.Type = SourceModified
			g.sourceState.status = SourceStatusSuspectChange
		}
	}

	handler := g.sourceState.changeHandler
	status := g.sourceState.status
	g.mu.Unlock()

	// Call handler outside of lock
	if handler != nil {
		handler(g, status, info)
	}
}

// AcknowledgeSourceChange acknowledges a detected source change.
// Call this after the user has been notified and made a decision.
func (g *Garland) AcknowledgeSourceChange(reload bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.sourceState == nil {
		return nil
	}

	g.sourceState.userNotifiedPending = false

	if reload {
		// User wants to reload - this would need a full reload implementation
		// For now, just reset state
		return g.captureSourceInfo()
	}

	// User wants to keep their version
	// Reset change counter so warm storage becomes trusted again
	g.sourceState.changeCounter = 0
	g.sourceState.status = SourceStatusNormal

	return nil
}

// RefreshSourceInfo updates stored metadata after a save.
func (g *Garland) RefreshSourceInfo() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.captureSourceInfo()
}
