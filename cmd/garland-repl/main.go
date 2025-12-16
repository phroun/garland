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
	lib     *garland.Library
	garland *garland.Garland
	cursor  *garland.Cursor
	reader  *bufio.Reader
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

	case "read":
		r.cmdRead(args)

	case "readline":
		r.cmdReadLine()

	case "insert":
		r.cmdInsert(args)

	case "delete":
		r.cmdDelete(args)

	case "dump":
		r.cmdDump()

	case "tree":
		r.cmdTree()

	case "tx", "transaction":
		r.cmdTransaction(args)

	case "undo":
		r.cmdUndo()

	case "fork":
		r.cmdFork()

	case "version":
		r.cmdVersion()

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
  seek byte <pos>         Move cursor to byte position
  seek rune <pos>         Move cursor to rune position
  seek line <line> <rune> Move cursor to line:rune position

READ OPERATIONS:
  read bytes <length>     Read bytes from cursor position
  read string <length>    Read runes from cursor position as string
  readline                Read the entire line at cursor position

EDIT OPERATIONS:
  insert <text>           Insert text at cursor position
  delete bytes <length>   Delete bytes from cursor position
  delete runes <length>   Delete runes from cursor position

INSPECTION:
  dump                    Dump all content
  tree                    Show tree structure

VERSION CONTROL:
  tx start <name>         Start a transaction with optional name
  tx commit               Commit the current transaction
  tx rollback             Rollback the current transaction
  undo                    Undo to previous revision (not yet implemented)
  fork                    Create a new fork
  version                 Show current fork and revision

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
	r.cursor = g.NewCursor()
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
	r.cursor = nil
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

	if r.cursor != nil {
		line, lineRune := r.cursor.LinePos()
		fmt.Printf("  Cursor: byte=%d, rune=%d, line=%d:%d\n",
			r.cursor.BytePos(), r.cursor.RunePos(), line, lineRune)
	}
}

func (r *REPL) cmdCursor(args []string) {
	if !r.ensureGarland() {
		return
	}

	line, lineRune := r.cursor.LinePos()
	fmt.Printf("Cursor Position:\n")
	fmt.Printf("  Byte:     %d\n", r.cursor.BytePos())
	fmt.Printf("  Rune:     %d\n", r.cursor.RunePos())
	fmt.Printf("  Line:     %d\n", line)
	fmt.Printf("  LineRune: %d\n", lineRune)
	fmt.Printf("  Ready:    %v\n", r.cursor.IsReady())
}

func (r *REPL) cmdSeek(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: seek byte|rune|line <pos> [<rune>]")
		return
	}

	mode := strings.ToLower(args[0])
	pos, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		fmt.Printf("Invalid position: %v\n", err)
		return
	}

	switch mode {
	case "byte":
		err = r.cursor.SeekByte(pos)
	case "rune":
		err = r.cursor.SeekRune(pos)
	case "line":
		runeInLine := int64(0)
		if len(args) >= 3 {
			runeInLine, err = strconv.ParseInt(args[2], 10, 64)
			if err != nil {
				fmt.Printf("Invalid rune position: %v\n", err)
				return
			}
		}
		err = r.cursor.SeekLine(pos, runeInLine)
	default:
		fmt.Println("Unknown seek mode. Use: byte, rune, or line")
		return
	}

	if err != nil {
		fmt.Printf("Seek error: %v\n", err)
		return
	}

	line, lineRune := r.cursor.LinePos()
	fmt.Printf("Cursor moved to byte=%d, rune=%d, line=%d:%d\n",
		r.cursor.BytePos(), r.cursor.RunePos(), line, lineRune)
}

func (r *REPL) cmdRead(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: read bytes|string <length>")
		return
	}

	mode := strings.ToLower(args[0])
	length, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		fmt.Printf("Invalid length: %v\n", err)
		return
	}

	switch mode {
	case "bytes":
		data, err := r.cursor.ReadBytes(length)
		if err != nil {
			fmt.Printf("Read error: %v\n", err)
			return
		}
		fmt.Printf("Read %d bytes: %q\n", len(data), string(data))
		fmt.Printf("Hex: %x\n", data)

	case "string":
		data, err := r.cursor.ReadString(length)
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

	data, err := r.cursor.ReadLine()
	if err != nil {
		fmt.Printf("Read error: %v\n", err)
		return
	}
	fmt.Printf("Line content: %q\n", data)
}

func (r *REPL) cmdInsert(args []string) {
	if !r.ensureGarland() {
		return
	}

	text := strings.Join(args, " ")
	if text == "" {
		fmt.Println("Usage: insert <text>")
		return
	}

	// Handle escape sequences
	text = strings.ReplaceAll(text, "\\n", "\n")
	text = strings.ReplaceAll(text, "\\t", "\t")

	result, err := r.cursor.InsertString(text, nil, false)
	if err != nil {
		fmt.Printf("Insert error: %v\n", err)
		return
	}
	fmt.Printf("Inserted %d bytes. Now at fork=%d, revision=%d\n",
		len(text), result.Fork, result.Revision)
}

func (r *REPL) cmdDelete(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: delete bytes|runes <length>")
		return
	}

	mode := strings.ToLower(args[0])
	length, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		fmt.Printf("Invalid length: %v\n", err)
		return
	}

	switch mode {
	case "bytes":
		_, result, err := r.cursor.DeleteBytes(length, false)
		if err != nil {
			fmt.Printf("Delete error: %v\n", err)
			return
		}
		fmt.Printf("Deleted %d bytes. Now at fork=%d, revision=%d\n",
			length, result.Fork, result.Revision)

	case "runes":
		_, result, err := r.cursor.DeleteRunes(length, false)
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

func (r *REPL) cmdDump() {
	if !r.ensureGarland() {
		return
	}

	// Save cursor position
	savedPos := r.cursor.BytePos()

	// Read all content
	r.cursor.SeekByte(0)
	byteCount := r.garland.ByteCount().Value
	data, err := r.cursor.ReadBytes(byteCount)
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
	r.cursor.SeekByte(savedPos)
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

func (r *REPL) cmdUndo() {
	fmt.Println("Undo not yet implemented")
}

func (r *REPL) cmdFork() {
	fmt.Println("Fork creation not yet implemented")
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
