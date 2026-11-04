package cspint

import (
	"strings"
	"sync"

	"github.com/wdcyxxycdw/padmate/daemon/internal/cspnames"
	"github.com/wdcyxxycdw/padmate/daemon/internal/integration"
)

// NamesNamer adapts cspnames (CSP's menu resource, the bundled table, the
// plug-in DLLs) to integration.Namer. It keeps one *cspnames.Names per
// language. Reading CSP's installation happens on first use of a language;
// when it fails the namer names what it can (the bundled table) or falls back
// to the command's ID.
type NamesNamer struct {
	// InstallDir returns CSP's installation folder; it is called once per
	// language, and a failure means no installation.
	InstallDir func() (string, error)

	mu    sync.Mutex
	names map[string]*cspnames.Names // by cspnames.Language
}

var _ integration.Namer = (*NamesNamer)(nil)

func (n *NamesNamer) forLang(lang string) *cspnames.Names {
	key := cspnames.Language(lang)
	n.mu.Lock()
	defer n.mu.Unlock()
	if names, ok := n.names[key]; ok {
		return names
	}
	var names *cspnames.Names
	dir := ""
	if n.InstallDir != nil {
		dir, _ = n.InstallDir()
	}
	if dir != "" {
		// Load returns usable names together with some errors.
		names, _ = cspnames.Load(dir, lang)
	}
	if n.names == nil {
		n.names = map[string]*cspnames.Names{}
	}
	n.names[key] = names
	return names
}

// CommandName names the command id ("<menucommandtype>/<menucommand>").
func (n *NamesNamer) CommandName(id, lang string) (string, []string, string, bool) {
	names := n.forLang(lang)
	if names == nil {
		return "", nil, "", false
	}
	typ, cmd, ok := strings.Cut(id, "/")
	if !ok {
		return "", nil, "", false
	}
	return names.Command(typ, cmd)
}

// Categories lists the categories cspnames knows, in display order; the
// integration drops the ones no function uses and adds "tools" and "other".
func (n *NamesNamer) Categories(lang string) []integration.Category {
	names := n.forLang(lang)
	if names == nil {
		return nil
	}
	var out []integration.Category
	for _, c := range names.Categories() {
		out = append(out, integration.Category{ID: c.ID, Name: c.Name})
	}
	return out
}
