package amgp

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// The limits that keep an untrusted file from using more than a bounded
// amount of memory and time. A real file is a few tens of kilobytes and about
// ten levels deep.
const (
	// MaxBytes is the largest file Import reads.
	MaxBytes = 1 << 20
	// maxDepth is the deepest element nesting accepted. A mix slot is the
	// deepest construct of the format, at 11 levels.
	maxDepth = 32
	// maxElements is the largest number of elements accepted.
	maxElements = 100_000
	// maxQuote is the most characters of the file that a message about it
	// quotes: element and entity names are as long as the file is.
	maxQuote = 120
)

// node is an element of the file: its name, attributes, text and children.
// Namespaces are ignored: only local names are kept.
type node struct {
	name  string
	attrs map[string]string
	text  string
	kids  []*node
}

// child returns the first child called name, or nil.
func (n *node) child(name string) *node {
	for _, k := range n.kids {
		if k.name == name {
			return k
		}
	}
	return nil
}

// children returns the children called name, in file order.
func (n *node) children(name string) []*node {
	var out []*node
	for _, k := range n.kids {
		if k.name == name {
			out = append(out, k)
		}
	}
	return out
}

// str returns the trimmed text of the child called name, and whether there
// is such a child.
func (n *node) str(name string) (string, bool) {
	c := n.child(name)
	if c == nil {
		return "", false
	}
	return strings.TrimSpace(c.text), true
}

// num returns the integer in the child called name. A child whose text is
// not an integer counts as absent, so the default applies, instead of the 0
// that Qt's toInt would read.
func (n *node) num(name string) (int, bool) {
	s, ok := n.str(name)
	if !ok {
		return 0, false
	}
	return parseNumber(s)
}

// flag reports whether the child called name says true, as AntiMicroX writes
// a boolean: the literal text. Some of its readers take any integer, which
// "1" covers.
func (n *node) flag(name string) bool {
	s, _ := n.str(name)
	return s == "true" || s == "1"
}

// attr returns an attribute as an integer.
func (n *node) attr(name string) (int, bool) {
	s, ok := n.attrs[name]
	if !ok {
		return 0, false
	}
	return parseNumber(strings.TrimSpace(s))
}

// parseNumber reads an integer as Qt's toInt(0) does: 0x for hexadecimal, a
// leading 0 for octal, otherwise decimal. A value outside int32 is no number.
func parseNumber(s string) (int, bool) {
	if strings.Contains(s, "_") {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 0, 64)
	if err != nil || v < -1<<31 || v > 1<<31-1 {
		return 0, false
	}
	return int(v), true
}

// Reasons of an Error.
const (
	// ReasonSize: the file is larger than MaxBytes.
	ReasonSize = "size"
	// ReasonEncoding: the file is not UTF-8.
	ReasonEncoding = "encoding"
	// ReasonXML: the file is not well-formed XML.
	ReasonXML = "xml"
	// ReasonLimit: the file nests deeper or has more elements than allowed.
	ReasonLimit = "limit"
	// ReasonDoctype: the file has a DOCTYPE or another declaration.
	ReasonDoctype = "doctype"
	// ReasonRoot: the root element is not an AntiMicroX profile.
	ReasonRoot = "root"
	// ReasonJoystick: the profile is for a raw joystick, not a game
	// controller.
	ReasonJoystick = "joystick"
	// ReasonVersion: the profile was made by an old AntiMicro whose key
	// codes are native codes.
	ReasonVersion = "version"
	// ReasonInternal: the importer made a profile that does not validate.
	// It is a bug of the importer, never the file's fault.
	ReasonInternal = "internal"
)

// Error refuses a file that Import does not read. Message is for the user.
type Error struct {
	Reason  string
	Message string
}

func (e *Error) Error() string { return "amgp: " + e.Message }

func refuse(reason, format string, args ...any) *Error {
	return &Error{Reason: reason, Message: fmt.Sprintf(format, args...)}
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// parseTree reads the file into an element tree under the limits.
func parseTree(data []byte) (*node, error) {
	if len(data) > MaxBytes {
		return nil, refuse(ReasonSize, "文件超过 %d MiB", MaxBytes>>20)
	}
	data = bytes.TrimPrefix(data, utf8BOM)
	if bytes.HasPrefix(data, []byte{0xFF, 0xFE}) || bytes.HasPrefix(data, []byte{0xFE, 0xFF}) {
		return nil, refuse(ReasonEncoding, "文件不是 UTF-8（是 UTF-16）；请用 AntiMicroX 另存，或转成 UTF-8")
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	badCharset := false
	dec.CharsetReader = func(label string, input io.Reader) (io.Reader, error) {
		switch strings.ToLower(label) {
		case "utf-8", "utf8", "us-ascii", "ascii":
			return input, nil
		}
		badCharset = true
		return nil, fmt.Errorf("encoding %q is not UTF-8", label)
	}
	var (
		root, top *node
		stack     []*node
		count     int
		text      []*strings.Builder
	)
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if badCharset {
				return nil, refuse(ReasonEncoding, "文件声明的编码不是 UTF-8；请用 AntiMicroX 另存，或转成 UTF-8")
			}
			var syn *xml.SyntaxError
			if errors.As(err, &syn) {
				return nil, refuse(ReasonXML, "不是合法的 XML（第 %d 行）：%s", syn.Line, clipText(syn.Msg, maxQuote))
			}
			return nil, refuse(ReasonXML, "不是合法的 XML：%s", clipText(err.Error(), maxQuote))
		}
		switch t := tok.(type) {
		case xml.StartElement:
			count++
			if count > maxElements || len(stack) >= maxDepth {
				return nil, refuse(ReasonLimit, "文件的元素太多或嵌套太深（最多 %d 层、%d 个元素）", maxDepth, maxElements)
			}
			n := &node{name: t.Name.Local}
			if len(t.Attr) > 0 {
				n.attrs = make(map[string]string, len(t.Attr))
				for _, a := range t.Attr {
					n.attrs[a.Name.Local] = a.Value
				}
			}
			switch {
			case len(stack) > 0:
				top.kids = append(top.kids, n)
			case root == nil:
				root = n
			default:
				return nil, refuse(ReasonXML, "不是合法的 XML：有不止一个根元素")
			}
			stack = append(stack, n)
			text = append(text, new(strings.Builder))
			top = n
		case xml.EndElement:
			top.text = text[len(text)-1].String()
			stack = stack[:len(stack)-1]
			text = text[:len(text)-1]
			if len(stack) > 0 {
				top = stack[len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				text[len(text)-1].Write(t)
			}
		case xml.Directive:
			return nil, refuse(ReasonDoctype, "文件含有 DOCTYPE 或其他声明；AntiMicroX 不会写这些，出于安全不读取")
		}
	}
	if root == nil {
		return nil, refuse(ReasonXML, "文件是空的")
	}
	return root, nil
}
