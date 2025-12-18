# Fork and Revision Numbering

Garland uses a copy-on-write versioning system with **forks** and **revisions**.

## Basic Concepts

- **Revision**: A snapshot of the document at a specific point in time
- **Fork**: An independent timeline of revisions that branches from another fork
- **Fork 0**: The primary/original timeline, always exists

## Revision Flow (Single Fork)

```
Initial state (rev 0)        Edit 1 (rev 1)        Edit 2 (rev 2)
     "BASE"          -->       "ABASE"      -->      "ABBASE"
        |                         |                     |
     rev 0                     rev 1                  rev 2
                            Fork 0 Timeline
```

When editing in a single fork, each edit increments the revision number.

## Fork Creation (Branching)

When you `UndoSeek` to a previous revision and make an edit, a new fork is created:

```
Fork 0 Timeline:
    rev 0      rev 1      rev 2      rev 3      rev 4
   "BASE" --> "ABASE" --> "ABBASE" --> "ABCBASE" --> "ABCDBASE"
                  |
                  |  UndoSeek(1), then edit
                  v
Fork 1 Timeline:
    rev 0      rev 1      rev 2      rev 3      rev 4      rev 5
   "BASE" --> "ABASE" --> "AXBASE" --> "ABXYBASE" --> "ABXYZBASE"
       |          |           ^
       |          |           |
       +----------+-----------+--- inherited from Fork 0 (revisions 0-1)
                              |
                              +--- Fork 1's first edit (rev 2, since it diverges from rev 1)
```

**Key insight**: Fork 1 inherits revisions 0-1 from Fork 0. When Fork 1 makes its
first edit from revision 1, the new revision is numbered 2 (continuing the sequence).

## Revision Inheritance

```
                Fork 0                      Fork 1
           +--------------+           +--------------+
 rev 0     |    "BASE"    |   <---->  |    "BASE"    |  (inherited)
           +--------------+           +--------------+
 rev 1     |   "ABASE"    |   <---->  |   "ABASE"    |  (inherited, divergence point)
           +--------------+           +--------------+
 rev 2     |  "ABBASE"    |           |  "AXBASE"    |  (different content)
           +--------------+           +--------------+
 rev 3     |  "ABCBASE"   |           | "AXYZBASE"   |  (different content)
           +--------------+           +--------------+
 rev 4     | "ABCDBASE"   |           |     ...      |
           +--------------+           +--------------+
```

In Fork 1:
- `UndoSeek(0)` shows "BASE" (inherited from Fork 0)
- `UndoSeek(1)` shows "ABASE" (inherited from Fork 0, divergence point)
- `UndoSeek(2)` shows "AXBASE" (Fork 1's first edit)
- `UndoSeek(3)` shows "AXYZBASE" (Fork 1's second edit)

## Nested Forks

```
Fork 0:  rev 0 --> rev 1 --> rev 2 --> rev 3 --> rev 4
                      |
                      +---> Fork 1:  rev 2 --> rev 3 --> rev 4 --> rev 5
                                        |
                                        +---> Fork 2:  rev 3 --> rev 4
```

Fork 2 inherits:
- rev 0-1 from Fork 0 (via Fork 1's inheritance)
- rev 2 from Fork 1 (its divergence point)
- Its own edits start at rev 3

## ForkInfo Structure

```
ForkInfo {
    ID:              ForkID      // This fork's unique identifier
    ParentFork:      ForkID      // Fork this branched from
    ParentRevision:  RevisionID  // Revision in parent fork where this fork diverged
    HighestRevision: RevisionID  // Highest revision number in this fork
}
```

## Navigation Commands

| Command | Description |
|---------|-------------|
| `UndoSeek(rev)` | Navigate to revision within current fork (including inherited revisions) |
| `ForkSeek(fork)` | Switch to a different fork (goes to common ancestor revision) |
| `CurrentFork()` | Get current fork ID |
| `CurrentRevision()` | Get current revision number |
| `ListForks()` | List all forks with their info |

## Example Session

```
// Start: Fork 0, rev 0, content = "BASE"
Insert("A")        // Fork 0, rev 1, content = "ABASE"
Insert("B")        // Fork 0, rev 2, content = "ABBASE"
Insert("C")        // Fork 0, rev 3, content = "ABCBASE"
UndoSeek(1)        // Fork 0, rev 1, content = "ABASE"
Insert("X")        // Fork 1, rev 2, content = "AXBASE" (new fork!)
Insert("Y")        // Fork 1, rev 3, content = "AXYBASE"
UndoSeek(0)        // Fork 1, rev 0, content = "BASE" (inherited)
UndoSeek(1)        // Fork 1, rev 1, content = "ABASE" (inherited)
UndoSeek(3)        // Fork 1, rev 3, content = "AXYBASE" (this fork's edit)
ForkSeek(0)        // Fork 0, rev 1, content = "ABASE" (common ancestor)
UndoSeek(3)        // Fork 0, rev 3, content = "ABCBASE" (Fork 0's version)
```
