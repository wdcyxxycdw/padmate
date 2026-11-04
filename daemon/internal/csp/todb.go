package csp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// uuidLen is the length of a node UUID blob.
const uuidLen = 16

// ToolNode is one node of the tool tree.
type ToolNode struct {
	// UUID is the stable id: 32 lower-case hex characters.
	UUID string
	Name string
	// Level is 1 for a tool, 2 for a sub-tool group, 3 for a sub-tool. Deeper
	// nodes, if a CSP version ever has them, keep counting.
	Level int
	// ParentUUID is the UUID of the parent, empty for a level-1 node (its
	// parent is the unlisted root).
	ParentUUID string
	// Path is the names from the level-1 ancestor down to this node.
	Path []string
	// KeyCode is the raw NodeShortCutKey: 0 none, 1-26 A-Z, others unknown.
	KeyCode int
	// Index is the position in tree order (depth-first, parents first).
	Index int
}

// ToolTree is the tool tree of an EditImageTool.todb.
type ToolTree struct {
	// Nodes are all nodes below the root in tree order.
	Nodes []*ToolNode

	byUUID map[string]*ToolNode
}

// Node returns the node with the given UUID (lower-case hex), or nil.
func (t *ToolTree) Node(uuid string) *ToolNode { return t.byUUID[uuid] }

var todbNodeColumns = []string{"NodeUuid", "NodeName", "NodeFirstChildUuid", "NodeNextUuid", "NodeShortCutKey"}

// validateTODB checks the tables and columns this package needs and returns
// the name of the Manager column that holds the root node UUID.
func validateTODB(ctx context.Context, q querier) (rootCol string, err error) {
	if err := requireColumns(ctx, q, "Node", todbNodeColumns...); err != nil {
		return "", err
	}
	cols, err := tableColumns(ctx, q, "Manager")
	if err != nil {
		return "", err
	}
	if cols == nil {
		return "", fmt.Errorf("%w: missing table Manager", ErrUnknownFormat)
	}
	var cand []string
	for _, c := range cols {
		if strings.Contains(c, "root") {
			cand = append(cand, c)
		}
	}
	if len(cand) == 0 {
		return "", fmt.Errorf("%w: table Manager has no root column", ErrUnknownFormat)
	}
	rootCol = cand[0]
	for _, c := range cand {
		if strings.Contains(c, "uuid") {
			rootCol = c
			break
		}
	}
	return rootCol, nil
}

// ReadTODB reads the tool tree from the EditImageTool.todb at path. The file is
// opened read-only and immutable. A missing table or column, a bad link, a
// cycle or a duplicate UUID is reported as ErrUnknownFormat.
func ReadTODB(path string) (*ToolTree, error) {
	db, err := openRO(path)
	if err != nil {
		return nil, fmt.Errorf("csp: open todb: %w", err)
	}
	defer func() { _ = db.Close() }()
	return readTODB(context.Background(), db)
}

type rawNode struct {
	uuid, first, next []byte
	name              string
	key               int
}

func readTODB(ctx context.Context, db *sql.DB) (*ToolTree, error) {
	rootCol, err := validateTODB(ctx, db)
	if err != nil {
		return nil, err
	}
	var root []byte
	if err := db.QueryRowContext(ctx, `SELECT "`+rootCol+`" FROM Manager LIMIT 1`).Scan(&root); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: table Manager has no row", ErrUnknownFormat)
		}
		return nil, fmt.Errorf("%w: read root uuid: %w", ErrUnknownFormat, err)
	}
	if len(root) != uuidLen {
		return nil, fmt.Errorf("%w: root uuid is not %d bytes", ErrUnknownFormat, uuidLen)
	}

	rows, err := db.QueryContext(ctx,
		"SELECT NodeUuid, NodeName, NodeFirstChildUuid, NodeNextUuid, NodeShortCutKey FROM Node")
	if err != nil {
		return nil, fmt.Errorf("csp: read Node: %w", err)
	}
	defer func() { _ = rows.Close() }()
	raw := map[string]*rawNode{}
	for rows.Next() {
		var (
			n    rawNode
			name sql.NullString
			key  sql.NullInt64
		)
		if err := rows.Scan(&n.uuid, &name, &n.first, &n.next, &key); err != nil {
			return nil, fmt.Errorf("%w: read Node row: %w", ErrUnknownFormat, err)
		}
		if len(n.uuid) != uuidLen {
			return nil, fmt.Errorf("%w: node uuid is not %d bytes", ErrUnknownFormat, uuidLen)
		}
		n.name, n.key = name.String, int(key.Int64)
		id := hex.EncodeToString(n.uuid)
		if raw[id] != nil {
			return nil, fmt.Errorf("%w: duplicate node uuid %s", ErrUnknownFormat, id)
		}
		raw[id] = &n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("csp: read Node: %w", err)
	}

	rootRaw := raw[hex.EncodeToString(root)]
	if rootRaw == nil {
		return nil, fmt.Errorf("%w: root node not found", ErrUnknownFormat)
	}
	t := &ToolTree{byUUID: map[string]*ToolNode{}}
	if err := t.walk(raw, rootRaw.first, "", nil, 1); err != nil {
		return nil, err
	}
	return t, nil
}

// isNoLink reports whether a link blob means "none": NULL, empty or all zero.
func isNoLink(b []byte) bool {
	return len(b) == 0 || bytes.Equal(b, make([]byte, len(b)))
}

// walk appends the sibling chain starting at link, each followed by its
// children, in tree order. Every node may be visited once, which bounds the
// walk and rejects cycles.
func (t *ToolTree) walk(raw map[string]*rawNode, link []byte, parent string, path []string, level int) error {
	for !isNoLink(link) {
		id := hex.EncodeToString(link)
		n := raw[id]
		if n == nil {
			return fmt.Errorf("%w: link to missing node %s", ErrUnknownFormat, id)
		}
		if t.byUUID[id] != nil {
			return fmt.Errorf("%w: node %s is reached twice (cycle or shared link)", ErrUnknownFormat, id)
		}
		node := &ToolNode{
			UUID: id, Name: n.name, Level: level, ParentUUID: parent,
			Path:    append(append([]string(nil), path...), n.name),
			KeyCode: n.key, Index: len(t.Nodes),
		}
		t.Nodes = append(t.Nodes, node)
		t.byUUID[id] = node
		if err := t.walk(raw, n.first, id, node.Path, level+1); err != nil {
			return err
		}
		link = n.next
	}
	return nil
}
