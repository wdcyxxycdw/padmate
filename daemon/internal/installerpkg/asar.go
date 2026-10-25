package installerpkg

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// maxAsarHeader bounds the header read from an archive that is not trusted
// to be an asar at all.
const maxAsarHeader = 64 << 20

// maxAsarFile bounds the file ReadAsarFile returns.
const maxAsarFile = 16 << 20

type asarNode struct {
	Files  map[string]asarNode `json:"files"`
	Size   int64               `json:"size"`
	Offset string              `json:"offset"`
	Link   string              `json:"link"`
	// Unpacked files live next to the archive, not in it.
	Unpacked bool `json:"unpacked"`
}

// ReadAsarFile returns the file at a slash-separated path of an Electron asar
// archive. An asar is two Pickles and the files: a 4-byte size (4), the
// 4-byte size of the header Pickle, the header Pickle (a 4-byte payload
// size, a 4-byte string length, the JSON index) and the file contents after
// it; each file's offset in the index is relative to the end of the header.
func ReadAsarFile(archive io.ReaderAt, name string) ([]byte, error) {
	var prefix [16]byte
	if _, err := archive.ReadAt(prefix[:], 0); err != nil {
		return nil, fmt.Errorf("asar header: %w", err)
	}
	if binary.LittleEndian.Uint32(prefix[0:4]) != 4 {
		return nil, errors.New("asar: bad size pickle")
	}
	pickle := binary.LittleEndian.Uint32(prefix[4:8])
	length := binary.LittleEndian.Uint32(prefix[12:16])
	if pickle < 8 || pickle > maxAsarHeader || length > pickle-8 {
		return nil, errors.New("asar: bad header size")
	}
	index := make([]byte, length)
	if _, err := archive.ReadAt(index, 16); err != nil {
		return nil, fmt.Errorf("asar index: %w", err)
	}
	var root asarNode
	if err := json.Unmarshal(index, &root); err != nil {
		return nil, fmt.Errorf("asar index: %w", err)
	}
	node := root
	for part := range strings.SplitSeq(name, "/") {
		next, ok := node.Files[part]
		if !ok {
			return nil, fmt.Errorf("asar: %s not found", name)
		}
		node = next
	}
	if node.Files != nil || node.Link != "" || node.Unpacked {
		return nil, fmt.Errorf("asar: %s is not a packed file", name)
	}
	offset, err := strconv.ParseInt(node.Offset, 10, 64)
	if err != nil || offset < 0 || node.Size < 0 || node.Size > maxAsarFile {
		return nil, fmt.Errorf("asar: bad entry for %s", name)
	}
	data := make([]byte, node.Size)
	if _, err := archive.ReadAt(data, 8+int64(pickle)+offset); err != nil {
		return nil, fmt.Errorf("asar: reading %s: %w", name, err)
	}
	return data, nil
}
