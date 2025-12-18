package garland

import (
	"sync/atomic"
	"unicode/utf8"
)

// regionSerialCounter is a global counter for assigning unique serial numbers to regions.
var regionSerialCounter uint64

// nextRegionSerial returns the next unique serial number for a region.
func nextRegionSerial() uint64 {
	return atomic.AddUint64(&regionSerialCounter, 1)
}

// CursorMode determines how a cursor interacts with optimized regions.
type CursorMode int

const (
	// CursorModeHuman automatically creates optimized regions on edit operations.
	// This is the default mode for interactive editing.
	CursorModeHuman CursorMode = iota

	// CursorModeProcess does not auto-create regions; uses transactions instead.
	// Use this mode for programmatic/scripted operations.
	CursorModeProcess
)

// OptimizedRegionHandle tracks an active optimized region attached to a cursor.
type OptimizedRegionHandle struct {
	serial uint64 // unique serial number for debugging

	// Grace window bounds (fixed at creation, in document coordinates)
	graceStart int64
	graceEnd   int64

	// Current content bounds (in document coordinates, changes as content grows/shrinks)
	contentStart int64

	// The region buffer
	buffer *ByteBufferRegion

	// Decorations within this region (positions relative to contentStart)
	decorations []Decoration

	// Owner cursor
	cursor *Cursor
}

// Serial returns the unique serial number of this region for debugging.
func (h *OptimizedRegionHandle) Serial() uint64 {
	return h.serial
}

// GraceWindow returns the grace window bounds (document coordinates).
func (h *OptimizedRegionHandle) GraceWindow() (start, end int64) {
	return h.graceStart, h.graceEnd
}

// ContentBounds returns the current content bounds (document coordinates).
func (h *OptimizedRegionHandle) ContentBounds() (start, end int64) {
	return h.contentStart, h.contentStart + h.buffer.ByteCount()
}

// ByteCount returns the current byte count of the region content.
func (h *OptimizedRegionHandle) ByteCount() int64 {
	return h.buffer.ByteCount()
}

// adjustDecorationsForInsert slides decorations after an insert.
func (h *OptimizedRegionHandle) adjustDecorationsForInsert(offset, insertLen int64, insertBefore bool) {
	for i := range h.decorations {
		if h.decorations[i].Position > offset ||
			(h.decorations[i].Position == offset && insertBefore) {
			h.decorations[i].Position += insertLen
		}
	}
}

// adjustDecorationsForDelete removes decorations in deleted range and slides others.
func (h *OptimizedRegionHandle) adjustDecorationsForDelete(offset, deleteLen int64) []Decoration {
	var removed []Decoration
	kept := h.decorations[:0]
	for _, d := range h.decorations {
		if d.Position < offset {
			kept = append(kept, d)
		} else if d.Position >= offset+deleteLen {
			kept = append(kept, Decoration{
				Key:      d.Key,
				Position: d.Position - deleteLen,
			})
		} else {
			// Decoration in deleted range
			removed = append(removed, d)
		}
	}
	h.decorations = kept
	return removed
}

// ByteBufferRegion is a simple OptimizedRegion implementation using a byte slice.
// It's suitable for small to medium editing sessions.
type ByteBufferRegion struct {
	data      []byte
	runeCount int64
	lineCount int64
}

// NewByteBufferRegion creates a new ByteBufferRegion with initial content.
func NewByteBufferRegion(initialContent []byte) *ByteBufferRegion {
	r := &ByteBufferRegion{
		data: make([]byte, len(initialContent)),
	}
	copy(r.data, initialContent)
	r.recalculateCounts()
	return r
}

// recalculateCounts updates rune and line counts from the data.
func (r *ByteBufferRegion) recalculateCounts() {
	r.runeCount = int64(utf8.RuneCount(r.data))
	r.lineCount = 0
	for _, b := range r.data {
		if b == '\n' {
			r.lineCount++
		}
	}
}

// ByteCount returns the number of bytes in the region.
func (r *ByteBufferRegion) ByteCount() int64 {
	return int64(len(r.data))
}

// RuneCount returns the number of runes in the region.
func (r *ByteBufferRegion) RuneCount() int64 {
	return r.runeCount
}

// LineCount returns the number of newlines in the region.
func (r *ByteBufferRegion) LineCount() int64 {
	return r.lineCount
}

// InsertBytes inserts data at the given offset.
func (r *ByteBufferRegion) InsertBytes(offset int64, data []byte) error {
	if offset < 0 || offset > int64(len(r.data)) {
		return ErrInvalidPosition
	}

	// Count runes and lines in inserted data
	insertedRunes := int64(utf8.RuneCount(data))
	insertedLines := int64(0)
	for _, b := range data {
		if b == '\n' {
			insertedLines++
		}
	}

	// Insert into buffer
	newData := make([]byte, len(r.data)+len(data))
	copy(newData, r.data[:offset])
	copy(newData[offset:], data)
	copy(newData[offset+int64(len(data)):], r.data[offset:])
	r.data = newData

	r.runeCount += insertedRunes
	r.lineCount += insertedLines

	return nil
}

// DeleteBytes deletes length bytes starting at offset.
func (r *ByteBufferRegion) DeleteBytes(offset, length int64) error {
	if offset < 0 || length < 0 || offset+length > int64(len(r.data)) {
		return ErrInvalidPosition
	}

	// Count runes and lines in deleted data
	deletedData := r.data[offset : offset+length]
	deletedRunes := int64(utf8.RuneCount(deletedData))
	deletedLines := int64(0)
	for _, b := range deletedData {
		if b == '\n' {
			deletedLines++
		}
	}

	// Delete from buffer
	newData := make([]byte, len(r.data)-int(length))
	copy(newData, r.data[:offset])
	copy(newData[offset:], r.data[offset+length:])
	r.data = newData

	r.runeCount -= deletedRunes
	r.lineCount -= deletedLines

	return nil
}

// ReadBytes reads length bytes starting at offset.
func (r *ByteBufferRegion) ReadBytes(offset, length int64) ([]byte, error) {
	if offset < 0 || length < 0 || offset+length > int64(len(r.data)) {
		return nil, ErrInvalidPosition
	}
	result := make([]byte, length)
	copy(result, r.data[offset:offset+length])
	return result, nil
}

// Content returns the full content of the region.
func (r *ByteBufferRegion) Content() []byte {
	result := make([]byte, len(r.data))
	copy(result, r.data)
	return result
}
