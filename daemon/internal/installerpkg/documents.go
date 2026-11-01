package installerpkg

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// UserDocuments are the documents an installation has besides the user
// guide: the README, in English and in Chinese. The rest of docs/
// (the architecture, the pitfalls, the decision records) is about how padmate
// is made, and the READMEs of the examples are notes for developing them.
var UserDocuments = []string{"README.md", "README.zh-CN.md"}

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
	case strings.HasPrefix(name, "docs/"):
		return strings.HasPrefix(name, UserGuide)
	case strings.HasPrefix(name, "examples/") && path.Base(name) == "README.md":
		return false
	}
	return true
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
	for _, name := range UserDocuments {
		if info, err := os.Stat(filepath.Join(resources, filepath.FromSlash(name))); err == nil && info.Mode().IsRegular() {
			have[name] = true
		}
	}
	for _, name := range missingUserDocuments(have) {
		problems = append(problems, fmt.Sprintf("%s/%s: missing", ResourcesFolder, name))
	}
	return problems
}
