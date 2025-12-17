package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/phroun/garland"
)

// REPL holds the state of the interactive session
type REPL struct {
	lib           *garland.Library
	garland       *garland.Garland
	cursors       map[string]*garland.Cursor // named cursors
	currentCursor string                     // name of current cursor
	reader        *bufio.Reader
}

// cursor returns the currently selected cursor
func (r *REPL) cursor() *garland.Cursor {
	if r.cursors == nil {
		return nil
	}
	return r.cursors[r.currentCursor]
}

func main() {
	fmt.Println("Garland REPL - Interactive Text Editor Demo")
	fmt.Println("Type 'help' for available commands, 'quit' to exit")
	fmt.Println()

	repl := &REPL{
		reader: bufio.NewReader(os.Stdin),
	}

	// Initialize library
	lib, err := garland.Init(garland.LibraryOptions{})
	if err != nil {
		fmt.Printf("Error initializing library: %v\n", err)
		os.Exit(1)
	}
	repl.lib = lib

	// Main loop
	for {
		fmt.Print("garland> ")
		input, err := repl.reader.ReadString('\n')
		if err != nil {
			fmt.Println("\nGoodbye!")
			break
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		if !repl.handleCommand(input) {
			break
		}
	}

	// Cleanup
	if repl.garland != nil {
		repl.garland.Close()
	}
}

func (r *REPL) handleCommand(input string) bool {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return true
	}

	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "help":
		r.printHelp()

	case "quit", "exit":
		fmt.Println("Goodbye!")
		return false

	case "new":
		r.cmdNew(args)

	case "open":
		r.cmdOpen(args)

	case "close":
		r.cmdClose()

	case "status":
		r.cmdStatus()

	case "cursor":
		r.cmdCursor(args)

	case "seek":
		r.cmdSeek(args)

	case "relseek":
		r.cmdRelSeek(args)

	case "read":
		r.cmdRead(args)

	case "readline":
		r.cmdReadLine()

	case "insert":
		r.cmdInsert(args, false)

	case "insert-":
		r.cmdInsert(args, true)

	case "overwrite":
		r.cmdOverwrite(args)

	case "truncate":
		r.cmdTruncate()

	case "delete":
		r.cmdDelete(args)

	case "backdelete":
		r.cmdBackDelete(args)

	case "dump":
		r.cmdDump()

	case "tree":
		r.cmdTree()

	case "tx", "transaction":
		r.cmdTransaction(args)

	case "undoseek":
		r.cmdUndoSeek(args)

	case "revisions":
		r.cmdRevisions()

	case "forks":
		r.cmdForks()

	case "forkswitch":
		r.cmdForkSwitch(args)

	case "version":
		r.cmdVersion()

	case "decorate":
		r.cmdDecorate(args)

	case "undecorate":
		r.cmdUndecorate(args)

	case "decorations":
		r.cmdDecorations(args)

	case "decoration":
		r.cmdGetDecoration(args)

	default:
		fmt.Printf("Unknown command: %s. Type 'help' for available commands.\n", cmd)
	}

	return true
}

func (r *REPL) printHelp() {
	help := `
Available Commands:
-------------------

FILE OPERATIONS:
  new <text>              Create a new garland with the given text content
  open <filepath>         Open a file (not yet implemented)
  close                   Close the current garland
  status                  Show current garland status

CURSOR OPERATIONS:
  cursor                  Show current cursor position
  cursor <name>           Switch to (or create) a named cursor
  cursor list             List all cursors and their positions
  seek byte <pos>         Move cursor to byte position
  seek rune <pos>         Move cursor to rune position
  seek line <line> <rune> Move cursor to line:rune position
  relseek bytes <delta>   Move cursor relative (+ forward, - backward)
  relseek runes <delta>   Move cursor relative by runes

READ OPERATIONS:
  read bytes <length>     Read bytes from cursor position (advances cursor)
  read string <length>    Read runes from cursor position (advances cursor)
  readline                Read the entire line at cursor position

EDIT OPERATIONS:
  insert <text>           Insert text at cursor position (advances cursor)
  insert- <text>          Insert text BEFORE existing content at position
  overwrite <len> <text>  Replace <len> bytes at cursor with <text>
  truncate                Delete from cursor to end of file
  delete bytes <length>   Delete bytes forward from cursor position
  delete runes <length>   Delete runes forward from cursor position
  backdelete bytes <len>  Delete bytes backward (like backspace)
  backdelete runes <len>  Delete runes backward (like backspace)

INSPECTION:
  dump                    Dump all content
  tree                    Show tree structure

VERSION CONTROL:
  tx start <name>         Start a transaction with optional name
  tx commit               Commit the current transaction
  tx rollback             Rollback the current transaction
  undoseek <revision>     Seek to a specific revision in current fork
  revisions               List revisions in current fork
  forks                   List all forks
  forkswitch <fork>       Switch to a different fork
  version                 Show current fork and revision

NOTE: Forks are created automatically when you edit from a non-HEAD revision.
      Use 'forkswitch' to navigate between existing forks.

DECORATIONS:
  decorate <key>          Add decoration at cursor position
  decorate k=byte <pos>   Add decoration at byte position
  decorate k=rune <pos>   Add decoration at rune position
  decorate k=line <l>:<r> Add decoration at line:rune position
  decorate k=nil          Remove decoration (same as undecorate)
  decorate a=byte 5, b=line 1:0   Multiple decorations at once
  undecorate <key>        Remove a decoration
  decorations             List all decorations in the file
  decorations <line>      List decorations on a specific line
  decoration <key>        Get the position of a specific decoration

OTHER:
  help                    Show this help message
  quit, exit              Exit the REPL
`
	fmt.Println(help)
}

func (r *REPL) cmdNew(args []string) {
	if r.garland != nil {
		r.garland.Close()
	}

	content := strings.Join(args, " ")
	g, err := r.lib.Open(garland.FileOptions{DataString: content})
	if err != nil {
		// Handle empty string case
		if content == "" {
			g, err = r.lib.Open(garland.FileOptions{DataBytes: []byte{}})
			if err != nil {
				fmt.Printf("Error creating garland: %v\n", err)
				return
			}
		} else {
			fmt.Printf("Error creating garland: %v\n", err)
			return
		}
	}

	r.garland = g
	r.cursors = make(map[string]*garland.Cursor)
	r.cursors["default"] = g.NewCursor()
	r.currentCursor = "default"
	fmt.Printf("Created new garland with %d bytes\n", g.ByteCount().Value)
}

func (r *REPL) cmdOpen(args []string) {
	fmt.Println("File opening not yet implemented")
}

func (r *REPL) cmdClose() {
	if r.garland == nil {
		fmt.Println("No garland is open")
		return
	}

	r.garland.Close()
	r.garland = nil
	r.cursors = nil
	r.currentCursor = ""
	fmt.Println("Garland closed")
}

func (r *REPL) cmdStatus() {
	if r.garland == nil {
		fmt.Println("No garland is open. Use 'new <text>' to create one.")
		return
	}

	g := r.garland
	byteCount := g.ByteCount()
	runeCount := g.RuneCount()
	lineCount := g.LineCount()

	fmt.Println("Garland Status:")
	fmt.Printf("  Bytes: %d (complete: %v)\n", byteCount.Value, byteCount.Complete)
	fmt.Printf("  Runes: %d (complete: %v)\n", runeCount.Value, runeCount.Complete)
	fmt.Printf("  Lines: %d (complete: %v)\n", lineCount.Value, lineCount.Complete)
	fmt.Printf("  Fork: %d, Revision: %d\n", g.CurrentFork(), g.CurrentRevision())
	fmt.Printf("  In Transaction: %v (depth: %d)\n", g.InTransaction(), g.TransactionDepth())

	if cursor := r.cursor(); cursor != nil {
		line, lineRune := cursor.LinePos()
		fmt.Printf("  Cursor '%s': byte=%d, rune=%d, line=%d:%d\n",
			r.currentCursor, cursor.BytePos(), cursor.RunePos(), line, lineRune)
		fmt.Printf("  Total cursors: %d\n", len(r.cursors))
	}
}

func (r *REPL) cmdCursor(args []string) {
	if !r.ensureGarland() {
		return
	}

	// Handle subcommands: cursor, cursor <name>, cursor list
	if len(args) >= 1 {
		subcmd := strings.ToLower(args[0])

		if subcmd == "list" {
			fmt.Println("Cursors:")
			for name, c := range r.cursors {
				marker := "  "
				if name == r.currentCursor {
					marker = "> "
				}
				line, lineRune := c.LinePos()
				fmt.Printf("%s%s: byte=%d, rune=%d, line=%d:%d\n",
					marker, name, c.BytePos(), c.RunePos(), line, lineRune)
			}
			return
		}

		// Switch to or create a cursor by name
		name := args[0]
		if _, exists := r.cursors[name]; !exists {
			// Create new cursor
			r.cursors[name] = r.garland.NewCursor()
			fmt.Printf("Created new cursor '%s'\n", name)
		}
		r.currentCursor = name
		fmt.Printf("Switched to cursor '%s'\n", name)
	}

	// Show current cursor info
	cursor := r.cursor()
	line, lineRune := cursor.LinePos()
	fmt.Printf("Cursor '%s' Position:\n", r.currentCursor)
	fmt.Printf("  Byte:     %d\n", cursor.BytePos())
	fmt.Printf("  Rune:     %d\n", cursor.RunePos())
	fmt.Printf("  Line:     %d\n", line)
	fmt.Printf("  LineRune: %d\n", lineRune)
	fmt.Printf("  Ready:    %v\n", cursor.IsReady())
}

func (r *REPL) cmdSeek(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: seek byte|rune|line <pos> [<rune>]")
		return
	}

	cursor := r.cursor()
	mode := strings.ToLower(args[0])
	pos, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		fmt.Printf("Invalid position: %v\n", err)
		return
	}

	switch mode {
	case "byte":
		err = cursor.SeekByte(pos)
	case "rune":
		err = cursor.SeekRune(pos)
	case "line":
		runeInLine := int64(0)
		if len(args) >= 3 {
			runeInLine, err = strconv.ParseInt(args[2], 10, 64)
			if err != nil {
				fmt.Printf("Invalid rune position: %v\n", err)
				return
			}
		}
		err = cursor.SeekLine(pos, runeInLine)
	default:
		fmt.Println("Unknown seek mode. Use: byte, rune, or line")
		return
	}

	if err != nil {
		fmt.Printf("Seek error: %v\n", err)
		return
	}

	line, lineRune := cursor.LinePos()
	fmt.Printf("Cursor moved to byte=%d, rune=%d, line=%d:%d\n",
		cursor.BytePos(), cursor.RunePos(), line, lineRune)
}

func (r *REPL) cmdRelSeek(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: relseek bytes|runes <delta>")
		fmt.Println("  delta can be positive (forward) or negative (backward)")
		return
	}

	cursor := r.cursor()
	mode := strings.ToLower(args[0])
	delta, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		fmt.Printf("Invalid delta: %v\n", err)
		return
	}

	switch mode {
	case "bytes":
		err = cursor.SeekRelativeBytes(delta)
	case "runes":
		err = cursor.SeekRelativeRunes(delta)
	default:
		fmt.Println("Unknown relseek mode. Use: bytes or runes")
		return
	}

	if err != nil {
		fmt.Printf("RelSeek error: %v\n", err)
		return
	}

	line, lineRune := cursor.LinePos()
	fmt.Printf("Cursor moved to byte=%d, rune=%d, line=%d:%d\n",
		cursor.BytePos(), cursor.RunePos(), line, lineRune)
}

func (r *REPL) cmdRead(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: read bytes|string <length>")
		return
	}

	cursor := r.cursor()
	mode := strings.ToLower(args[0])
	length, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		fmt.Printf("Invalid length: %v\n", err)
		return
	}

	switch mode {
	case "bytes":
		data, err := cursor.ReadBytes(length)
		if err != nil {
			fmt.Printf("Read error: %v\n", err)
			return
		}
		fmt.Printf("Read %d bytes: %q\n", len(data), string(data))
		fmt.Printf("Hex: %x\n", data)

	case "string":
		data, err := cursor.ReadString(length)
		if err != nil {
			fmt.Printf("Read error: %v\n", err)
			return
		}
		fmt.Printf("Read %d runes: %q\n", len([]rune(data)), data)

	default:
		fmt.Println("Unknown read mode. Use: bytes or string")
	}
}

func (r *REPL) cmdReadLine() {
	if !r.ensureGarland() {
		return
	}

	cursor := r.cursor()
	data, err := cursor.ReadLine()
	if err != nil {
		fmt.Printf("Read error: %v\n", err)
		return
	}
	fmt.Printf("Line content: %q\n", data)
}

func (r *REPL) cmdInsert(args []string, insertBefore bool) {
	if !r.ensureGarland() {
		return
	}

	text := strings.Join(args, " ")
	if text == "" {
		fmt.Println("Usage: insert <text>")
		fmt.Println("       insert- <text>  (insert before existing content at position)")
		return
	}

	// Handle escape sequences
	text = strings.ReplaceAll(text, "\\n", "\n")
	text = strings.ReplaceAll(text, "\\t", "\t")

	cursor := r.cursor()
	result, err := cursor.InsertString(text, nil, insertBefore)
	if err != nil {
		fmt.Printf("Insert error: %v\n", err)
		return
	}
	beforeStr := ""
	if insertBefore {
		beforeStr = " (before)"
	}
	fmt.Printf("Inserted %d bytes%s. Now at fork=%d, revision=%d\n",
		len(text), beforeStr, result.Fork, result.Revision)
}

func (r *REPL) cmdOverwrite(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: overwrite <length> <text>")
		fmt.Println("  Replaces <length> bytes at cursor with <text>")
		return
	}

	length, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		fmt.Printf("Invalid length: %v\n", err)
		return
	}

	text := strings.Join(args[1:], " ")
	// Handle escape sequences
	text = strings.ReplaceAll(text, "\\n", "\n")
	text = strings.ReplaceAll(text, "\\t", "\t")

	cursor := r.cursor()
	_, result, err := cursor.OverwriteBytes(length, []byte(text))
	if err != nil {
		fmt.Printf("Overwrite error: %v\n", err)
		return
	}
	fmt.Printf("Overwrote %d bytes with %d bytes. Now at fork=%d, revision=%d\n",
		length, len(text), result.Fork, result.Revision)
}

func (r *REPL) cmdTruncate() {
	if !r.ensureGarland() {
		return
	}

	cursor := r.cursor()
	result, err := cursor.TruncateToEOF()
	if err != nil {
		fmt.Printf("Truncate error: %v\n", err)
		return
	}
	fmt.Printf("Truncated from cursor to EOF. Now at fork=%d, revision=%d\n",
		result.Fork, result.Revision)
	fmt.Printf("File is now %d bytes\n", r.garland.ByteCount().Value)
}

func (r *REPL) cmdDelete(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: delete bytes|runes <length>")
		return
	}

	cursor := r.cursor()
	mode := strings.ToLower(args[0])
	length, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		fmt.Printf("Invalid length: %v\n", err)
		return
	}

	switch mode {
	case "bytes":
		_, result, err := cursor.DeleteBytes(length, false)
		if err != nil {
			fmt.Printf("Delete error: %v\n", err)
			return
		}
		fmt.Printf("Deleted %d bytes. Now at fork=%d, revision=%d\n",
			length, result.Fork, result.Revision)

	case "runes":
		_, result, err := cursor.DeleteRunes(length, false)
		if err != nil {
			fmt.Printf("Delete error: %v\n", err)
			return
		}
		fmt.Printf("Deleted %d runes. Now at fork=%d, revision=%d\n",
			length, result.Fork, result.Revision)

	default:
		fmt.Println("Unknown delete mode. Use: bytes or runes")
	}
}

func (r *REPL) cmdBackDelete(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: backdelete bytes|runes <length>")
		return
	}

	cursor := r.cursor()
	mode := strings.ToLower(args[0])
	length, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		fmt.Printf("Invalid length: %v\n", err)
		return
	}

	switch mode {
	case "bytes":
		_, result, err := cursor.BackDeleteBytes(length, false)
		if err != nil {
			fmt.Printf("BackDelete error: %v\n", err)
			return
		}
		line, lineRune := cursor.LinePos()
		fmt.Printf("Back-deleted %d bytes. Cursor now at byte=%d, line=%d:%d. Fork=%d, revision=%d\n",
			length, cursor.BytePos(), line, lineRune, result.Fork, result.Revision)

	case "runes":
		_, result, err := cursor.BackDeleteRunes(length, false)
		if err != nil {
			fmt.Printf("BackDelete error: %v\n", err)
			return
		}
		line, lineRune := cursor.LinePos()
		fmt.Printf("Back-deleted %d runes. Cursor now at byte=%d, line=%d:%d. Fork=%d, revision=%d\n",
			length, cursor.BytePos(), line, lineRune, result.Fork, result.Revision)

	default:
		fmt.Println("Unknown backdelete mode. Use: bytes or runes")
	}
}

func (r *REPL) cmdDump() {
	if !r.ensureGarland() {
		return
	}

	cursor := r.cursor()

	// Save cursor position
	savedPos := cursor.BytePos()

	// Read all content
	cursor.SeekByte(0)
	byteCount := r.garland.ByteCount().Value
	data, err := cursor.ReadBytes(byteCount)
	if err != nil {
		fmt.Printf("Read error: %v\n", err)
		return
	}

	fmt.Println("Content:")
	fmt.Println("--------")
	fmt.Printf("%s\n", string(data))
	fmt.Println("--------")
	fmt.Printf("Total: %d bytes, %d runes, %d lines\n",
		r.garland.ByteCount().Value,
		r.garland.RuneCount().Value,
		r.garland.LineCount().Value)

	// Restore cursor position
	cursor.SeekByte(savedPos)
}

func (r *REPL) cmdTree() {
	if !r.ensureGarland() {
		return
	}

	fmt.Println("Tree structure inspection not yet implemented")
	fmt.Printf("Current state: fork=%d, revision=%d\n",
		r.garland.CurrentFork(), r.garland.CurrentRevision())
}

func (r *REPL) cmdTransaction(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: tx start [name] | tx commit | tx rollback")
		return
	}

	subcmd := strings.ToLower(args[0])
	switch subcmd {
	case "start":
		name := ""
		if len(args) > 1 {
			name = strings.Join(args[1:], " ")
		}
		err := r.garland.TransactionStart(name)
		if err != nil {
			fmt.Printf("Transaction start error: %v\n", err)
			return
		}
		fmt.Printf("Transaction started (depth=%d, name=%q)\n",
			r.garland.TransactionDepth(), name)

	case "commit":
		result, err := r.garland.TransactionCommit()
		if err != nil {
			fmt.Printf("Transaction commit error: %v\n", err)
			return
		}
		fmt.Printf("Transaction committed. Now at fork=%d, revision=%d\n",
			result.Fork, result.Revision)

	case "rollback":
		err := r.garland.TransactionRollback()
		if err != nil {
			fmt.Printf("Transaction rollback error: %v\n", err)
			return
		}
		fmt.Printf("Transaction rolled back. Now at fork=%d, revision=%d\n",
			r.garland.CurrentFork(), r.garland.CurrentRevision())

	default:
		fmt.Println("Unknown transaction command. Use: start, commit, or rollback")
	}
}

func (r *REPL) cmdUndoSeek(args []string) {
	if !r.ensureGarland() {
		return
	}

	g := r.garland

	if len(args) < 1 {
		fmt.Println("Usage: undoseek <revision>")
		fmt.Printf("Current revision: %d\n", g.CurrentRevision())
		// Show revision range
		forkInfo, err := g.GetForkInfo(g.CurrentFork())
		if err == nil {
			fmt.Printf("Valid range: 0 to %d (highest in this fork)\n", forkInfo.HighestRevision)
		}
		return
	}

	rev, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		fmt.Printf("Invalid revision number: %v\n", err)
		return
	}

	prevFork := g.CurrentFork()
	prevRev := g.CurrentRevision()

	err = g.UndoSeek(garland.RevisionID(rev))
	if err != nil {
		fmt.Printf("UndoSeek error: %v\n", err)
		return
	}

	fmt.Printf("Moved from fork=%d/rev=%d to fork=%d/rev=%d\n",
		prevFork, prevRev, g.CurrentFork(), g.CurrentRevision())
	fmt.Printf("Content is now %d bytes\n", g.ByteCount().Value)

	// Show cursor position update
	if cursor := r.cursor(); cursor != nil {
		line, lineRune := cursor.LinePos()
		fmt.Printf("Cursor '%s' now at: byte=%d, line=%d:%d\n",
			r.currentCursor, cursor.BytePos(), line, lineRune)
	}

	// Warn about fork creation on edit
	forkInfo, err := g.GetForkInfo(g.CurrentFork())
	if err == nil && g.CurrentRevision() < forkInfo.HighestRevision {
		fmt.Println("Note: Editing from here will create a new fork!")
	}
}

func (r *REPL) cmdRevisions() {
	if !r.ensureGarland() {
		return
	}

	g := r.garland
	currentRev := g.CurrentRevision()
	currentFork := g.CurrentFork()

	// Get fork info to know the highest revision
	forkInfo, err := g.GetForkInfo(currentFork)
	if err != nil {
		fmt.Printf("Error getting fork info: %v\n", err)
		return
	}

	highestRev := forkInfo.HighestRevision

	fmt.Printf("Fork %d - Revisions (0 to %d):\n", currentFork, highestRev)

	// Get revision range (0 to highest)
	revisions, err := g.GetRevisionRange(0, highestRev)
	if err != nil {
		fmt.Printf("Error getting revisions: %v\n", err)
		return
	}

	if len(revisions) == 0 {
		fmt.Println("  (no recorded revisions yet)")
		fmt.Printf("  Current position: revision %d\n", currentRev)
		return
	}

	for _, info := range revisions {
		marker := "  "
		if info.Revision == currentRev {
			marker = "> "
		}
		changes := ""
		if info.HasChanges {
			changes = " [has changes]"
		}
		name := info.Name
		if name == "" {
			name = "(unnamed)"
		}
		fmt.Printf("%s%d: %s%s\n", marker, info.Revision, name, changes)
	}

	if currentRev < highestRev {
		fmt.Printf("\nNote: Not at HEAD (current=%d, HEAD=%d). Editing will create a new fork.\n",
			currentRev, highestRev)
	}
}

func (r *REPL) cmdForks() {
	if !r.ensureGarland() {
		return
	}

	g := r.garland
	forks := g.ListForks()
	currentFork := g.CurrentFork()

	fmt.Printf("Forks (%d total):\n", len(forks))
	for _, info := range forks {
		marker := "  "
		if info.ID == currentFork {
			marker = "> "
		}
		parentInfo := ""
		if info.ParentFork != info.ID {
			parentInfo = fmt.Sprintf(" (parent: fork=%d@rev=%d)", info.ParentFork, info.ParentRevision)
		}
		fmt.Printf("%s%d: highest revision %d%s\n", marker, info.ID, info.HighestRevision, parentInfo)
	}
}

func (r *REPL) cmdForkSwitch(args []string) {
	if !r.ensureGarland() {
		return
	}

	g := r.garland

	if len(args) < 1 {
		fmt.Println("Usage: forkswitch <fork_id>")
		fmt.Printf("Current fork: %d\n", g.CurrentFork())
		fmt.Println("Use 'forks' to see available forks.")
		return
	}

	forkID, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		fmt.Printf("Invalid fork ID: %v\n", err)
		return
	}

	prevFork := g.CurrentFork()
	prevRev := g.CurrentRevision()

	err = g.ForkSeek(garland.ForkID(forkID))
	if err != nil {
		fmt.Printf("ForkSwitch error: %v\n", err)
		return
	}

	fmt.Printf("Switched from fork=%d/rev=%d to fork=%d/rev=%d\n",
		prevFork, prevRev, g.CurrentFork(), g.CurrentRevision())
	fmt.Printf("Content is now %d bytes\n", g.ByteCount().Value)

	// Show cursor position update
	if cursor := r.cursor(); cursor != nil {
		line, lineRune := cursor.LinePos()
		fmt.Printf("Cursor '%s' now at: byte=%d, line=%d:%d\n",
			r.currentCursor, cursor.BytePos(), line, lineRune)
	}
}

func (r *REPL) cmdVersion() {
	if !r.ensureGarland() {
		return
	}

	g := r.garland
	fmt.Printf("Current Fork: %d\n", g.CurrentFork())
	fmt.Printf("Current Revision: %d\n", g.CurrentRevision())

	// Show revision info if available
	info, err := g.GetRevisionInfo(g.CurrentRevision())
	if err == nil && info != nil {
		fmt.Printf("Revision Name: %q\n", info.Name)
		fmt.Printf("Has Changes: %v\n", info.HasChanges)
	}
}

func (r *REPL) ensureGarland() bool {
	if r.garland == nil {
		fmt.Println("No garland is open. Use 'new <text>' to create one.")
		return false
	}
	return true
}

func (r *REPL) cmdDecorate(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: decorate <key>                    - at cursor position")
		fmt.Println("       decorate key=byte <pos>           - at byte position")
		fmt.Println("       decorate key=rune <pos>           - at rune position")
		fmt.Println("       decorate key=line <line>:<rune>   - at line:rune position")
		fmt.Println("       decorate key=nil                  - remove decoration")
		fmt.Println("       decorate k1=byte 5, k2=line 1:0   - multiple decorations")
		return
	}

	// Join args and split by comma to handle multiple decorations
	fullInput := strings.Join(args, " ")
	parts := strings.Split(fullInput, ",")

	var entries []garland.DecorationEntry

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		entry, desc, err := r.parseDecorationSpec(part)
		if err != nil {
			fmt.Printf("Error parsing '%s': %v\n", part, err)
			return
		}
		entries = append(entries, entry)
		fmt.Printf("  %s: %s\n", entry.Key, desc)
	}

	if len(entries) == 0 {
		fmt.Println("No decorations specified")
		return
	}

	result, err := r.garland.Decorate(entries)
	if err != nil {
		fmt.Printf("Decorate error: %v\n", err)
		return
	}

	fmt.Printf("Applied %d decoration(s). Fork=%d, revision=%d\n",
		len(entries), result.Fork, result.Revision)
}

// parseDecorationSpec parses a decoration specification like "key=byte 120" or "key" (cursor pos)
func (r *REPL) parseDecorationSpec(spec string) (garland.DecorationEntry, string, error) {
	// Check for key=value format
	if idx := strings.Index(spec, "="); idx > 0 {
		key := strings.TrimSpace(spec[:idx])
		value := strings.TrimSpace(spec[idx+1:])

		// Handle nil (deletion)
		if value == "nil" {
			return garland.DecorationEntry{Key: key, Address: nil}, "removed", nil
		}

		// Parse address type and value
		valueParts := strings.Fields(value)
		if len(valueParts) < 2 {
			return garland.DecorationEntry{}, "", fmt.Errorf("expected 'type position', got %q", value)
		}

		addrType := strings.ToLower(valueParts[0])
		posStr := valueParts[1]

		switch addrType {
		case "byte":
			pos, err := strconv.ParseInt(posStr, 10, 64)
			if err != nil {
				return garland.DecorationEntry{}, "", fmt.Errorf("invalid byte position: %v", err)
			}
			return garland.DecorationEntry{
				Key:     key,
				Address: &garland.AbsoluteAddress{Mode: garland.ByteMode, Byte: pos},
			}, fmt.Sprintf("byte %d", pos), nil

		case "rune":
			pos, err := strconv.ParseInt(posStr, 10, 64)
			if err != nil {
				return garland.DecorationEntry{}, "", fmt.Errorf("invalid rune position: %v", err)
			}
			return garland.DecorationEntry{
				Key:     key,
				Address: &garland.AbsoluteAddress{Mode: garland.RuneMode, Rune: pos},
			}, fmt.Sprintf("rune %d", pos), nil

		case "line":
			// Parse line:rune format
			lineParts := strings.Split(posStr, ":")
			if len(lineParts) != 2 {
				return garland.DecorationEntry{}, "", fmt.Errorf("line position must be 'line:rune', got %q", posStr)
			}
			line, err := strconv.ParseInt(lineParts[0], 10, 64)
			if err != nil {
				return garland.DecorationEntry{}, "", fmt.Errorf("invalid line number: %v", err)
			}
			runeInLine, err := strconv.ParseInt(lineParts[1], 10, 64)
			if err != nil {
				return garland.DecorationEntry{}, "", fmt.Errorf("invalid rune in line: %v", err)
			}
			return garland.DecorationEntry{
				Key:     key,
				Address: &garland.AbsoluteAddress{Mode: garland.LineRuneMode, Line: line, LineRune: runeInLine},
			}, fmt.Sprintf("line %d:%d", line, runeInLine), nil

		default:
			return garland.DecorationEntry{}, "", fmt.Errorf("unknown address type %q (use byte, rune, or line)", addrType)
		}
	}

	// Simple form: just key, use cursor position
	key := spec
	cursor := r.cursor()
	bytePos := cursor.BytePos()

	return garland.DecorationEntry{
		Key:     key,
		Address: &garland.AbsoluteAddress{Mode: garland.ByteMode, Byte: bytePos},
	}, fmt.Sprintf("byte %d (cursor)", bytePos), nil
}

func (r *REPL) cmdUndecorate(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: undecorate <key>")
		fmt.Println("  Removes the decoration with the given key")
		return
	}

	key := args[0]

	entry := garland.DecorationEntry{
		Key:     key,
		Address: nil, // nil address means delete
	}

	result, err := r.garland.Decorate([]garland.DecorationEntry{entry})
	if err != nil {
		fmt.Printf("Undecorate error: %v\n", err)
		return
	}

	fmt.Printf("Removed decoration '%s'. Fork=%d, revision=%d\n",
		key, result.Fork, result.Revision)
}

func (r *REPL) cmdDecorations(args []string) {
	if !r.ensureGarland() {
		return
	}

	// If a line number is provided, show decorations on that line
	if len(args) >= 1 {
		line, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			fmt.Printf("Invalid line number: %v\n", err)
			return
		}

		decs, err := r.garland.GetDecorationsOnLine(line)
		if err != nil {
			fmt.Printf("Error getting decorations: %v\n", err)
			return
		}

		if len(decs) == 0 {
			fmt.Printf("No decorations on line %d\n", line)
			return
		}

		fmt.Printf("Decorations on line %d:\n", line)
		for _, dec := range decs {
			fmt.Printf("  '%s' at byte %d\n", dec.Key, dec.Address.Byte)
		}
		return
	}

	// Otherwise show all decorations in the file
	byteCount := r.garland.ByteCount().Value
	decs, err := r.garland.GetDecorationsInByteRange(0, byteCount)
	if err != nil {
		fmt.Printf("Error getting decorations: %v\n", err)
		return
	}

	if len(decs) == 0 {
		fmt.Println("No decorations in file")
		return
	}

	fmt.Printf("Decorations (%d total):\n", len(decs))
	for _, dec := range decs {
		fmt.Printf("  '%s' at byte %d\n", dec.Key, dec.Address.Byte)
	}
}

func (r *REPL) cmdGetDecoration(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: decoration <key>")
		fmt.Println("  Gets the position of a specific decoration")
		return
	}

	key := args[0]

	addr, err := r.garland.GetDecorationPosition(key)
	if err != nil {
		if err == garland.ErrDecorationNotFound {
			fmt.Printf("Decoration '%s' not found\n", key)
		} else {
			fmt.Printf("Error getting decoration: %v\n", err)
		}
		return
	}

	fmt.Printf("Decoration '%s' is at:\n", key)
	switch addr.Mode {
	case garland.ByteMode:
		fmt.Printf("  Byte: %d\n", addr.Byte)
	case garland.RuneMode:
		fmt.Printf("  Rune: %d\n", addr.Rune)
	case garland.LineRuneMode:
		fmt.Printf("  Line: %d:%d\n", addr.Line, addr.LineRune)
	}
}
