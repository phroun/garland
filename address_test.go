package garland

import "testing"

func TestByteAddress(t *testing.T) {
	addr := ByteAddress(12345)

	if addr.Mode != ByteMode {
		t.Errorf("Mode = %d, want ByteMode (%d)", addr.Mode, ByteMode)
	}
	if addr.Byte != 12345 {
		t.Errorf("Byte = %d, want 12345", addr.Byte)
	}
}

func TestRuneAddress(t *testing.T) {
	addr := RuneAddress(67890)

	if addr.Mode != RuneMode {
		t.Errorf("Mode = %d, want RuneMode (%d)", addr.Mode, RuneMode)
	}
	if addr.Rune != 67890 {
		t.Errorf("Rune = %d, want 67890", addr.Rune)
	}
}

func TestLineAddress(t *testing.T) {
	addr := LineAddress(100, 50)

	if addr.Mode != LineRuneMode {
		t.Errorf("Mode = %d, want LineRuneMode (%d)", addr.Mode, LineRuneMode)
	}
	if addr.Line != 100 {
		t.Errorf("Line = %d, want 100", addr.Line)
	}
	if addr.LineRune != 50 {
		t.Errorf("LineRune = %d, want 50", addr.LineRune)
	}
}

func TestAbsoluteAddressStruct(t *testing.T) {
	// Test manual construction
	addr := AbsoluteAddress{
		Mode:     LineRuneMode,
		Byte:     0,    // not used for this mode
		Rune:     0,    // not used for this mode
		Line:     10,
		LineRune: 5,
	}

	if addr.Mode != LineRuneMode {
		t.Error("Mode should be LineRuneMode")
	}
	if addr.Line != 10 || addr.LineRune != 5 {
		t.Error("Line and LineRune should be set correctly")
	}
}

func TestAddressModeConstants(t *testing.T) {
	// Ensure constants are distinct
	modes := []AddressMode{ByteMode, RuneMode, LineRuneMode}
	seen := make(map[AddressMode]bool)

	for _, mode := range modes {
		if seen[mode] {
			t.Errorf("Duplicate AddressMode value: %d", mode)
		}
		seen[mode] = true
	}
}

func TestLineStart(t *testing.T) {
	ls := LineStart{
		ByteOffset: 100,
		RuneOffset: 80,
	}

	if ls.ByteOffset != 100 {
		t.Errorf("ByteOffset = %d, want 100", ls.ByteOffset)
	}
	if ls.RuneOffset != 80 {
		t.Errorf("RuneOffset = %d, want 80", ls.RuneOffset)
	}
}

func TestRelativeDecoration(t *testing.T) {
	rd := RelativeDecoration{
		Key:      "test-decoration",
		Position: -1, // before insert point
	}

	if rd.Key != "test-decoration" {
		t.Errorf("Key = %q, want %q", rd.Key, "test-decoration")
	}
	if rd.Position != -1 {
		t.Errorf("Position = %d, want -1", rd.Position)
	}
}

func TestRelativeDecorationPositionSemantics(t *testing.T) {
	tests := []struct {
		position int64
		desc     string
	}{
		{-100, "far before insert point (attach to line start)"},
		{-1, "just before insert point (attach to line start)"},
		{0, "at insert point start"},
		{5, "within inserted content"},
		{10, "at end of inserted content (assuming Len=10)"},
		{11, "just after insert point (attach to line end)"},
		{100, "far after insert point (attach after next newline)"},
	}

	for _, tt := range tests {
		rd := RelativeDecoration{Key: "test", Position: tt.position}
		if rd.Position != tt.position {
			t.Errorf("Position for %s: got %d, want %d", tt.desc, rd.Position, tt.position)
		}
	}
}

func TestDecorationEntry(t *testing.T) {
	// With address (add/update)
	addr := ByteAddress(100)
	entry := DecorationEntry{
		Key:     "bookmark",
		Address: &addr,
	}

	if entry.Key != "bookmark" {
		t.Errorf("Key = %q, want %q", entry.Key, "bookmark")
	}
	if entry.Address == nil {
		t.Error("Address should not be nil")
	}
	if entry.Address.Byte != 100 {
		t.Errorf("Address.Byte = %d, want 100", entry.Address.Byte)
	}

	// Nil address (delete)
	deleteEntry := DecorationEntry{
		Key:     "to-delete",
		Address: nil,
	}

	if deleteEntry.Address != nil {
		t.Error("Address should be nil for delete entry")
	}
}

func TestDecoration(t *testing.T) {
	d := Decoration{
		Key:      "syntax-highlight",
		Position: 42,
	}

	if d.Key != "syntax-highlight" {
		t.Errorf("Key = %q, want %q", d.Key, "syntax-highlight")
	}
	if d.Position != 42 {
		t.Errorf("Position = %d, want 42", d.Position)
	}
}

func TestAddressModeZeroValue(t *testing.T) {
	// Zero value should be ByteMode
	var mode AddressMode
	if mode != ByteMode {
		t.Errorf("Zero value of AddressMode = %d, want ByteMode (%d)", mode, ByteMode)
	}
}

func TestAbsoluteAddressZeroValue(t *testing.T) {
	// Zero value should be ByteMode at position 0
	var addr AbsoluteAddress
	if addr.Mode != ByteMode {
		t.Errorf("Zero value Mode = %d, want ByteMode", addr.Mode)
	}
	if addr.Byte != 0 {
		t.Errorf("Zero value Byte = %d, want 0", addr.Byte)
	}
}

func TestMultipleAddressCreation(t *testing.T) {
	// Create multiple addresses and ensure they're independent
	a1 := ByteAddress(10)
	a2 := ByteAddress(20)
	a3 := RuneAddress(30)

	if a1.Byte == a2.Byte {
		t.Error("Different addresses should have different values")
	}
	if a1.Mode != a2.Mode {
		t.Error("Both should be ByteMode")
	}
	if a1.Mode == a3.Mode {
		t.Error("a1 (Byte) and a3 (Rune) should have different modes")
	}
}
