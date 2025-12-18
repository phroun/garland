package garland

import (
	"testing"
)

// Helper function to create test garland
func newTestGarland(t *testing.T, content string) (*Garland, *Cursor) {
	t.Helper()
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}
	g, err := lib.Open(FileOptions{DataString: content})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	cursor := g.NewCursor()
	return g, cursor
}

// ====================
// String Search Tests
// ====================

func TestFindStringBasic(t *testing.T) {
	g, cursor := newTestGarland(t, "hello world hello")
	defer g.Close()

	// Find forward from start
	result, err := cursor.FindString("hello", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("FindString error: %v", err)
	}
	if result == nil {
		t.Fatal("Expected match, got nil")
	}
	if result.ByteStart != 0 || result.ByteEnd != 5 {
		t.Errorf("Expected match at 0-5, got %d-%d", result.ByteStart, result.ByteEnd)
	}
	if result.Match != "hello" {
		t.Errorf("Expected match 'hello', got %q", result.Match)
	}

	// Find from middle - should find second occurrence
	cursor.SeekByte(6)
	result, err = cursor.FindString("hello", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("FindString error: %v", err)
	}
	if result == nil {
		t.Fatal("Expected match, got nil")
	}
	if result.ByteStart != 12 {
		t.Errorf("Expected match at byte 12, got %d", result.ByteStart)
	}
}

func TestFindStringCaseInsensitive(t *testing.T) {
	g, cursor := newTestGarland(t, "Hello World HELLO")
	defer g.Close()

	// Case sensitive - should find "Hello"
	result, err := cursor.FindString("hello", SearchOptions{CaseSensitive: false})
	if err != nil {
		t.Fatalf("FindString error: %v", err)
	}
	if result == nil {
		t.Fatal("Expected match, got nil")
	}
	if result.ByteStart != 0 {
		t.Errorf("Expected match at byte 0, got %d", result.ByteStart)
	}

	// Find all case-insensitive matches
	matches, err := cursor.FindStringAll("hello", SearchOptions{CaseSensitive: false})
	if err != nil {
		t.Fatalf("FindStringAll error: %v", err)
	}
	if len(matches) != 2 {
		t.Errorf("Expected 2 matches, got %d", len(matches))
	}
}

func TestFindStringWholeWord(t *testing.T) {
	g, cursor := newTestGarland(t, "hello unhello helloworld hello")
	defer g.Close()

	// Without whole word - should find first occurrence
	result, err := cursor.FindString("hello", SearchOptions{CaseSensitive: true, WholeWord: false})
	if err != nil {
		t.Fatalf("FindString error: %v", err)
	}
	if result == nil || result.ByteStart != 0 {
		t.Error("Expected match at byte 0 without whole word")
	}

	// With whole word - should find first "hello" (standalone)
	cursor.SeekByte(0)
	result, err = cursor.FindString("hello", SearchOptions{CaseSensitive: true, WholeWord: true})
	if err != nil {
		t.Fatalf("FindString error: %v", err)
	}
	if result == nil {
		t.Fatal("Expected match, got nil")
	}
	if result.ByteStart != 0 {
		t.Errorf("Expected first whole-word match at 0, got %d", result.ByteStart)
	}

	// Find all whole-word matches
	matches, err := cursor.FindStringAll("hello", SearchOptions{CaseSensitive: true, WholeWord: true})
	if err != nil {
		t.Fatalf("FindStringAll error: %v", err)
	}
	// Should find: "hello" at 0 and "hello" at 25
	if len(matches) != 2 {
		t.Errorf("Expected 2 whole-word matches, got %d", len(matches))
		for i, m := range matches {
			t.Logf("  Match %d: %d-%d %q", i, m.ByteStart, m.ByteEnd, m.Match)
		}
	}
}

func TestFindStringBackward(t *testing.T) {
	g, cursor := newTestGarland(t, "hello world hello universe hello")
	defer g.Close()

	// Search backward from end
	cursor.SeekByte(32) // end of content
	result, err := cursor.FindString("hello", SearchOptions{CaseSensitive: true, Backward: true})
	if err != nil {
		t.Fatalf("FindString error: %v", err)
	}
	if result == nil {
		t.Fatal("Expected match, got nil")
	}
	// Should find the last "hello" before position 32
	if result.ByteStart != 27 {
		t.Errorf("Expected backward match at 27, got %d", result.ByteStart)
	}

	// Search backward from middle
	cursor.SeekByte(20)
	result, err = cursor.FindString("hello", SearchOptions{CaseSensitive: true, Backward: true})
	if err != nil {
		t.Fatalf("FindString error: %v", err)
	}
	if result == nil {
		t.Fatal("Expected match, got nil")
	}
	if result.ByteStart != 12 {
		t.Errorf("Expected backward match at 12, got %d", result.ByteStart)
	}
}

func TestFindStringNotFound(t *testing.T) {
	g, cursor := newTestGarland(t, "hello world")
	defer g.Close()

	result, err := cursor.FindString("xyz", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("FindString error: %v", err)
	}
	if result != nil {
		t.Errorf("Expected nil, got match at %d", result.ByteStart)
	}
}

func TestFindStringAll(t *testing.T) {
	g, cursor := newTestGarland(t, "cat dog cat bird cat")
	defer g.Close()

	matches, err := cursor.FindStringAll("cat", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("FindStringAll error: %v", err)
	}
	if len(matches) != 3 {
		t.Errorf("Expected 3 matches, got %d", len(matches))
	}

	// Verify positions
	expected := []int64{0, 8, 17}
	for i, match := range matches {
		if match.ByteStart != expected[i] {
			t.Errorf("Match %d: expected start %d, got %d", i, expected[i], match.ByteStart)
		}
	}
}

func TestCountString(t *testing.T) {
	g, cursor := newTestGarland(t, "the quick brown fox the lazy dog the")
	defer g.Close()

	count, err := cursor.CountString("the", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("CountString error: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected 3, got %d", count)
	}

	// With whole word
	count, err = cursor.CountString("the", SearchOptions{CaseSensitive: true, WholeWord: true})
	if err != nil {
		t.Fatalf("CountString error: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected 3 whole words, got %d", count)
	}
}

func TestFindNext(t *testing.T) {
	g, cursor := newTestGarland(t, "aaa bbb aaa ccc aaa")
	defer g.Close()

	// FindNext should move cursor
	cursor.SeekByte(0)
	match, err := cursor.FindNext("aaa", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("FindNext error: %v", err)
	}
	if match == nil {
		t.Fatal("Expected match")
	}
	// First match is at 0, but FindNext starts at cursor+1, so should find second
	if match.ByteStart != 8 {
		t.Errorf("Expected next match at 8, got %d", match.ByteStart)
	}
	if cursor.BytePos() != 8 {
		t.Errorf("Expected cursor at 8, got %d", cursor.BytePos())
	}

	// Find next again
	match, err = cursor.FindNext("aaa", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("FindNext error: %v", err)
	}
	if match == nil {
		t.Fatal("Expected match")
	}
	if match.ByteStart != 16 {
		t.Errorf("Expected next match at 16, got %d", match.ByteStart)
	}
}

// ====================
// String Replace Tests
// ====================

func TestReplaceString(t *testing.T) {
	g, cursor := newTestGarland(t, "hello world hello")
	defer g.Close()

	replaced, result, err := cursor.ReplaceString("hello", "hi", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("ReplaceString error: %v", err)
	}
	if !replaced {
		t.Error("Expected replacement")
	}
	if result.Revision == 0 {
		t.Error("Expected revision > 0")
	}

	// Verify content
	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	expected := "hi world hello"
	if string(data) != expected {
		t.Errorf("Expected %q, got %q", expected, string(data))
	}
}

func TestReplaceStringAll(t *testing.T) {
	g, cursor := newTestGarland(t, "cat dog cat bird cat")
	defer g.Close()

	count, _, err := cursor.ReplaceStringAll("cat", "kitten", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("ReplaceStringAll error: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected 3 replacements, got %d", count)
	}

	// Verify content
	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	expected := "kitten dog kitten bird kitten"
	if string(data) != expected {
		t.Errorf("Expected %q, got %q", expected, string(data))
	}
}

func TestReplaceStringCount(t *testing.T) {
	g, cursor := newTestGarland(t, "a a a a a")
	defer g.Close()

	count, _, err := cursor.ReplaceStringCount("a", "b", 2, SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("ReplaceStringCount error: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 replacements, got %d", count)
	}

	// Verify content - only first 2 'a's replaced
	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	expected := "b b a a a"
	if string(data) != expected {
		t.Errorf("Expected %q, got %q", expected, string(data))
	}
}

func TestReplaceStringCaseInsensitive(t *testing.T) {
	g, cursor := newTestGarland(t, "Hello HELLO hello")
	defer g.Close()

	count, _, err := cursor.ReplaceStringAll("hello", "hi", SearchOptions{CaseSensitive: false})
	if err != nil {
		t.Fatalf("ReplaceStringAll error: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected 3 replacements, got %d", count)
	}

	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	expected := "hi hi hi"
	if string(data) != expected {
		t.Errorf("Expected %q, got %q", expected, string(data))
	}
}

func TestReplaceStringNoMatch(t *testing.T) {
	g, cursor := newTestGarland(t, "hello world")
	defer g.Close()

	replaced, _, err := cursor.ReplaceString("xyz", "abc", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("ReplaceString error: %v", err)
	}
	if replaced {
		t.Error("Expected no replacement")
	}

	// Content should be unchanged
	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	if string(data) != "hello world" {
		t.Errorf("Content changed unexpectedly: %q", string(data))
	}
}

// ====================
// Regex Search Tests
// ====================

func TestFindRegexBasic(t *testing.T) {
	g, cursor := newTestGarland(t, "hello123world456")
	defer g.Close()

	// Find digits
	result, err := cursor.FindRegex(`\d+`, RegexOptions{})
	if err != nil {
		t.Fatalf("FindRegex error: %v", err)
	}
	if result == nil {
		t.Fatal("Expected match, got nil")
	}
	if result.Match != "123" {
		t.Errorf("Expected '123', got %q", result.Match)
	}
	if result.ByteStart != 5 || result.ByteEnd != 8 {
		t.Errorf("Expected 5-8, got %d-%d", result.ByteStart, result.ByteEnd)
	}
}

func TestFindRegexCaseInsensitive(t *testing.T) {
	g, cursor := newTestGarland(t, "Hello World HELLO")
	defer g.Close()

	// Case sensitive - should not match
	result, err := cursor.FindRegex(`hello`, RegexOptions{CaseInsensitive: false})
	if err != nil {
		t.Fatalf("FindRegex error: %v", err)
	}
	if result != nil {
		t.Error("Expected no match for case-sensitive search")
	}

	// Case insensitive - should match
	result, err = cursor.FindRegex(`hello`, RegexOptions{CaseInsensitive: true})
	if err != nil {
		t.Fatalf("FindRegex error: %v", err)
	}
	if result == nil {
		t.Fatal("Expected match for case-insensitive search")
	}
	if result.ByteStart != 0 {
		t.Errorf("Expected match at 0, got %d", result.ByteStart)
	}
}

func TestFindRegexBackward(t *testing.T) {
	g, cursor := newTestGarland(t, "abc 123 def 456 ghi")
	defer g.Close()

	cursor.SeekByte(19) // end
	result, err := cursor.FindRegex(`\d+`, RegexOptions{Backward: true})
	if err != nil {
		t.Fatalf("FindRegex error: %v", err)
	}
	if result == nil {
		t.Fatal("Expected match")
	}
	if result.Match != "456" {
		t.Errorf("Expected '456', got %q", result.Match)
	}
}

func TestFindRegexAll(t *testing.T) {
	g, cursor := newTestGarland(t, "a1b2c3d4e5")
	defer g.Close()

	matches, err := cursor.FindRegexAll(`\d`, RegexOptions{})
	if err != nil {
		t.Fatalf("FindRegexAll error: %v", err)
	}
	if len(matches) != 5 {
		t.Errorf("Expected 5 matches, got %d", len(matches))
	}

	expected := []string{"1", "2", "3", "4", "5"}
	for i, match := range matches {
		if match.Match != expected[i] {
			t.Errorf("Match %d: expected %q, got %q", i, expected[i], match.Match)
		}
	}
}

func TestMatchRegex(t *testing.T) {
	g, cursor := newTestGarland(t, "hello123world")
	defer g.Close()

	// Match at start - should not match (starts with letters, not digits)
	matches, result, err := cursor.MatchRegex(`\d+`, false)
	if err != nil {
		t.Fatalf("MatchRegex error: %v", err)
	}
	if matches {
		t.Error("Expected no match at position 0")
	}

	// Seek to digits
	cursor.SeekByte(5)
	matches, result, err = cursor.MatchRegex(`\d+`, false)
	if err != nil {
		t.Fatalf("MatchRegex error: %v", err)
	}
	if !matches {
		t.Error("Expected match at position 5")
	}
	if result.Match != "123" {
		t.Errorf("Expected '123', got %q", result.Match)
	}
}

func TestCountRegex(t *testing.T) {
	g, cursor := newTestGarland(t, "the quick 123 brown 456 fox 789")
	defer g.Close()

	count, err := cursor.CountRegex(`\d+`, false)
	if err != nil {
		t.Fatalf("CountRegex error: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected 3 digit groups, got %d", count)
	}

	// Count words
	count, err = cursor.CountRegex(`\b\w+\b`, false)
	if err != nil {
		t.Fatalf("CountRegex error: %v", err)
	}
	// Words: the, quick, 123, brown, 456, fox, 789 = 7
	if count != 7 {
		t.Errorf("Expected 7 words, got %d", count)
	}
}

func TestFindNextRegex(t *testing.T) {
	g, cursor := newTestGarland(t, "num:1 num:2 num:3")
	defer g.Close()

	cursor.SeekByte(0)
	match, err := cursor.FindNextRegex(`num:\d`, RegexOptions{})
	if err != nil {
		t.Fatalf("FindNextRegex error: %v", err)
	}
	if match == nil {
		t.Fatal("Expected match")
	}
	// FindNext starts at cursor+1, so from 0+1=1, first match is at 6 (num:2)
	if match.ByteStart != 6 {
		t.Errorf("Expected match at 6, got %d", match.ByteStart)
	}
	if cursor.BytePos() != 6 {
		t.Errorf("Expected cursor at 6, got %d", cursor.BytePos())
	}
}

// ====================
// Regex Replace Tests
// ====================

func TestReplaceRegex(t *testing.T) {
	g, cursor := newTestGarland(t, "hello123world")
	defer g.Close()

	replaced, _, err := cursor.ReplaceRegex(`\d+`, "XXX", RegexOptions{})
	if err != nil {
		t.Fatalf("ReplaceRegex error: %v", err)
	}
	if !replaced {
		t.Error("Expected replacement")
	}

	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	expected := "helloXXXworld"
	if string(data) != expected {
		t.Errorf("Expected %q, got %q", expected, string(data))
	}
}

func TestReplaceRegexAll(t *testing.T) {
	g, cursor := newTestGarland(t, "a1 b2 c3")
	defer g.Close()

	count, _, err := cursor.ReplaceRegexAll(`\d`, "X", RegexOptions{})
	if err != nil {
		t.Fatalf("ReplaceRegexAll error: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected 3 replacements, got %d", count)
	}

	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	expected := "aX bX cX"
	if string(data) != expected {
		t.Errorf("Expected %q, got %q", expected, string(data))
	}
}

func TestReplaceRegexWithCaptureGroups(t *testing.T) {
	g, cursor := newTestGarland(t, "John Smith, Jane Doe")
	defer g.Close()

	// Swap first and last names
	count, _, err := cursor.ReplaceRegexAll(`(\w+) (\w+)`, "$2, $1", RegexOptions{})
	if err != nil {
		t.Fatalf("ReplaceRegexAll error: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 replacements, got %d", count)
	}

	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	expected := "Smith, John, Doe, Jane"
	if string(data) != expected {
		t.Errorf("Expected %q, got %q", expected, string(data))
	}
}

func TestReplaceRegexCount(t *testing.T) {
	g, cursor := newTestGarland(t, "x1 x2 x3 x4 x5")
	defer g.Close()

	count, _, err := cursor.ReplaceRegexCount(`x\d`, "Y", 3, RegexOptions{})
	if err != nil {
		t.Fatalf("ReplaceRegexCount error: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected 3 replacements, got %d", count)
	}

	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	expected := "Y Y Y x4 x5"
	if string(data) != expected {
		t.Errorf("Expected %q, got %q", expected, string(data))
	}
}

func TestReplaceRegexCaseInsensitive(t *testing.T) {
	g, cursor := newTestGarland(t, "Hello HELLO hello")
	defer g.Close()

	count, _, err := cursor.ReplaceRegexAll(`hello`, "hi", RegexOptions{CaseInsensitive: true})
	if err != nil {
		t.Fatalf("ReplaceRegexAll error: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected 3 replacements, got %d", count)
	}

	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	expected := "hi hi hi"
	if string(data) != expected {
		t.Errorf("Expected %q, got %q", expected, string(data))
	}
}

// ====================
// Edge Cases
// ====================

func TestFindEmptyNeedle(t *testing.T) {
	g, cursor := newTestGarland(t, "hello world")
	defer g.Close()

	result, err := cursor.FindString("", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("FindString error: %v", err)
	}
	if result != nil {
		t.Error("Expected nil for empty needle")
	}
}

func TestFindEmptyPattern(t *testing.T) {
	g, cursor := newTestGarland(t, "hello world")
	defer g.Close()

	result, err := cursor.FindRegex("", RegexOptions{})
	if err != nil {
		t.Fatalf("FindRegex error: %v", err)
	}
	if result != nil {
		t.Error("Expected nil for empty pattern")
	}
}

func TestFindInEmptyDocument(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}
	g, err := lib.Open(FileOptions{DataBytes: []byte{}})
	if err != nil {
		t.Fatalf("Failed to create empty garland: %v", err)
	}
	defer g.Close()
	cursor := g.NewCursor()

	result, err := cursor.FindString("hello", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("FindString error: %v", err)
	}
	if result != nil {
		t.Error("Expected nil for empty document")
	}
}

func TestReplaceEmptyNeedle(t *testing.T) {
	g, cursor := newTestGarland(t, "hello world")
	defer g.Close()

	replaced, _, err := cursor.ReplaceString("", "x", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("ReplaceString error: %v", err)
	}
	if replaced {
		t.Error("Expected no replacement for empty needle")
	}
}

func TestInvalidRegexPattern(t *testing.T) {
	g, cursor := newTestGarland(t, "hello world")
	defer g.Close()

	_, err := cursor.FindRegex(`[invalid`, RegexOptions{})
	if err == nil {
		t.Error("Expected error for invalid regex")
	}
}

func TestUnicodeSearch(t *testing.T) {
	g, cursor := newTestGarland(t, "héllo wörld 日本語")
	defer g.Close()

	// Search for unicode text
	result, err := cursor.FindString("wörld", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("FindString error: %v", err)
	}
	if result == nil {
		t.Fatal("Expected match")
	}
	if result.Match != "wörld" {
		t.Errorf("Expected 'wörld', got %q", result.Match)
	}

	// Search for Japanese
	result, err = cursor.FindString("日本語", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("FindString error: %v", err)
	}
	if result == nil {
		t.Fatal("Expected match")
	}
	if result.Match != "日本語" {
		t.Errorf("Expected '日本語', got %q", result.Match)
	}
}

func TestWholeWordUnicode(t *testing.T) {
	g, cursor := newTestGarland(t, "über überall über")
	defer g.Close()

	matches, err := cursor.FindStringAll("über", SearchOptions{CaseSensitive: true, WholeWord: true})
	if err != nil {
		t.Fatalf("FindStringAll error: %v", err)
	}
	// Should find standalone "über" at positions 0 and 14 (roughly), not "überall"
	if len(matches) != 2 {
		t.Errorf("Expected 2 whole-word matches, got %d", len(matches))
		for i, m := range matches {
			t.Logf("  Match %d: %d-%d %q", i, m.ByteStart, m.ByteEnd, m.Match)
		}
	}
}

func TestSearchAfterModification(t *testing.T) {
	g, cursor := newTestGarland(t, "hello world")
	defer g.Close()

	// Replace and then search
	cursor.ReplaceString("world", "universe", SearchOptions{CaseSensitive: true})

	cursor.SeekByte(0)
	result, err := cursor.FindString("universe", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("FindString error: %v", err)
	}
	if result == nil {
		t.Error("Expected to find 'universe' after replacement")
	}

	// Old text should not be found
	cursor.SeekByte(0)
	result, err = cursor.FindString("world", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("FindString error: %v", err)
	}
	if result != nil {
		t.Error("Should not find 'world' after replacement")
	}
}

func TestReplaceGrowingShrinking(t *testing.T) {
	// Test replacement that grows text
	g1, cursor1 := newTestGarland(t, "a a a")
	defer g1.Close()

	count, _, err := cursor1.ReplaceStringAll("a", "longer", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("ReplaceStringAll error: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected 3 replacements, got %d", count)
	}

	cursor1.SeekByte(0)
	data, _ := cursor1.ReadBytes(g1.ByteCount().Value)
	if string(data) != "longer longer longer" {
		t.Errorf("Unexpected content: %q", string(data))
	}

	// Test replacement that shrinks text
	g2, cursor2 := newTestGarland(t, "longer longer longer")
	defer g2.Close()

	count, _, err = cursor2.ReplaceStringAll("longer", "x", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("ReplaceStringAll error: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected 3 replacements, got %d", count)
	}

	cursor2.SeekByte(0)
	data, _ = cursor2.ReadBytes(g2.ByteCount().Value)
	if string(data) != "x x x" {
		t.Errorf("Unexpected content: %q", string(data))
	}
}

func TestRegexSpecialChars(t *testing.T) {
	g, cursor := newTestGarland(t, "hello.world hello*world hello+world")
	defer g.Close()

	// Search with escaped special chars
	result, err := cursor.FindRegex(`hello\.world`, RegexOptions{})
	if err != nil {
		t.Fatalf("FindRegex error: %v", err)
	}
	if result == nil {
		t.Fatal("Expected match")
	}
	if result.Match != "hello.world" {
		t.Errorf("Expected 'hello.world', got %q", result.Match)
	}
}

func TestSearchTransaction(t *testing.T) {
	g, cursor := newTestGarland(t, "old old old")
	defer g.Close()

	// ReplaceAll should be atomic
	count, result, err := cursor.ReplaceStringAll("old", "new", SearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("ReplaceStringAll error: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected 3 replacements, got %d", count)
	}

	// Should create single revision for all replacements
	if result.Revision == 0 {
		t.Error("Expected revision > 0")
	}

	// Undo should revert all changes at once
	g.UndoSeek(0)
	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	if string(data) != "old old old" {
		t.Errorf("Undo failed, got: %q", string(data))
	}
}
