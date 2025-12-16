package garland

// AddressMode specifies how a position is interpreted.
type AddressMode int

const (
	// ByteMode specifies an absolute byte position (0-indexed).
	ByteMode AddressMode = iota

	// RuneMode specifies an absolute rune/Unicode code point position (0-indexed).
	RuneMode

	// LineRuneMode specifies a line number and rune position within that line (both 0-indexed).
	// The newline character is considered the last character of its line.
	LineRuneMode
)

// AbsoluteAddress specifies a position using one of three addressing modes.
type AbsoluteAddress struct {
	Mode AddressMode

	// Byte is used when Mode is ByteMode.
	Byte int64

	// Rune is used when Mode is RuneMode.
	Rune int64

	// Line is used when Mode is LineRuneMode (0-indexed line number).
	Line int64

	// LineRune is used when Mode is LineRuneMode (0-indexed rune within line).
	LineRune int64
}

// ByteAddress creates an AbsoluteAddress in byte mode.
func ByteAddress(pos int64) AbsoluteAddress {
	return AbsoluteAddress{
		Mode: ByteMode,
		Byte: pos,
	}
}

// RuneAddress creates an AbsoluteAddress in rune mode.
func RuneAddress(pos int64) AbsoluteAddress {
	return AbsoluteAddress{
		Mode: RuneMode,
		Rune: pos,
	}
}

// LineAddress creates an AbsoluteAddress in line:rune mode.
func LineAddress(line, runeInLine int64) AbsoluteAddress {
	return AbsoluteAddress{
		Mode:     LineRuneMode,
		Line:     line,
		LineRune: runeInLine,
	}
}

// LineStart represents the starting position of a line within a leaf node.
type LineStart struct {
	// ByteOffset is the byte offset from the start of the node where this line begins.
	ByteOffset int64

	// RuneOffset is the rune offset from the start of the node where this line begins.
	RuneOffset int64
}

// RelativeDecoration specifies a decoration position relative to an insert point.
// Position semantics:
//   - < 0      : attach after the newline preceding the insert point
//   - 0 to Len : attach to the byte/rune at that offset in inserted content
//   - Len + 1  : attach before the newline following the insert point
//   - > Len + 1: attach after the newline following the insert point
type RelativeDecoration struct {
	Key      string
	Position int64
}

// DecorationEntry represents a decoration with its absolute position.
type DecorationEntry struct {
	Key     string
	Address *AbsoluteAddress // nil to delete the decoration
}

// Decoration represents a decoration stored within a node.
type Decoration struct {
	Key      string
	Position int64 // relative byte offset within the node
}
