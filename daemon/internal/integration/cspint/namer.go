package cspint

import (
	"strings"
	"sync"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/cspnames"
	"github.com/wdcyxxycdw/padmate/daemon/internal/integration"
)

// namesRetryEvery is how often a namer that could not read CSP's
// installation looks again, so that a CSP installed later is picked up.
const namesRetryEvery = 30 * time.Second

// NamesNamer adapts cspnames (CSP's menu resource, the bundled table and
// dialog tree, the plug-in DLLs) to integration.Namer. It keeps one
// *cspnames.Names per language.
//
// Reading CSP's installation happens on first use of a language. Without an
// installation the namer still has the bundled table and the dialog's tree;
// only the menu names and the plug-ins are missing. A failed lookup is not
// kept for good: it is tried again after namesRetryEvery.
type NamesNamer struct {
	// InstallDir returns CSP's installation folder; it is called once per
	// language and retry, and a failure means no installation.
	InstallDir func() (string, error)
	// Now is the clock of the retries; nil means time.Now.
	Now func() time.Time

	mu    sync.Mutex
	names map[string]*loaded // by cspnames.Language
}

// loaded is the names of one language and how complete they are.
type loaded struct {
	names   *cspnames.Names
	problem string // integration.Names* or ""
	retryAt time.Time
	// loadErr is the error of cspnames.Load, for logs.
	loadErr error
}

var (
	_ integration.Namer         = (*NamesNamer)(nil)
	_ integration.NamesReporter = (*NamesNamer)(nil)
)

func (n *NamesNamer) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

func (n *NamesNamer) forLang(lang string) *loaded {
	key := cspnames.Language(lang)
	n.mu.Lock()
	defer n.mu.Unlock()
	if l, ok := n.names[key]; ok && (l.problem == "" || n.now().Before(l.retryAt)) {
		return l
	}
	l := &loaded{}
	dir := ""
	if n.InstallDir != nil {
		dir, _ = n.InstallDir()
	}
	// Load returns usable names together with some errors, and never nil.
	l.names, l.loadErr = cspnames.Load(dir, lang)
	switch {
	case dir == "":
		l.problem = integration.NamesInstallNotFound
	case l.names.MenuCommands() == 0:
		l.problem = integration.NamesResourceUnreadable
	}
	if l.problem != "" {
		l.retryAt = n.now().Add(namesRetryEvery)
	}
	if n.names == nil {
		n.names = map[string]*loaded{}
	}
	n.names[key] = l
	return l
}

// NamesProblem says why menu names are missing in lang, or "".
func (n *NamesNamer) NamesProblem(lang string) string { return n.forLang(lang).problem }

// CommandName names the command id ("<menucommandtype>/<menucommand>").
func (n *NamesNamer) CommandName(id, lang string) (integration.CommandInfo, bool) {
	names := n.forLang(lang).names
	typ, cmd, ok := strings.Cut(id, "/")
	if !ok {
		return integration.CommandInfo{}, false
	}
	name, path, group, ok := names.Command(typ, cmd)
	if !ok {
		return integration.CommandInfo{}, false
	}
	info := integration.CommandInfo{Name: name, Path: path, Group: group}
	if group != "" && len(path) > 1 {
		info.Sub = path[len(path)-1]
	}
	return info, true
}

// Layout is the tree of CSP's shortcut dialog.
func (n *NamesNamer) Layout(lang string) []integration.LayoutNode {
	return layoutNodes(n.forLang(lang).names.Layout())
}

func layoutNodes(in []cspnames.Node) []integration.LayoutNode {
	out := make([]integration.LayoutNode, 0, len(in))
	for _, x := range in {
		out = append(out, integration.LayoutNode{ID: x.ID, Name: x.Name, Children: layoutNodes(x.Children)})
	}
	return out
}
