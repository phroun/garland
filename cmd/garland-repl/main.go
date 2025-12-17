package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

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

	case "word":
		r.cmdWord(args)

	case "linestart":
		r.cmdLineStart()

	case "lineend":
		r.cmdLineEnd()

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

	case "move":
		r.cmdMove(args, false)

	case "move-":
		r.cmdMove(args, true)

	case "copy":
		r.cmdCopy(args, false)

	case "copy-":
		r.cmdCopy(args, true)

	case "truncate":
		r.cmdTruncate()

	case "delete":
		r.cmdDelete(args, false)

	case "delete+":
		r.cmdDelete(args, true)

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

	case "prune":
		r.cmdPrune(args)

	case "deletefork":
		r.cmdDeleteFork(args)

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

	case "save":
		r.cmdSave()

	case "saveas":
		r.cmdSaveAs(args)

	case "chill":
		r.cmdChill(args)

	case "thaw":
		r.cmdThaw(args)

	case "thawrange":
		r.cmdThawRange(args)

	case "convert":
		r.cmdConvert(args)

	case "divergences":
		r.cmdDivergences(args)

	case "dumpdecorations":
		r.cmdDumpDecorations(args)

	case "loaddecorations":
		r.cmdLoadDecorations(args)

	// Search commands
	case "find":
		r.cmdFind(args)

	case "findall":
		r.cmdFindAll(args)

	case "findnext":
		r.cmdFindNext(args)

	case "findregex":
		r.cmdFindRegex(args)

	case "findregexall":
		r.cmdFindRegexAll(args)

	case "findnextregex":
		r.cmdFindNextRegex(args)

	case "match":
		r.cmdMatch(args)

	case "replace":
		r.cmdReplace(args)

	case "replaceall":
		r.cmdReplaceAll(args)

	case "replacecount":
		r.cmdReplaceCount(args)

	case "replaceregex":
		r.cmdReplaceRegex(args)

	case "replaceregexall":
		r.cmdReplaceRegexAll(args)

	case "replaceregexcount":
		r.cmdReplaceRegexCount(args)

	case "count":
		r.cmdCount(args)

	case "countregex":
		r.cmdCountRegex(args)

	case "ready":
		r.cmdReady()

	case "isready":
		r.cmdIsReady(args)

	// Memory management commands
	case "memory":
		r.cmdMemory()

	case "memchill":
		r.cmdMemChill(args)

	case "rebalance":
		r.cmdRebalance()

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
  new <text>                Create a new garland with the given text content
  open <filepath>           Open a file from disk
  save                      Save to original file path
  saveas <filepath>         Save to a new file path
  close                     Close the current garland
  status                    Show current garland status

CURSOR OPERATIONS:
  cursor                    Show current cursor position
  cursor <name>             Switch to (or create) a named cursor
  cursor list               List all cursors and their positions
  cursor delete <name>      Delete a cursor
  seek byte <pos>           Move cursor to byte position
  seek rune <pos>           Move cursor to rune position
  seek line <line> <rune>   Move cursor to line:rune position
  relseek bytes <delta>     Move cursor relative (+ forward, - backward)
  relseek runes <delta>     Move cursor relative by runes

READ OPERATIONS:
  read bytes <length>       Read bytes from cursor position (advances cursor)
  read string <length>      Read runes from cursor position (advances cursor)
  readline                  Read the entire line at cursor position

EDIT OPERATIONS:
  insert "text"             Insert text at cursor position (advances cursor)
  insert "text", key=5      Insert with decoration at byte offset 5 in content
  insert- "text"            Insert BEFORE existing content at position
  overwrite <len> "text"    Replace <len> bytes at cursor with <text>
  move <src1> <src2> <dst1> [dst2]    Move bytes [src1,src2) to [dst1,dst2)
  move- <src1> <src2> <dst1> [dst2]   Move with decorations consolidated to end
  copy <src1> <src2> <dst1> [dst2]    Copy bytes [src1,src2) to [dst1,dst2)
  copy- <src1> <src2> <dst1> [dst2]   Copy with decorations consolidated to end
  truncate                  Delete from cursor to end of file
  delete bytes <length>     Delete bytes forward from cursor position
  delete runes <length>     Delete runes forward from cursor position
  delete+ bytes <length>    Delete bytes including line-anchored decorations
  delete+ runes <length>    Delete runes including line-anchored decorations
  backdelete bytes <len>    Delete bytes backward (like backspace)
  backdelete runes <len>    Delete runes backward (like backspace)

String arguments use quotes to allow spaces: insert "hello world"
Escape sequences: \n (newline), \t (tab), \" (quote), \\ (backslash)
Move/Copy: All addresses are original document positions. If dst2 omitted, dst2=dst1.

INSPECTION:
  dump                      Dump all content
  tree                      Show tree structure

VERSION CONTROL:
  tx start <name>           Start a transaction with optional name
  tx commit                 Commit the current transaction
  tx rollback               Rollback the current transaction
  undoseek <revision>       Seek to a specific revision in current fork
  revisions                 List revisions in current fork
  forks                     List all forks
  forkswitch <fork>         Switch to a different fork
  divergences               List fork divergence points for entire history
  divergences <from> <to>   List fork divergences in revision range
  version                   Show current fork and revision

NOTE: Forks are created automatically when you edit from a non-HEAD revision.
      Use 'forkswitch' to navigate between existing forks.

DECORATIONS:
  decorate <key>            Add decoration at cursor position
  decorate k=byte <pos>     Add decoration at byte position
  decorate k=rune <pos>     Add decoration at rune position
  decorate k=line <l>:<r>   Add decoration at line:rune position
  decorate k=nil            Remove decoration (same as undecorate)
  decorate a=byte 5, b=line 1:0   Multiple decorations at once
  undecorate <key>          Remove a decoration
  decorations               List all decorations in the file
  decorations <line>        List decorations on a specific line
  decoration <key>          Get the position of a specific decoration
  dumpdecorations <path>    Export all decorations to INI file

STORAGE TIERS:
  chill inactive            Chill data from inactive forks
  chill history             Chill old undo history (keep last 10 revisions)
  chill unused              Chill data not used at current revision
  chill all                 Chill all data to cold storage
  thaw                      Thaw all data for current fork (caution: large files)
  thaw <start> <end>        Thaw specific revision range (caution: large files)
  thawrange <start> <end>   Thaw specific byte range (RAM-safe for large files)

POSITION CONVERSION:
  convert byte <pos>        Convert byte position to rune/line
  convert rune <pos>        Convert rune position to byte/line
  convert line <l> <r>      Convert line:rune position to byte/rune

SEARCH & REPLACE:
  find "needle" [flags]     Find first occurrence from cursor
  findall "needle" [flags]  Find all occurrences
  findnext "needle" [flags] Find next and move cursor to it
  findregex "pattern" [flags]    Find first regex match
  findregexall "pattern" [flags] Find all regex matches
  findnextregex "pattern" [flags] Find next regex match, move cursor
  match "pattern" [flags]   Check if regex matches at cursor position
  replace "needle" "repl" [flags]    Replace first occurrence
  replaceall "needle" "repl" [flags] Replace all occurrences
  replacecount "needle" "repl" <n> [flags] Replace up to n occurrences
  replaceregex "pattern" "repl" [flags]    Replace first regex match
  replaceregexall "pattern" "repl" [flags] Replace all regex matches
  replaceregexcount "pattern" "repl" <n> [flags] Replace up to n regex matches
  count "needle" [flags]    Count occurrences
  countregex "pattern" [flags] Count regex matches

Search flags: -i (case insensitive), -w (whole word), -b (backward)
Regex flags: -i (case insensitive), -b (backward)
Regex replacement supports $1, $2, etc. for capture groups.

STREAMING/LAZY LOADING:
  ready                     Show loading status (complete, bytes/runes/lines loaded)
  isready byte <pos>        Check if byte position is ready (non-blocking)
  isready rune <pos>        Check if rune position is ready (non-blocking)
  isready line <line>       Check if line is ready (non-blocking)

Note: During streaming input (via DataChannel), these commands let you check
if a position is available before seeking. Seek operations block by default
until data arrives. Use isready to guard against blocking.

MEMORY MANAGEMENT:
  memory                    Show current memory usage statistics
  memchill [count]          Incrementally chill LRU nodes (default: 5 nodes)
  rebalance                 Force tree rebalancing (use sparingly)

Note: Memory management is automatic when soft/hard limits are configured in
LibraryOptions. These commands allow manual intervention for debugging.

OTHER:
  help                      Show this help message
  quit, exit                Exit the REPL
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
	if len(args) < 1 {
		fmt.Println("Usage: open <filepath>")
		return
	}

	path := strings.Join(args, " ")

	if r.garland != nil {
		r.garland.Close()
	}

	g, err := r.lib.Open(garland.FileOptions{
		FilePath: path,
	})
	if err != nil {
		fmt.Printf("Error opening file: %v\n", err)
		return
	}

	r.garland = g
	r.cursors = make(map[string]*garland.Cursor)
	r.cursors["default"] = g.NewCursor()
	r.currentCursor = "default"
	fmt.Printf("Opened %s (%d bytes)\n", path, g.ByteCount().Value)
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

	// Handle subcommands: cursor, cursor <name>, cursor list, cursor delete <name>
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

		if subcmd == "delete" {
			if len(args) < 2 {
				fmt.Println("Usage: cursor delete <name>")
				return
			}
			name := args[1]
			if name == "default" {
				fmt.Println("Cannot delete the default cursor")
				return
			}
			c, exists := r.cursors[name]
			if !exists {
				fmt.Printf("Cursor '%s' not found\n", name)
				return
			}
			r.garland.RemoveCursor(c)
			delete(r.cursors, name)
			if r.currentCursor == name {
				r.currentCursor = "default"
			}
			fmt.Printf("Deleted cursor '%s'\n", name)
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

func (r *REPL) cmdWord(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: word <count>")
		fmt.Println("  count can be positive (forward) or negative (backward)")
		return
	}

	cursor := r.cursor()
	count, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Printf("Invalid count: %v\n", err)
		return
	}

	moved, err := cursor.SeekByWord(count)
	if err != nil {
		fmt.Printf("Word seek error: %v\n", err)
		return
	}

	line, lineRune := cursor.LinePos()
	fmt.Printf("Moved %d word(s), cursor at byte=%d, rune=%d, line=%d:%d\n",
		moved, cursor.BytePos(), cursor.RunePos(), line, lineRune)
}

func (r *REPL) cmdLineStart() {
	if !r.ensureGarland() {
		return
	}

	cursor := r.cursor()
	err := cursor.SeekLineStart()
	if err != nil {
		fmt.Printf("LineStart error: %v\n", err)
		return
	}

	line, lineRune := cursor.LinePos()
	fmt.Printf("Cursor moved to byte=%d, rune=%d, line=%d:%d\n",
		cursor.BytePos(), cursor.RunePos(), line, lineRune)
}

func (r *REPL) cmdLineEnd() {
	if !r.ensureGarland() {
		return
	}

	cursor := r.cursor()
	err := cursor.SeekLineEnd()
	if err != nil {
		fmt.Printf("LineEnd error: %v\n", err)
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

	fullInput := strings.Join(args, " ")
	if fullInput == "" {
		fmt.Println("Usage: insert \"text\"")
		fmt.Println("       insert \"text\", key=5, key2=10  (with decorations at byte offsets)")
		fmt.Println("       insert- \"text\"  (insert before existing content)")
		return
	}

	// Parse quoted string and optional decorations
	text, remainder, err := r.parseQuotedString(fullInput)
	if err != nil {
		fmt.Printf("Parse error: %v\n", err)
		return
	}

	// Parse optional decorations after the string
	var decorations []garland.RelativeDecoration
	if remainder != "" {
		// Remainder should start with a comma (or just have decorations)
		remainder = strings.TrimSpace(remainder)
		if strings.HasPrefix(remainder, ",") {
			remainder = strings.TrimSpace(remainder[1:])
		}
		if remainder != "" {
			decorations, err = r.parseRelativeDecorations(remainder)
			if err != nil {
				fmt.Printf("Decoration parse error: %v\n", err)
				return
			}
		}
	}

	cursor := r.cursor()
	result, err := cursor.InsertString(text, decorations, insertBefore)
	if err != nil {
		fmt.Printf("Insert error: %v\n", err)
		return
	}
	beforeStr := ""
	if insertBefore {
		beforeStr = " (before)"
	}
	decStr := ""
	if len(decorations) > 0 {
		decStr = fmt.Sprintf(" with %d decoration(s)", len(decorations))
	}
	fmt.Printf("Inserted %d bytes%s%s. Now at fork=%d, revision=%d\n",
		len(text), beforeStr, decStr, result.Fork, result.Revision)
}

// parseQuotedString extracts a quoted string and returns the content and remainder
func (r *REPL) parseQuotedString(input string) (string, string, error) {
	input = strings.TrimSpace(input)
	if len(input) == 0 {
		return "", "", fmt.Errorf("empty input")
	}

	if input[0] != '"' {
		return "", "", fmt.Errorf("expected quoted string (starting with \")")
	}

	// Parse the quoted string, handling escapes
	var result []byte
	i := 1
	for i < len(input) {
		if input[i] == '\\' && i+1 < len(input) {
			// Handle escape sequences
			switch input[i+1] {
			case 'n':
				result = append(result, '\n')
			case 't':
				result = append(result, '\t')
			case '"':
				result = append(result, '"')
			case '\\':
				result = append(result, '\\')
			default:
				// Unknown escape, keep as-is
				result = append(result, input[i], input[i+1])
			}
			i += 2
		} else if input[i] == '"' {
			// End of string
			remainder := strings.TrimSpace(input[i+1:])
			return string(result), remainder, nil
		} else {
			result = append(result, input[i])
			i++
		}
	}

	return "", "", fmt.Errorf("unterminated string (missing closing \")")
}

// parseRelativeDecorations parses decoration specs relative to inserted content
// Format: key=5, key2=10  (byte offsets within the inserted content)
func (r *REPL) parseRelativeDecorations(input string) ([]garland.RelativeDecoration, error) {
	parts := strings.Split(input, ",")
	var decorations []garland.RelativeDecoration

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		idx := strings.Index(part, "=")
		if idx <= 0 {
			return nil, fmt.Errorf("invalid decoration spec: %q (expected key=position)", part)
		}

		key := strings.TrimSpace(part[:idx])
		posStr := strings.TrimSpace(part[idx+1:])

		pos, err := strconv.ParseInt(posStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid position for %q: %v", key, err)
		}

		decorations = append(decorations, garland.RelativeDecoration{
			Key:      key,
			Position: pos,
		})
	}

	return decorations, nil
}

func (r *REPL) cmdOverwrite(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: overwrite <length> \"text\"")
		fmt.Println("  Replaces <length> bytes at cursor with <text>")
		return
	}

	length, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		fmt.Printf("Invalid length: %v\n", err)
		return
	}

	// Join remaining args and parse quoted string
	fullInput := strings.Join(args[1:], " ")
	text, _, err := r.parseQuotedString(fullInput)
	if err != nil {
		fmt.Printf("Parse error: %v\n", err)
		return
	}

	cursor := r.cursor()
	_, result, err := cursor.OverwriteBytes(length, []byte(text))
	if err != nil {
		fmt.Printf("Overwrite error: %v\n", err)
		return
	}
	fmt.Printf("Overwrote %d bytes with %d bytes. Now at fork=%d, revision=%d\n",
		length, len(text), result.Fork, result.Revision)
}

func (r *REPL) cmdMove(args []string, insertBefore bool) {
	if !r.ensureGarland() {
		return
	}

	// Syntax: move srcStart srcEnd dstStart dstEnd
	// Or: move srcStart srcEnd dstStart (same as dstStart dstStart - insertion point)
	if len(args) < 3 {
		fmt.Println("Usage: move <srcStart> <srcEnd> <dstStart> [dstEnd]")
		fmt.Println("       move- <srcStart> <srcEnd> <dstStart> [dstEnd]")
		fmt.Println("  Moves bytes [srcStart, srcEnd) to replace [dstStart, dstEnd)")
		fmt.Println("  If dstEnd omitted, dstEnd = dstStart (insertion point)")
		fmt.Println("  move- consolidates displaced decorations to end instead of start")
		fmt.Println("  Source and destination ranges cannot overlap")
		return
	}

	srcStart, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		fmt.Printf("Invalid srcStart: %v\n", err)
		return
	}

	srcEnd, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		fmt.Printf("Invalid srcEnd: %v\n", err)
		return
	}

	dstStart, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		fmt.Printf("Invalid dstStart: %v\n", err)
		return
	}

	dstEnd := dstStart // Default: insertion point
	if len(args) >= 4 {
		dstEnd, err = strconv.ParseInt(args[3], 10, 64)
		if err != nil {
			fmt.Printf("Invalid dstEnd: %v\n", err)
			return
		}
	}

	cursor := r.cursor()
	result, err := cursor.MoveBytes(srcStart, srcEnd, dstStart, dstEnd, insertBefore)
	if err != nil {
		fmt.Printf("Move error: %v\n", err)
		return
	}

	fmt.Printf("Moved %d bytes from [%d,%d) to [%d,%d). Now at fork=%d, revision=%d\n",
		srcEnd-srcStart, srcStart, srcEnd, dstStart, dstEnd, result.Fork, result.Revision)
	if len(result.DisplacedDecorations) > 0 {
		fmt.Printf("Displaced decorations from destination: %d\n", len(result.DisplacedDecorations))
		for _, d := range result.DisplacedDecorations {
			fmt.Printf("  %s @ relative position %d\n", d.Key, d.Position)
		}
	}
}

func (r *REPL) cmdCopy(args []string, insertBefore bool) {
	if !r.ensureGarland() {
		return
	}

	// Syntax: copy srcStart srcEnd dstStart [dstEnd] ["decorations", key=pos, ...]
	if len(args) < 3 {
		fmt.Println("Usage: copy <srcStart> <srcEnd> <dstStart> [dstEnd]")
		fmt.Println("       copy- <srcStart> <srcEnd> <dstStart> [dstEnd]")
		fmt.Println("  Copies bytes [srcStart, srcEnd) to replace [dstStart, dstEnd)")
		fmt.Println("  If dstEnd omitted, dstEnd = dstStart (insertion point)")
		fmt.Println("  copy- consolidates displaced decorations to end instead of start")
		fmt.Println("  Source and destination ranges may overlap")
		return
	}

	srcStart, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		fmt.Printf("Invalid srcStart: %v\n", err)
		return
	}

	srcEnd, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		fmt.Printf("Invalid srcEnd: %v\n", err)
		return
	}

	dstStart, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		fmt.Printf("Invalid dstStart: %v\n", err)
		return
	}

	dstEnd := dstStart // Default: insertion point
	if len(args) >= 4 {
		// Try to parse as number; if fails, might be decoration syntax
		var parseErr error
		dstEnd, parseErr = strconv.ParseInt(args[3], 10, 64)
		if parseErr != nil {
			dstEnd = dstStart // Keep as insertion point
		}
	}

	cursor := r.cursor()
	result, err := cursor.CopyBytes(srcStart, srcEnd, dstStart, dstEnd, nil, insertBefore)
	if err != nil {
		fmt.Printf("Copy error: %v\n", err)
		return
	}

	fmt.Printf("Copied %d bytes from [%d,%d) to [%d,%d). Now at fork=%d, revision=%d\n",
		srcEnd-srcStart, srcStart, srcEnd, dstStart, dstEnd, result.Fork, result.Revision)
	if len(result.DisplacedDecorations) > 0 {
		fmt.Printf("Displaced decorations from destination: %d\n", len(result.DisplacedDecorations))
		for _, d := range result.DisplacedDecorations {
			fmt.Printf("  %s @ relative position %d\n", d.Key, d.Position)
		}
	}
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

func (r *REPL) cmdDelete(args []string, includeLineDecorations bool) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: delete bytes|runes <length>")
		fmt.Println("       delete+ bytes|runes <length>  (includes line-anchored decorations)")
		return
	}

	cursor := r.cursor()
	mode := strings.ToLower(args[0])
	length, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		fmt.Printf("Invalid length: %v\n", err)
		return
	}

	flagStr := ""
	if includeLineDecorations {
		flagStr = " (including line decorations)"
	}

	switch mode {
	case "bytes":
		_, result, err := cursor.DeleteBytes(length, includeLineDecorations)
		if err != nil {
			fmt.Printf("Delete error: %v\n", err)
			return
		}
		fmt.Printf("Deleted %d bytes%s. Now at fork=%d, revision=%d\n",
			length, flagStr, result.Fork, result.Revision)

	case "runes":
		_, result, err := cursor.DeleteRunes(length, includeLineDecorations)
		if err != nil {
			fmt.Printf("Delete error: %v\n", err)
			return
		}
		fmt.Printf("Deleted %d runes%s. Now at fork=%d, revision=%d\n",
			length, flagStr, result.Fork, result.Revision)

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

	// Collect cursor positions BEFORE reading (reading advances cursors)
	type cursorPos struct {
		name string
		pos  int64
	}
	var cursorPositions []cursorPos
	for name, c := range r.cursors {
		cursorPositions = append(cursorPositions, cursorPos{name: name, pos: c.BytePos()})
	}
	sort.Slice(cursorPositions, func(i, j int) bool {
		return cursorPositions[i].pos < cursorPositions[j].pos
	})

	// Read all content
	cursor.SeekByte(0)
	byteCount := r.garland.ByteCount().Value
	data, err := cursor.ReadBytes(byteCount)
	if err != nil {
		fmt.Printf("Read error: %v\n", err)
		return
	}

	// Build output with cursor markers inserted at their positions
	// ANSI: \x1b[1;32m = bold green, \x1b[0m = reset
	var output strings.Builder
	dataStr := string(data)
	lastPos := int64(0)

	// Group cursors at the same position
	for i := 0; i < len(cursorPositions); {
		pos := cursorPositions[i].pos

		// Output text from last position to this cursor position
		if pos > lastPos && lastPos < int64(len(dataStr)) {
			endPos := pos
			if endPos > int64(len(dataStr)) {
				endPos = int64(len(dataStr))
			}
			output.WriteString(dataStr[lastPos:endPos])
		}

		// Collect all cursors at this position
		var cursorsHere []string
		for i < len(cursorPositions) && cursorPositions[i].pos == pos {
			cursorsHere = append(cursorsHere, cursorPositions[i].name)
			i++
		}

		// Output cursor marker(s)
		output.WriteString("\x1b[1;32m(")
		output.WriteString(strings.Join(cursorsHere, ","))
		output.WriteString(")\x1b[0m")

		lastPos = pos
	}

	// Output remaining text after last cursor
	if lastPos < int64(len(dataStr)) {
		output.WriteString(dataStr[lastPos:])
	}

	fmt.Println("Content:")
	fmt.Println("--------")
	fmt.Printf("%s\n", output.String())
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

	treeInfo := r.garland.GetTreeInfo()
	if treeInfo == nil {
		fmt.Println("No tree structure available")
		return
	}

	fmt.Printf("Tree structure (fork=%d, rev=%d):\n",
		r.garland.CurrentFork(), r.garland.CurrentRevision())
	fmt.Println()
	r.printTreeNode(treeInfo, "", true)
}

// printTreeNode recursively prints a tree node with line-drawing characters
func (r *REPL) printTreeNode(node *garland.TreeNodeInfo, prefix string, isLast bool) {
	if node == nil {
		return
	}

	// Line drawing characters (UTF-8)
	// ├── for non-last children
	// └── for last child
	// │   for continuation
	connector := "├── "
	if isLast {
		connector = "└── "
	}

	// Build node description
	var desc string
	storageLabel := storageStateString(node.Storage)

	if node.IsLeaf {
		if node.DataPreview != "" {
			desc = fmt.Sprintf("LEAF[%d] %dB %dR %dL [%s] \"%s\"",
				node.NodeID, node.ByteCount, node.RuneCount, node.LineCount,
				storageLabel, node.DataPreview)
		} else {
			desc = fmt.Sprintf("LEAF[%d] %dB %dR %dL [%s] (empty/cold)",
				node.NodeID, node.ByteCount, node.RuneCount, node.LineCount,
				storageLabel)
		}
	} else {
		desc = fmt.Sprintf("NODE[%d] %dB %dR %dL",
			node.NodeID, node.ByteCount, node.RuneCount, node.LineCount)
	}

	fmt.Printf("%s%s%s\n", prefix, connector, desc)

	// Determine child prefix
	childPrefix := prefix
	if isLast {
		childPrefix += "    "
	} else {
		childPrefix += "│   "
	}

	// Print children
	for i, child := range node.Children {
		isChildLast := (i == len(node.Children)-1)
		r.printTreeNode(child, childPrefix, isChildLast)
	}
}

// storageStateString returns a short label for a storage state
func storageStateString(s garland.StorageState) string {
	switch s {
	case garland.StorageMemory:
		return "mem"
	case garland.StorageWarm:
		return "warm"
	case garland.StorageCold:
		return "cold"
	case garland.StoragePlaceholder:
		return "placeholder"
	default:
		return "?"
	}
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
		prunedInfo := ""
		if info.PrunedUpTo > 0 {
			prunedInfo = fmt.Sprintf(" [pruned<%d]", info.PrunedUpTo)
		}
		deletedInfo := ""
		if info.Deleted {
			deletedInfo = " [DELETED]"
		}
		fmt.Printf("%s%d: highest revision %d%s%s%s\n", marker, info.ID, info.HighestRevision, parentInfo, prunedInfo, deletedInfo)
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

func (r *REPL) cmdPrune(args []string) {
	if !r.ensureGarland() {
		return
	}

	g := r.garland

	if len(args) < 1 {
		fmt.Println("Usage: prune <keep_from_revision>")
		fmt.Println("  Removes revision history before keep_from_revision in current fork")
		fmt.Println("  Revisions >= keep_from_revision are kept")
		forkInfo, err := g.GetForkInfo(g.CurrentFork())
		if err == nil {
			fmt.Printf("Current fork: %d (revision %d, pruned up to %d)\n",
				g.CurrentFork(), g.CurrentRevision(), forkInfo.PrunedUpTo)
		}
		return
	}

	keepFrom, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		fmt.Printf("Invalid revision: %v\n", err)
		return
	}

	err = g.Prune(garland.RevisionID(keepFrom))
	if err != nil {
		fmt.Printf("Prune error: %v\n", err)
		return
	}

	forkInfo, _ := g.GetForkInfo(g.CurrentFork())
	fmt.Printf("Fork %d pruned: revisions before %d removed\n", g.CurrentFork(), forkInfo.PrunedUpTo)
}

func (r *REPL) cmdDeleteFork(args []string) {
	if !r.ensureGarland() {
		return
	}

	g := r.garland

	if len(args) < 1 {
		fmt.Println("Usage: deletefork <fork_id>")
		fmt.Println("  Soft-deletes a fork (cannot switch to it anymore)")
		fmt.Println("  Cannot delete fork 0 or current fork")
		fmt.Printf("Current fork: %d\n", g.CurrentFork())
		return
	}

	forkID, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		fmt.Printf("Invalid fork ID: %v\n", err)
		return
	}

	err = g.DeleteFork(garland.ForkID(forkID))
	if err != nil {
		fmt.Printf("DeleteFork error: %v\n", err)
		return
	}

	fmt.Printf("Fork %d deleted\n", forkID)
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

func (r *REPL) cmdSave() {
	if !r.ensureGarland() {
		return
	}

	err := r.garland.Save()
	if err != nil {
		fmt.Printf("Save error: %v\n", err)
		return
	}

	fmt.Println("File saved")
}

func (r *REPL) cmdSaveAs(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: saveas <filepath>")
		return
	}

	path := strings.Join(args, " ")
	err := r.garland.SaveAs(nil, path)
	if err != nil {
		fmt.Printf("SaveAs error: %v\n", err)
		return
	}

	fmt.Printf("File saved to %s\n", path)
}

func (r *REPL) cmdChill(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: chill inactive|history|unused|all")
		fmt.Println("  inactive  - Chill data from inactive forks")
		fmt.Println("  history   - Chill old undo history (keep last 10 revisions)")
		fmt.Println("  unused    - Chill data not used at current revision")
		fmt.Println("  all       - Chill all data to cold storage")
		return
	}

	var level garland.ChillLevel
	levelName := strings.ToLower(args[0])
	switch levelName {
	case "inactive":
		level = garland.ChillInactiveForks
	case "history":
		level = garland.ChillOldHistory
	case "unused":
		level = garland.ChillUnusedData
	case "all":
		level = garland.ChillEverything
	default:
		fmt.Printf("Unknown chill level: %s\n", levelName)
		fmt.Println("Use: inactive, history, unused, or all")
		return
	}

	err := r.garland.Chill(level)
	if err != nil {
		fmt.Printf("Chill error: %v\n", err)
		return
	}

	fmt.Printf("Chilled data with level: %s\n", levelName)
}

func (r *REPL) cmdThaw(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) == 0 {
		// Thaw all for current fork
		err := r.garland.Thaw()
		if err != nil {
			fmt.Printf("Thaw error: %v\n", err)
			return
		}
		fmt.Println("Thawed all data for current fork")
		return
	}

	if len(args) >= 2 {
		// Thaw specific revision range
		startRev, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			fmt.Printf("Invalid start revision: %v\n", err)
			return
		}
		endRev, err := strconv.ParseUint(args[1], 10, 64)
		if err != nil {
			fmt.Printf("Invalid end revision: %v\n", err)
			return
		}

		err = r.garland.ThawRevision(garland.RevisionID(startRev), garland.RevisionID(endRev))
		if err != nil {
			fmt.Printf("ThawRevision error: %v\n", err)
			return
		}
		fmt.Printf("Thawed revisions %d to %d\n", startRev, endRev)
		return
	}

	fmt.Println("Usage: thaw              - Thaw all data for current fork")
	fmt.Println("       thaw <start> <end> - Thaw specific revision range")
}

func (r *REPL) cmdThawRange(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: thawrange <start_byte> <end_byte>")
		fmt.Println("  Thaws only the nodes covering the specified byte range")
		fmt.Println("  This is RAM-safe for large files")
		return
	}

	startByte, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		fmt.Printf("Invalid start byte: %v\n", err)
		return
	}
	endByte, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		fmt.Printf("Invalid end byte: %v\n", err)
		return
	}

	err = r.garland.ThawRange(startByte, endByte)
	if err != nil {
		fmt.Printf("ThawRange error: %v\n", err)
		return
	}

	fmt.Printf("Thawed byte range %d to %d\n", startByte, endByte)
}

func (r *REPL) cmdConvert(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: convert byte <pos>         - Convert byte to rune/line")
		fmt.Println("       convert rune <pos>         - Convert rune to byte/line")
		fmt.Println("       convert line <line> <rune> - Convert line:rune to byte/rune")
		return
	}

	mode := strings.ToLower(args[0])

	switch mode {
	case "byte":
		pos, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			fmt.Printf("Invalid byte position: %v\n", err)
			return
		}

		runePos, err := r.garland.ByteToRune(pos)
		if err != nil {
			fmt.Printf("Error converting: %v\n", err)
			return
		}

		line, lineRune, err := r.garland.ByteToLineRune(pos)
		if err != nil {
			fmt.Printf("Error converting: %v\n", err)
			return
		}

		fmt.Printf("Byte %d =\n", pos)
		fmt.Printf("  Rune: %d\n", runePos)
		fmt.Printf("  Line: %d:%d\n", line, lineRune)

	case "rune":
		pos, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			fmt.Printf("Invalid rune position: %v\n", err)
			return
		}

		bytePos, err := r.garland.RuneToByte(pos)
		if err != nil {
			fmt.Printf("Error converting: %v\n", err)
			return
		}

		line, lineRune, err := r.garland.ByteToLineRune(bytePos)
		if err != nil {
			fmt.Printf("Error converting: %v\n", err)
			return
		}

		fmt.Printf("Rune %d =\n", pos)
		fmt.Printf("  Byte: %d\n", bytePos)
		fmt.Printf("  Line: %d:%d\n", line, lineRune)

	case "line":
		if len(args) < 3 {
			fmt.Println("Usage: convert line <line> <rune>")
			return
		}
		line, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			fmt.Printf("Invalid line number: %v\n", err)
			return
		}
		runeInLine, err := strconv.ParseInt(args[2], 10, 64)
		if err != nil {
			fmt.Printf("Invalid rune position: %v\n", err)
			return
		}

		bytePos, err := r.garland.LineRuneToByte(line, runeInLine)
		if err != nil {
			fmt.Printf("Error converting: %v\n", err)
			return
		}

		runePos, err := r.garland.ByteToRune(bytePos)
		if err != nil {
			fmt.Printf("Error converting: %v\n", err)
			return
		}

		fmt.Printf("Line %d:%d =\n", line, runeInLine)
		fmt.Printf("  Byte: %d\n", bytePos)
		fmt.Printf("  Rune: %d\n", runePos)

	default:
		fmt.Println("Unknown mode. Use: byte, rune, or line")
	}
}

func (r *REPL) cmdDivergences(args []string) {
	if !r.ensureGarland() {
		return
	}

	g := r.garland
	forkInfo, err := g.GetForkInfo(g.CurrentFork())
	if err != nil {
		fmt.Printf("Error getting fork info: %v\n", err)
		return
	}

	// Default to full revision range
	startRev := garland.RevisionID(0)
	endRev := forkInfo.HighestRevision

	// Parse optional revision range
	if len(args) >= 2 {
		start, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			fmt.Printf("Invalid start revision: %v\n", err)
			return
		}
		end, err := strconv.ParseUint(args[1], 10, 64)
		if err != nil {
			fmt.Printf("Invalid end revision: %v\n", err)
			return
		}
		startRev = garland.RevisionID(start)
		endRev = garland.RevisionID(end)
	}

	divergences, err := g.FindForksBetween(startRev, endRev)
	if err != nil {
		fmt.Printf("Error finding divergences: %v\n", err)
		return
	}

	if len(divergences) == 0 {
		fmt.Printf("No fork divergences in revisions %d to %d\n", startRev, endRev)
		return
	}

	fmt.Printf("Fork divergences in revisions %d to %d:\n", startRev, endRev)
	for _, d := range divergences {
		dirStr := "branched into"
		if d.Direction == garland.BranchedFrom {
			dirStr = "branched from"
		}
		fmt.Printf("  Revision %d: %s fork %d\n", d.DivergenceRev, dirStr, d.Fork)
	}
}

func (r *REPL) cmdDumpDecorations(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: dumpdecorations <filepath>")
		fmt.Println("  Exports all decorations to an INI file")
		return
	}

	path := strings.Join(args, " ")
	err := r.garland.DumpDecorations(nil, path)
	if err != nil {
		fmt.Printf("DumpDecorations error: %v\n", err)
		return
	}

	fmt.Printf("Decorations exported to %s\n", path)
}

func (r *REPL) cmdLoadDecorations(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: loaddecorations <filepath>")
		fmt.Println("  Loads decorations from an INI file")
		fmt.Println("  Format: [decorations] section with key=byteposition entries")
		return
	}

	path := strings.Join(args, " ")
	err := r.garland.LoadDecorations(nil, path)
	if err != nil {
		fmt.Printf("LoadDecorations error: %v\n", err)
		return
	}

	fmt.Printf("Decorations loaded from %s\n", path)
}

// parseSearchFlags parses flags from args: -i (case insensitive), -w (whole word), -b (backward)
func parseSearchFlags(args []string) (garland.SearchOptions, []string) {
	opts := garland.SearchOptions{
		CaseSensitive: true, // Default to case sensitive
		WholeWord:     false,
		Backward:      false,
	}

	var remaining []string
	for _, arg := range args {
		switch arg {
		case "-i":
			opts.CaseSensitive = false
		case "-w":
			opts.WholeWord = true
		case "-b":
			opts.Backward = true
		default:
			remaining = append(remaining, arg)
		}
	}
	return opts, remaining
}

// parseRegexFlags parses flags from args: -i (case insensitive), -b (backward)
func parseRegexFlags(args []string) (garland.RegexOptions, []string) {
	opts := garland.RegexOptions{
		CaseInsensitive: false,
		Backward:        false,
	}

	var remaining []string
	for _, arg := range args {
		switch arg {
		case "-i":
			opts.CaseInsensitive = true
		case "-b":
			opts.Backward = true
		default:
			remaining = append(remaining, arg)
		}
	}
	return opts, remaining
}

func (r *REPL) cmdFind(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: find \"needle\" [-i] [-w] [-b]")
		fmt.Println("  -i: case insensitive")
		fmt.Println("  -w: whole word only")
		fmt.Println("  -b: search backward")
		return
	}

	opts, remaining := parseSearchFlags(args)
	if len(remaining) < 1 {
		fmt.Println("Usage: find \"needle\" [flags]")
		return
	}

	needle, _, err := r.parseQuotedString(strings.Join(remaining, " "))
	if err != nil {
		fmt.Printf("Parse error: %v\n", err)
		return
	}

	cursor := r.cursor()
	match, err := cursor.FindString(needle, opts)
	if err != nil {
		fmt.Printf("Find error: %v\n", err)
		return
	}

	if match == nil {
		fmt.Println("No match found")
		return
	}

	fmt.Printf("Found at byte %d-%d: %q\n", match.ByteStart, match.ByteEnd, match.Match)
}

func (r *REPL) cmdFindAll(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: findall \"needle\" [-i] [-w] [-b]")
		return
	}

	opts, remaining := parseSearchFlags(args)
	if len(remaining) < 1 {
		fmt.Println("Usage: findall \"needle\" [flags]")
		return
	}

	needle, _, err := r.parseQuotedString(strings.Join(remaining, " "))
	if err != nil {
		fmt.Printf("Parse error: %v\n", err)
		return
	}

	cursor := r.cursor()
	matches, err := cursor.FindStringAll(needle, opts)
	if err != nil {
		fmt.Printf("Find error: %v\n", err)
		return
	}

	if len(matches) == 0 {
		fmt.Println("No matches found")
		return
	}

	fmt.Printf("Found %d matches:\n", len(matches))
	for i, match := range matches {
		fmt.Printf("  %d. byte %d-%d: %q\n", i+1, match.ByteStart, match.ByteEnd, match.Match)
	}
}

func (r *REPL) cmdFindNext(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: findnext \"needle\" [-i] [-w] [-b]")
		return
	}

	opts, remaining := parseSearchFlags(args)
	if len(remaining) < 1 {
		fmt.Println("Usage: findnext \"needle\" [flags]")
		return
	}

	needle, _, err := r.parseQuotedString(strings.Join(remaining, " "))
	if err != nil {
		fmt.Printf("Parse error: %v\n", err)
		return
	}

	cursor := r.cursor()
	match, err := cursor.FindNext(needle, opts)
	if err != nil {
		fmt.Printf("Find error: %v\n", err)
		return
	}

	if match == nil {
		fmt.Println("No match found")
		return
	}

	line, lineRune := cursor.LinePos()
	fmt.Printf("Found at byte %d-%d: %q\n", match.ByteStart, match.ByteEnd, match.Match)
	fmt.Printf("Cursor moved to byte=%d, line=%d:%d\n", cursor.BytePos(), line, lineRune)
}

func (r *REPL) cmdFindRegex(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: findregex \"pattern\" [-i] [-b]")
		fmt.Println("  -i: case insensitive")
		fmt.Println("  -b: search backward")
		return
	}

	opts, remaining := parseRegexFlags(args)
	if len(remaining) < 1 {
		fmt.Println("Usage: findregex \"pattern\" [flags]")
		return
	}

	pattern, _, err := r.parseQuotedString(strings.Join(remaining, " "))
	if err != nil {
		fmt.Printf("Parse error: %v\n", err)
		return
	}

	cursor := r.cursor()
	match, err := cursor.FindRegex(pattern, opts)
	if err != nil {
		fmt.Printf("Find error: %v\n", err)
		return
	}

	if match == nil {
		fmt.Println("No match found")
		return
	}

	fmt.Printf("Found at byte %d-%d: %q\n", match.ByteStart, match.ByteEnd, match.Match)
}

func (r *REPL) cmdFindRegexAll(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: findregexall \"pattern\" [-i] [-b]")
		return
	}

	opts, remaining := parseRegexFlags(args)
	if len(remaining) < 1 {
		fmt.Println("Usage: findregexall \"pattern\" [flags]")
		return
	}

	pattern, _, err := r.parseQuotedString(strings.Join(remaining, " "))
	if err != nil {
		fmt.Printf("Parse error: %v\n", err)
		return
	}

	cursor := r.cursor()
	matches, err := cursor.FindRegexAll(pattern, opts)
	if err != nil {
		fmt.Printf("Find error: %v\n", err)
		return
	}

	if len(matches) == 0 {
		fmt.Println("No matches found")
		return
	}

	fmt.Printf("Found %d matches:\n", len(matches))
	for i, match := range matches {
		fmt.Printf("  %d. byte %d-%d: %q\n", i+1, match.ByteStart, match.ByteEnd, match.Match)
	}
}

func (r *REPL) cmdFindNextRegex(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: findnextregex \"pattern\" [-i] [-b]")
		return
	}

	opts, remaining := parseRegexFlags(args)
	if len(remaining) < 1 {
		fmt.Println("Usage: findnextregex \"pattern\" [flags]")
		return
	}

	pattern, _, err := r.parseQuotedString(strings.Join(remaining, " "))
	if err != nil {
		fmt.Printf("Parse error: %v\n", err)
		return
	}

	cursor := r.cursor()
	match, err := cursor.FindNextRegex(pattern, opts)
	if err != nil {
		fmt.Printf("Find error: %v\n", err)
		return
	}

	if match == nil {
		fmt.Println("No match found")
		return
	}

	line, lineRune := cursor.LinePos()
	fmt.Printf("Found at byte %d-%d: %q\n", match.ByteStart, match.ByteEnd, match.Match)
	fmt.Printf("Cursor moved to byte=%d, line=%d:%d\n", cursor.BytePos(), line, lineRune)
}

func (r *REPL) cmdMatch(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: match \"pattern\" [-i]")
		fmt.Println("  Checks if regex matches at current cursor position")
		return
	}

	caseInsensitive := false
	var remaining []string
	for _, arg := range args {
		if arg == "-i" {
			caseInsensitive = true
		} else {
			remaining = append(remaining, arg)
		}
	}

	if len(remaining) < 1 {
		fmt.Println("Usage: match \"pattern\" [-i]")
		return
	}

	pattern, _, err := r.parseQuotedString(strings.Join(remaining, " "))
	if err != nil {
		fmt.Printf("Parse error: %v\n", err)
		return
	}

	cursor := r.cursor()
	matches, result, err := cursor.MatchRegex(pattern, caseInsensitive)
	if err != nil {
		fmt.Printf("Match error: %v\n", err)
		return
	}

	if !matches {
		fmt.Println("No match at cursor position")
		return
	}

	fmt.Printf("Match found: %q (bytes %d-%d)\n", result.Match, result.ByteStart, result.ByteEnd)
}

func (r *REPL) cmdReplace(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: replace \"needle\" \"replacement\" [-i] [-w] [-b]")
		return
	}

	opts, remaining := parseSearchFlags(args)
	if len(remaining) < 2 {
		fmt.Println("Usage: replace \"needle\" \"replacement\" [flags]")
		return
	}

	fullInput := strings.Join(remaining, " ")
	needle, remainder, err := r.parseQuotedString(fullInput)
	if err != nil {
		fmt.Printf("Parse error for needle: %v\n", err)
		return
	}

	replacement, _, err := r.parseQuotedString(strings.TrimSpace(remainder))
	if err != nil {
		fmt.Printf("Parse error for replacement: %v\n", err)
		return
	}

	cursor := r.cursor()
	replaced, result, err := cursor.ReplaceString(needle, replacement, opts)
	if err != nil {
		fmt.Printf("Replace error: %v\n", err)
		return
	}

	if !replaced {
		fmt.Println("No match found")
		return
	}

	fmt.Printf("Replaced 1 occurrence. Fork=%d, revision=%d\n", result.Fork, result.Revision)
}

func (r *REPL) cmdReplaceAll(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: replaceall \"needle\" \"replacement\" [-i] [-w] [-b]")
		return
	}

	opts, remaining := parseSearchFlags(args)
	if len(remaining) < 2 {
		fmt.Println("Usage: replaceall \"needle\" \"replacement\" [flags]")
		return
	}

	fullInput := strings.Join(remaining, " ")
	needle, remainder, err := r.parseQuotedString(fullInput)
	if err != nil {
		fmt.Printf("Parse error for needle: %v\n", err)
		return
	}

	replacement, _, err := r.parseQuotedString(strings.TrimSpace(remainder))
	if err != nil {
		fmt.Printf("Parse error for replacement: %v\n", err)
		return
	}

	cursor := r.cursor()
	count, result, err := cursor.ReplaceStringAll(needle, replacement, opts)
	if err != nil {
		fmt.Printf("Replace error: %v\n", err)
		return
	}

	if count == 0 {
		fmt.Println("No matches found")
		return
	}

	fmt.Printf("Replaced %d occurrences. Fork=%d, revision=%d\n", count, result.Fork, result.Revision)
}

func (r *REPL) cmdReplaceCount(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 3 {
		fmt.Println("Usage: replacecount \"needle\" \"replacement\" <count> [-i] [-w] [-b]")
		return
	}

	opts, remaining := parseSearchFlags(args)
	if len(remaining) < 3 {
		fmt.Println("Usage: replacecount \"needle\" \"replacement\" <count> [flags]")
		return
	}

	fullInput := strings.Join(remaining, " ")
	needle, remainder, err := r.parseQuotedString(fullInput)
	if err != nil {
		fmt.Printf("Parse error for needle: %v\n", err)
		return
	}

	remainder = strings.TrimSpace(remainder)
	replacement, remainder, err := r.parseQuotedString(remainder)
	if err != nil {
		fmt.Printf("Parse error for replacement: %v\n", err)
		return
	}

	remainder = strings.TrimSpace(remainder)
	count, err := strconv.Atoi(strings.Fields(remainder)[0])
	if err != nil {
		fmt.Printf("Invalid count: %v\n", err)
		return
	}

	cursor := r.cursor()
	replaced, result, err := cursor.ReplaceStringCount(needle, replacement, count, opts)
	if err != nil {
		fmt.Printf("Replace error: %v\n", err)
		return
	}

	if replaced == 0 {
		fmt.Println("No matches found")
		return
	}

	fmt.Printf("Replaced %d occurrences. Fork=%d, revision=%d\n", replaced, result.Fork, result.Revision)
}

func (r *REPL) cmdReplaceRegex(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: replaceregex \"pattern\" \"replacement\" [-i] [-b]")
		fmt.Println("  Replacement can use $1, $2, etc. for capture groups")
		return
	}

	opts, remaining := parseRegexFlags(args)
	if len(remaining) < 2 {
		fmt.Println("Usage: replaceregex \"pattern\" \"replacement\" [flags]")
		return
	}

	fullInput := strings.Join(remaining, " ")
	pattern, remainder, err := r.parseQuotedString(fullInput)
	if err != nil {
		fmt.Printf("Parse error for pattern: %v\n", err)
		return
	}

	replacement, _, err := r.parseQuotedString(strings.TrimSpace(remainder))
	if err != nil {
		fmt.Printf("Parse error for replacement: %v\n", err)
		return
	}

	cursor := r.cursor()
	replaced, result, err := cursor.ReplaceRegex(pattern, replacement, opts)
	if err != nil {
		fmt.Printf("Replace error: %v\n", err)
		return
	}

	if !replaced {
		fmt.Println("No match found")
		return
	}

	fmt.Printf("Replaced 1 occurrence. Fork=%d, revision=%d\n", result.Fork, result.Revision)
}

func (r *REPL) cmdReplaceRegexAll(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: replaceregexall \"pattern\" \"replacement\" [-i] [-b]")
		return
	}

	opts, remaining := parseRegexFlags(args)
	if len(remaining) < 2 {
		fmt.Println("Usage: replaceregexall \"pattern\" \"replacement\" [flags]")
		return
	}

	fullInput := strings.Join(remaining, " ")
	pattern, remainder, err := r.parseQuotedString(fullInput)
	if err != nil {
		fmt.Printf("Parse error for pattern: %v\n", err)
		return
	}

	replacement, _, err := r.parseQuotedString(strings.TrimSpace(remainder))
	if err != nil {
		fmt.Printf("Parse error for replacement: %v\n", err)
		return
	}

	cursor := r.cursor()
	count, result, err := cursor.ReplaceRegexAll(pattern, replacement, opts)
	if err != nil {
		fmt.Printf("Replace error: %v\n", err)
		return
	}

	if count == 0 {
		fmt.Println("No matches found")
		return
	}

	fmt.Printf("Replaced %d occurrences. Fork=%d, revision=%d\n", count, result.Fork, result.Revision)
}

func (r *REPL) cmdReplaceRegexCount(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 3 {
		fmt.Println("Usage: replaceregexcount \"pattern\" \"replacement\" <count> [-i] [-b]")
		return
	}

	opts, remaining := parseRegexFlags(args)
	if len(remaining) < 3 {
		fmt.Println("Usage: replaceregexcount \"pattern\" \"replacement\" <count> [flags]")
		return
	}

	fullInput := strings.Join(remaining, " ")
	pattern, remainder, err := r.parseQuotedString(fullInput)
	if err != nil {
		fmt.Printf("Parse error for pattern: %v\n", err)
		return
	}

	remainder = strings.TrimSpace(remainder)
	replacement, remainder, err := r.parseQuotedString(remainder)
	if err != nil {
		fmt.Printf("Parse error for replacement: %v\n", err)
		return
	}

	remainder = strings.TrimSpace(remainder)
	count, err := strconv.Atoi(strings.Fields(remainder)[0])
	if err != nil {
		fmt.Printf("Invalid count: %v\n", err)
		return
	}

	cursor := r.cursor()
	replaced, result, err := cursor.ReplaceRegexCount(pattern, replacement, count, opts)
	if err != nil {
		fmt.Printf("Replace error: %v\n", err)
		return
	}

	if replaced == 0 {
		fmt.Println("No matches found")
		return
	}

	fmt.Printf("Replaced %d occurrences. Fork=%d, revision=%d\n", replaced, result.Fork, result.Revision)
}

func (r *REPL) cmdCount(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: count \"needle\" [-i] [-w]")
		return
	}

	opts, remaining := parseSearchFlags(args)
	if len(remaining) < 1 {
		fmt.Println("Usage: count \"needle\" [flags]")
		return
	}

	needle, _, err := r.parseQuotedString(strings.Join(remaining, " "))
	if err != nil {
		fmt.Printf("Parse error: %v\n", err)
		return
	}

	cursor := r.cursor()
	count, err := cursor.CountString(needle, opts)
	if err != nil {
		fmt.Printf("Count error: %v\n", err)
		return
	}

	fmt.Printf("Found %d occurrences\n", count)
}

func (r *REPL) cmdCountRegex(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 1 {
		fmt.Println("Usage: countregex \"pattern\" [-i]")
		return
	}

	caseInsensitive := false
	var remaining []string
	for _, arg := range args {
		if arg == "-i" {
			caseInsensitive = true
		} else {
			remaining = append(remaining, arg)
		}
	}

	if len(remaining) < 1 {
		fmt.Println("Usage: countregex \"pattern\" [-i]")
		return
	}

	pattern, _, err := r.parseQuotedString(strings.Join(remaining, " "))
	if err != nil {
		fmt.Printf("Parse error: %v\n", err)
		return
	}

	cursor := r.cursor()
	count, err := cursor.CountRegex(pattern, caseInsensitive)
	if err != nil {
		fmt.Printf("Count error: %v\n", err)
		return
	}

	fmt.Printf("Found %d matches\n", count)
}

func (r *REPL) cmdReady() {
	if !r.ensureGarland() {
		return
	}

	g := r.garland
	byteCount := g.ByteCount()
	runeCount := g.RuneCount()
	lineCount := g.LineCount()

	completeStr := "complete"
	if !byteCount.Complete {
		completeStr = "streaming"
	}

	fmt.Printf("Loading Status: %s\n", completeStr)
	fmt.Printf("  Bytes loaded: %d\n", byteCount.Value)
	fmt.Printf("  Runes loaded: %d\n", runeCount.Value)
	fmt.Printf("  Lines loaded: %d\n", lineCount.Value)

	if !byteCount.Complete {
		fmt.Println("\nDuring streaming, seek operations will block until data arrives.")
		fmt.Println("Use 'isready' to check if a position is available without blocking.")
	}
}

func (r *REPL) cmdIsReady(args []string) {
	if !r.ensureGarland() {
		return
	}

	if len(args) < 2 {
		fmt.Println("Usage: isready byte <pos>   - Check if byte position is ready")
		fmt.Println("       isready rune <pos>   - Check if rune position is ready")
		fmt.Println("       isready line <line>  - Check if line is ready")
		return
	}

	mode := strings.ToLower(args[0])
	pos, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		fmt.Printf("Invalid position: %v\n", err)
		return
	}

	g := r.garland
	var ready bool

	switch mode {
	case "byte":
		ready = g.IsByteReady(pos)
		if ready {
			fmt.Printf("Byte position %d is ready\n", pos)
		} else {
			fmt.Printf("Byte position %d is NOT ready (still streaming)\n", pos)
		}

	case "rune":
		ready = g.IsRuneReady(pos)
		if ready {
			fmt.Printf("Rune position %d is ready\n", pos)
		} else {
			fmt.Printf("Rune position %d is NOT ready (still streaming)\n", pos)
		}

	case "line":
		ready = g.IsLineReady(pos)
		if ready {
			fmt.Printf("Line %d is ready\n", pos)
		} else {
			fmt.Printf("Line %d is NOT ready (still streaming)\n", pos)
		}

	default:
		fmt.Println("Unknown mode. Use: byte, rune, or line")
	}
}

func (r *REPL) cmdMemory() {
	if !r.ensureGarland() {
		return
	}

	stats := r.garland.MemoryUsage()

	fmt.Println("Memory Usage Statistics:")
	fmt.Printf("  In-memory bytes:    %d\n", stats.MemoryBytes)
	fmt.Printf("  In-memory leaves:   %d\n", stats.InMemoryLeaves)
	fmt.Printf("  Cold storage leaves: %d\n", stats.ColdStoredLeaves)
	fmt.Printf("  Warm storage leaves: %d\n", stats.WarmStoredLeaves)

	if stats.SoftLimit > 0 {
		fmt.Printf("  Soft limit:         %d bytes\n", stats.SoftLimit)
		if stats.MemoryBytes > stats.SoftLimit {
			fmt.Println("  Status: OVER soft limit (background chilling active)")
		}
	} else {
		fmt.Println("  Soft limit:         (disabled)")
	}

	if stats.HardLimit > 0 {
		fmt.Printf("  Hard limit:         %d bytes\n", stats.HardLimit)
		if stats.MemoryBytes > stats.HardLimit {
			fmt.Println("  Status: OVER hard limit (immediate chilling triggered)")
		}
	} else {
		fmt.Println("  Hard limit:         (disabled)")
	}

	// Check tree balance
	if r.garland.NeedsRebalancing() {
		fmt.Println("  Tree status:        Needs rebalancing")
	} else {
		fmt.Println("  Tree status:        Balanced")
	}
}

func (r *REPL) cmdMemChill(args []string) {
	if !r.ensureGarland() {
		return
	}

	budget := 5 // default
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n <= 0 {
			fmt.Println("Usage: memchill [count]")
			fmt.Println("  count: number of nodes to chill (default: 5)")
			return
		}
		budget = n
	}

	stats := r.lib.IncrementalChill(budget)

	if stats.NodesChilled > 0 {
		fmt.Printf("Chilled %d nodes, freed %d bytes\n", stats.NodesChilled, stats.BytesChilled)
	} else {
		fmt.Println("No nodes chilled (none eligible or cold storage not configured)")
	}
}

func (r *REPL) cmdRebalance() {
	if !r.ensureGarland() {
		return
	}

	if !r.garland.NeedsRebalancing() {
		fmt.Println("Tree is already balanced, no rebalancing needed")
		return
	}

	stats := r.garland.ForceRebalance()

	if stats.RotationsPerformed == -1 {
		fmt.Println("Tree was rebuilt (full rebalance)")
	} else if stats.RotationsPerformed > 0 {
		fmt.Printf("Performed %d rotations\n", stats.RotationsPerformed)
	} else {
		fmt.Println("Rebalancing complete")
	}
}

// Ensure utf8 is used (for future unicode-aware operations)
var _ = utf8.RuneCountInString
