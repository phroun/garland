package garland

import (
	"testing"
)

func TestLoadDecorationsFromStringBasic(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello World!"})
	defer g.Close()

	content := `[decorations]
mark1=5
mark2=0
mark3=11
`

	err := g.LoadDecorationsFromString(content)
	if err != nil {
		t.Fatalf("LoadDecorationsFromString failed: %v", err)
	}

	// Check decorations were loaded
	pos1, err := g.GetDecorationPosition("mark1")
	if err != nil {
		t.Fatalf("mark1 not found: %v", err)
	}
	if pos1.Byte != 5 {
		t.Errorf("mark1 position = %d, want 5", pos1.Byte)
	}

	pos2, err := g.GetDecorationPosition("mark2")
	if err != nil {
		t.Fatalf("mark2 not found: %v", err)
	}
	if pos2.Byte != 0 {
		t.Errorf("mark2 position = %d, want 0", pos2.Byte)
	}

	pos3, err := g.GetDecorationPosition("mark3")
	if err != nil {
		t.Fatalf("mark3 not found: %v", err)
	}
	if pos3.Byte != 11 {
		t.Errorf("mark3 position = %d, want 11", pos3.Byte)
	}
}

func TestLoadDecorationsFromStringComments(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello World!"})
	defer g.Close()

	content := `; This is a full-line semicolon comment
# This is a full-line hash comment (note the space after #)
[decorations]
; Another semicolon comment
# Another hash comment
mark1=5
mark2=3   ; end of line comment
mark3=7  # another end of line comment
#nocomment=1
`

	err := g.LoadDecorationsFromString(content)
	if err != nil {
		t.Fatalf("LoadDecorationsFromString failed: %v", err)
	}

	// mark1, mark2, mark3 should exist
	_, err = g.GetDecorationPosition("mark1")
	if err != nil {
		t.Errorf("mark1 should exist: %v", err)
	}
	_, err = g.GetDecorationPosition("mark2")
	if err != nil {
		t.Errorf("mark2 should exist: %v", err)
	}
	_, err = g.GetDecorationPosition("mark3")
	if err != nil {
		t.Errorf("mark3 should exist: %v", err)
	}

	// #nocomment should exist (# without space is not a comment)
	_, err = g.GetDecorationPosition("#nocomment")
	if err != nil {
		t.Errorf("#nocomment should exist (# without space is not a comment): %v", err)
	}
}

func TestLoadDecorationsFromStringIgnoresUnknownSections(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello World!"})
	defer g.Close()

	content := `[unknown_section]
foo=1
bar=2

[decorations]
mark1=5

[another_unknown]
baz=3
`

	err := g.LoadDecorationsFromString(content)
	if err != nil {
		t.Fatalf("LoadDecorationsFromString failed: %v", err)
	}

	// Only mark1 should exist (from [decorations] section)
	_, err = g.GetDecorationPosition("mark1")
	if err != nil {
		t.Errorf("mark1 should exist: %v", err)
	}

	// foo, bar, baz should NOT exist
	_, err = g.GetDecorationPosition("foo")
	if err == nil {
		t.Error("foo should not exist (from unknown section)")
	}
	_, err = g.GetDecorationPosition("bar")
	if err == nil {
		t.Error("bar should not exist (from unknown section)")
	}
	_, err = g.GetDecorationPosition("baz")
	if err == nil {
		t.Error("baz should not exist (from unknown section)")
	}
}

func TestLoadDecorationsFromStringEmptyContent(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello World!"})
	defer g.Close()

	err := g.LoadDecorationsFromString("")
	if err != nil {
		t.Errorf("LoadDecorationsFromString with empty content should not fail: %v", err)
	}
}

func TestLoadDecorationsFromStringHashFragment(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello World!"})
	defer g.Close()

	// Bookmarks in #fragment format should work
	content := `[decorations]
#chapter1=0
#section2=5
`

	err := g.LoadDecorationsFromString(content)
	if err != nil {
		t.Fatalf("LoadDecorationsFromString failed: %v", err)
	}

	_, err = g.GetDecorationPosition("#chapter1")
	if err != nil {
		t.Errorf("#chapter1 should exist: %v", err)
	}
	_, err = g.GetDecorationPosition("#section2")
	if err != nil {
		t.Errorf("#section2 should exist: %v", err)
	}
}
