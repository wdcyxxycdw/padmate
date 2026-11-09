package tablegen

import (
	"fmt"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/cspnames"
)

// override names and places one command that neither the executable nor the
// menu resource names. The plug-in ExportPSB.dll / ExportPSD.dll carry
// "psb|psb" and "psd|psd" (an export format, not a label), and the executable
// has no stub for their GUIDs, so the scan cannot see them; CSP's dialog shows
// them as the last formats of the two export groups.
//
// Where they go comes from the order of the "advancedcommand" rows in CSP's
// own workspace presets (Settings\PAINT\WorkSpace\*\*.spws, which list the
// commands of the dialog bottom-up): each plain GUID sits right before
// filesaveaswebp (so it follows it in the dialog), each GUID with the
// "-2FB51A87-..." suffix right before filesavemergeimagewebp. Within a group
// psb comes before psd. The labels are those of CSP's dialog (saved-copy list).
type override struct {
	id     string // lower-case command id
	zh, en string
	group  string // id of the group the command is appended to
}

const variantSuffix = "-2fb51a87-53f9-44d0-92a0-b0e0b2ae3ad7"

const (
	psbID = "28691cf0-c95b-4afc-a4c2-75e46d4f689d"
	psdID = "3d617f46-7d99-44b2-80c1-2bef6f9ebe33"
)

// overrides is applied in order: within a group the commands are appended in
// the order of this list.
var overrides = []override{
	{psbID, ".psb(Photoshop 大型文档)", ".psb (Photoshop Big Document)", "filesaveasdirectparent"},
	{psdID, ".psd(Photoshop 文档)", ".psd (Photoshop Document)", "filesaveasdirectparent"},
	{psbID + variantSuffix, ".psb(Photoshop 大型文档)", ".psb (Photoshop Big Document)", "filesavemergeimageparent"},
	{psdID + variantSuffix, ".psd(Photoshop 文档)", ".psd (Photoshop Document)", "filesavemergeimageparent"},
}

// applyOverrides adds the names and the tree placements of overrides to t. A
// command the table already names or places is left as it is, so that an
// executable that starts to know these commands wins. A missing group is an
// error: the tree no longer has the shape the list assumes.
func applyOverrides(t *cspnames.Table) error {
	placed := map[string]bool{}
	var scan func(nodes []cspnames.TableNode)
	scan = func(nodes []cspnames.TableNode) {
		for _, n := range nodes {
			placed[n.ID] = true
			scan(n.Children)
		}
	}
	scan(t.Tree)
	for _, o := range overrides {
		if strings.ToLower(o.id) != o.id {
			return fmt.Errorf("tablegen: override %q is not lower-case", o.id)
		}
		if _, named := t.Commands[o.id]; !named {
			t.Commands[o.id] = cspnames.TableCommand{ZH: o.zh, EN: o.en}
		}
		if placed[o.id] {
			continue
		}
		g := findGroup(t.Tree, o.group)
		if g == nil {
			return fmt.Errorf("tablegen: override %q: group %q not in the tree", o.id, o.group)
		}
		g.Children = append(g.Children, cspnames.TableNode{ID: o.id})
		placed[o.id] = true
	}
	return nil
}

// findGroup returns the first group with the id, in tree order.
func findGroup(nodes []cspnames.TableNode, id string) *cspnames.TableNode {
	for i := range nodes {
		if nodes[i].ID == id && nodes[i].IsGroup() {
			return &nodes[i]
		}
		if g := findGroup(nodes[i].Children, id); g != nil {
			return g
		}
	}
	return nil
}
