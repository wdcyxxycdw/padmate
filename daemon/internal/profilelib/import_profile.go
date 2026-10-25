package profilelib

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// maxUniqueNames is how many numbered names ImportProfile tries when the name
// is taken and the caller wants a free one.
const maxUniqueNames = 200

// ImportProfile adds a profile.json that another format was converted to
// (PRO-21, the import of AntiMicroX files) to the library as a new profile.
// It uses the path of Import for an archive that holds nothing but
// profile.json: the folder is written and synced without the writer lock
// and renamed into place under it, the ID is made from the name (NewID), and
// the name must not be one another profile goes by (*NameTakenError).
//
// data must pass profile.Parse (an error otherwise, not an *ArchiveError:
// the caller made it). name, unless empty, replaces the profile's own name
// and passes CheckProfileName. With unique, a name that is taken is not an
// error: the first free "name 2", "name 3" and so on is used, the name cut so
// that the numbered one still fits MaxNameRunes.
func (l *Library) ImportProfile(data []byte, name string, unique bool) (Imported, error) {
	if len(data) > MaxDocumentBytes {
		return Imported{}, errors.New("profile library: document exceeds size limit")
	}
	p, ds := profile.Parse(data)
	if err := ds.Err(); err != nil {
		return Imported{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.TrimSpace(p.Name)
	}
	if err := CheckProfileName(name); err != nil {
		return Imported{}, err
	}
	archive := &Archive{
		Manifest: Manifest{Type: ArchiveType, Format: ArchiveFormat, Name: name},
		Profile:  p,
		items:    []copyItem{{path: "profile.json", data: data}},
	}
	try := name
	for n := 2; ; n++ {
		imported, err := l.Import(archive, ImportOptions{Name: try, Macros: MacrosAsk})
		var taken *NameTakenError
		if !unique || !errors.As(err, &taken) {
			return imported, err
		}
		if n > maxUniqueNames+1 {
			return Imported{}, err
		}
		try = numberedName(name, n)
	}
}

// numberedName returns name with " n" added, the name cut to leave room for it.
func numberedName(name string, n int) string {
	suffix := " " + strconv.Itoa(n)
	room := MaxNameRunes - utf8.RuneCountInString(suffix)
	if utf8.RuneCountInString(name) > room {
		name = strings.TrimSpace(string([]rune(name)[:room]))
	}
	return fmt.Sprintf("%s%s", name, suffix)
}
