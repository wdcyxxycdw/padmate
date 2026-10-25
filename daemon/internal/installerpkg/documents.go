package installerpkg

import (
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// UserDocuments are the documents an installation has besides the user
// guide (ENG-17): the pages that the README and the guide send the reader to,
// in both languages. The rest of docs/ is about how padmate is made and stays
// in the development repository, like the READMEs of the examples, which are
// notes for developing them (docs/build-installer.md, "安装包带哪些文档").
var UserDocuments = []string{
	"docs/quickstart.md", "docs/quickstart.en.md",
	"docs/known-limitations.md", "docs/known-limitations.en.md",
	"docs/troubleshooting-input.md", "docs/troubleshooting-input.en.md",
}

// UserGuide is the folder of the user guide, which an installation has whole.
const UserGuide = "docs/guide/"

// userGuideIndex is the first page of the guide; an installation without it
// has no guide.
const userGuideIndex = UserGuide + "00-index.md"

// shipsFile reports whether an installation has the file of the portable
// preview that is called name: a slash path from the repository root, as
// portablezip.Collect names them.
func shipsFile(name string) bool {
	switch {
	case name == "README-PORTABLE.md":
		// About unzipping the preview; the installer has no such step.
		return false
	case strings.HasPrefix(name, "docs/"):
		return isUserDocument(name)
	case strings.HasPrefix(name, "examples/") && path.Base(name) == "README.md":
		return false
	}
	return true
}

// isUserDocument reports whether the file of docs/ called name is one that an
// installation has.
func isUserDocument(name string) bool {
	return strings.HasPrefix(name, UserGuide) || slices.Contains(UserDocuments, name)
}

// missingUserDocuments returns the user documents that are not among staged,
// the names of the files that were staged (slash paths from the repository
// root).
func missingUserDocuments(staged map[string]bool) []string {
	var missing []string
	for _, name := range append([]string{userGuideIndex}, UserDocuments...) {
		if !staged[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

// documentProblems checks the documents of an installation, or of the tree it
// is installed from, whose root is root: it has the user guide and the user
// documents, and no other file of docs/ and no README of an example.
func documentProblems(root string) []string {
	var problems []string
	resources := filepath.Join(root, filepath.FromSlash(ResourcesFolder))
	have := make(map[string]bool)
	for _, tree := range []string{"docs", "examples"} {
		base := filepath.Join(resources, tree)
		err := filepath.WalkDir(base, func(file string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(resources, file)
			if err != nil {
				return err
			}
			name := filepath.ToSlash(rel)
			have[name] = true
			if !shipsFile(name) {
				problems = append(problems, fmt.Sprintf("%s/%s: must not be installed: only the user documents are", ResourcesFolder, name))
			}
			return nil
		})
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s/%s: %v", ResourcesFolder, tree, err))
		}
	}
	for _, name := range missingUserDocuments(have) {
		problems = append(problems, fmt.Sprintf("%s/%s: missing", ResourcesFolder, name))
	}
	return problems
}
