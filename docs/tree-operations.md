# Tree Operations: Split and Concatenate

Garland uses a rope data structure (balanced binary tree of text chunks) with two
fundamental operations: **split** and **concatenate**.

## Tree Structure Overview

```
                    [Internal Node]
                    byteCount: 11
                   /              \
           [Leaf "Hello"]    [Leaf " World"]
           byteCount: 5       byteCount: 6
```

Each node can be:
- **Leaf node**: Contains actual text data and decorations
- **Internal node**: Contains references to two children and aggregate counts

## Split Operation

Splits a leaf node at a byte position, creating two new leaf nodes.

### Before Split

```
                [Leaf "Hello World"]
                byteCount: 11
                decorations: [mark_H@0, mark_W@6]
```

### Split at position 6

```
         [Internal Node]
         byteCount: 11
        /              \
  [Leaf "Hello "]    [Leaf "World"]
  byteCount: 6       byteCount: 5
  decs: [mark_H@0]   decs: [mark_W@0]  <- position adjusted
```

### Split Implementation

```go
func splitLeaf(node *Node, snap *NodeSnapshot, bytePos int64) (NodeID, NodeID, error)
```

1. Ensures split doesn't occur mid-UTF-8 character (aligns to rune boundary)
2. Partitions data: `leftData = data[:pos]`, `rightData = data[pos:]`
3. Partitions decorations (adjusts positions in right node)
4. Creates two new leaf nodes (copy-on-write)
5. Returns IDs of (left, right) leaf nodes

### Used By

- `insertIntoLeaf`: Splits leaf to insert new data in the middle
- `deleteRange`: Splits at deletion boundaries

## Concatenate Operation

Creates an internal node joining two subtrees.

### Concatenate Example

```
    Input:                     Output:

    [Leaf "Hello"]      +      [Leaf " World"]

                               [Internal Node]
                               byteCount: 11
                              /              \
                       [Leaf "Hello"]    [Leaf " World"]
```

### Structural Sharing

Concatenate reuses existing internal nodes when possible:

```
    First concatenate(A, B):          Second concatenate(A, B):

    Creates new internal node         Reuses existing node!
           [I1]                              [I1]
          /    \                            /    \
        [A]    [B]                        [A]    [B]

    g.internalNodesByChildren[[A,B]] = I1
```

### Concatenate Implementation

```go
func concatenate(leftID, rightID NodeID) (NodeID, error)
```

1. Gets snapshots for both children
2. Creates internal snapshot with aggregate counts
3. Checks `internalNodesByChildren` cache for existing structure
4. If found, adds new snapshot to existing node
5. If not found, creates new internal node
6. Returns ID of internal node

### Used By

- `insertInternal`: Rebuilds internal nodes after insertion
- `insertIntoLeaf`: Joins split parts with new data
- `deleteRange`: Joins remaining parts after deletion
- `rotateLeft` / `rotateRight`: Balancing operations

## Insert Operation Flow

Inserting "XX" at position 6 in "Hello World":

```
Step 1: Navigate to leaf        Step 2: Split at position 6

    [Internal]                      [Internal]
       |                           /          \
   [Leaf "Hello World"]    [Leaf "Hello "]  [Leaf "World"]


Step 3: Create new leaf         Step 4: Concatenate all parts

     [Leaf "XX"]                         [Internal]
                                        /          \
                               [Internal]        [Leaf "World"]
                              /          \
                     [Leaf "Hello "]  [Leaf "XX"]

Final Result: "Hello XX World"
```

### Insert Implementation

```go
func insertIntoLeaf(snap *NodeSnapshot, localPos int64, data []byte, ...) (NodeID, error)
```

1. If inserting at start: `concatenate(newLeaf, existingLeaf)`
2. If inserting at end: `concatenate(existingLeaf, newLeaf)`
3. If inserting in middle:
   - Split leaf at position
   - Concatenate: `(left, newLeaf)`, then `(leftMiddle, right)`

## Delete Operation Flow

Deleting "lo Wo" from "Hello World":

```
Step 1: Navigate to range       Step 2: Split at boundaries

    [Leaf "Hello World"]            [Internal]
                                   /     |      \
                             [Leaf  [Leaf    [Leaf
                             "Hel"]  "lo Wo"] "rld"]

Step 3: Remove middle           Step 4: Concatenate remaining

                                     [Internal]
                                    /          \
    Remove "lo Wo"            [Leaf "Hel"]  [Leaf "rld"]

Final Result: "Helrld"
```

### Delete Implementation

```go
func deleteRange(startPos, length int64) ([]Decoration, NodeID, error)
```

1. Navigate tree to find range boundaries
2. Split leaf nodes at boundaries
3. Collect decorations from deleted range
4. Concatenate remaining parts
5. Return collected decorations and new root ID

## Tree Balancing

After insertions, the tree may become unbalanced. Garland uses rotations:

### Right Rotation (Left-heavy tree)

```
Before:                     After:
        [C]                     [B]
       /                       /   \
     [B]                     [A]   [C]
    /
  [A]
```

### Left Rotation (Right-heavy tree)

```
Before:                     After:
  [A]                           [B]
    \                          /   \
    [B]                      [A]   [C]
      \
      [C]
```

### Rotation Uses Concatenate

```go
func rotateLeft(node *Node, snap *NodeSnapshot) (NodeID, error) {
    rightSnap := getRightChild(snap)
    // Right's left becomes node's right
    // Right becomes new parent
    newLeftID, _ := g.concatenate(snap.leftID, rightSnap.leftID)
    newRootID, _ := g.concatenate(newLeftID, rightSnap.rightID)
    return newRootID, nil
}
```

## Operation Summary

| Operation | Uses Split | Uses Concatenate |
|-----------|------------|------------------|
| Insert at start | No | Yes (1x) |
| Insert at end | No | Yes (1x) |
| Insert in middle | Yes (1x) | Yes (2x) |
| Delete | Yes (0-2x) | Yes (1x) |
| Rotate left | No | Yes (2x) |
| Rotate right | No | Yes (2x) |

## Copy-on-Write Semantics

All operations preserve existing node data:

```
Before edit (rev 1):              After edit (rev 2):

    [Internal]                        [Internal']  <- new snapshot
       |                             /            \
   [Leaf "AB"]                [Leaf "AB"]    [Leaf "X"]
                              (unchanged)     (new node)
```

- Old version remains accessible for undo/history
- Structural sharing minimizes memory usage
- Each node tracks snapshots by `{Fork, Revision}`
