// Package garland provides a rope-based data structure for efficient text/binary editing
// with multiple storage tiers, versioning, decorations, and lazy loading.
package garland

import "errors"

// Position errors
var (
	// ErrNotReady indicates that a position is not yet available during lazy loading.
	ErrNotReady = errors.New("position not yet available")

	// ErrInvalidPosition indicates that a position is out of bounds.
	ErrInvalidPosition = errors.New("position out of bounds")

	// ErrTimeout indicates that a blocking wait operation timed out.
	ErrTimeout = errors.New("operation timed out")

	// ErrInvalidUTF8 indicates that an operation would split a UTF-8 sequence.
	ErrInvalidUTF8 = errors.New("invalid UTF-8 sequence")

	// ErrOverlappingRanges indicates that source and destination ranges overlap
	// in an operation that doesn't allow overlap (e.g., Move).
	ErrOverlappingRanges = errors.New("source and destination ranges overlap")
)

// Decoration errors
var (
	// ErrDecorationNotFound indicates that a decoration key does not exist.
	ErrDecorationNotFound = errors.New("decoration not found")
)

// Versioning errors
var (
	// ErrForkNotFound indicates that a fork ID does not exist.
	ErrForkNotFound = errors.New("fork not found")

	// ErrRevisionNotFound indicates that a revision does not exist in the current fork.
	ErrRevisionNotFound = errors.New("revision not found")
)

// Storage errors
var (
	// ErrColdStorageFailure indicates that a cold storage operation failed.
	ErrColdStorageFailure = errors.New("cold storage operation failed")

	// ErrWarmStorageMismatch indicates that the original file has changed (checksum mismatch).
	ErrWarmStorageMismatch = errors.New("warm storage checksum mismatch")

	// ErrReadOnly indicates that a region is read-only due to storage failure.
	ErrReadOnly = errors.New("region is read-only due to storage failure")

	// ErrNotFromOriginalFile indicates warm storage is not available for this node.
	ErrNotFromOriginalFile = errors.New("node is not from original file")
)

// File system errors
var (
	// ErrNotSupported indicates that an optional file system operation is not supported.
	ErrNotSupported = errors.New("operation not supported")

	// ErrFileNotOpen indicates that the file handle is not open.
	ErrFileNotOpen = errors.New("file not open")
)

// Region errors
var (
	// ErrRegionOverlap indicates that optimized regions cannot overlap.
	ErrRegionOverlap = errors.New("optimized regions cannot overlap")

	// ErrRegionNotFound indicates that the optimized region does not exist.
	ErrRegionNotFound = errors.New("optimized region not found")
)

// Transaction errors
var (
	// ErrTransactionPending indicates that an operation is not allowed during a transaction.
	ErrTransactionPending = errors.New("operation not allowed during transaction")

	// ErrTransactionPoisoned indicates that a transaction was poisoned by an inner rollback.
	ErrTransactionPoisoned = errors.New("transaction was poisoned by inner rollback")

	// ErrNoTransaction indicates that there is no active transaction.
	ErrNoTransaction = errors.New("no active transaction")
)

// Cursor errors
var (
	// ErrCursorNotFound indicates that the cursor does not belong to this garland.
	ErrCursorNotFound = errors.New("cursor not found")
)

// Tree structure errors
var (
	// ErrNotALeaf indicates that an operation expected a leaf node but got an internal node.
	ErrNotALeaf = errors.New("expected leaf node")

	// ErrInternal indicates an internal consistency error (should not happen).
	ErrInternal = errors.New("internal error")
)

// Configuration errors
var (
	// ErrNoDataSource indicates that no data source was provided in FileOptions.
	ErrNoDataSource = errors.New("no data source provided")

	// ErrMultipleDataSources indicates that multiple data sources were provided.
	ErrMultipleDataSources = errors.New("multiple data sources provided")

	// ErrNoColdStorage indicates that cold storage is required but not configured.
	ErrNoColdStorage = errors.New("cold storage not configured")

	// ErrDataNotLoaded indicates that data is in cold/warm storage and needs to be thawed.
	ErrDataNotLoaded = errors.New("data not loaded - call Thaw() first")
)
