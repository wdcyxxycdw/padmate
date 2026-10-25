package compile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildCache(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "lib.ts"), []byte("export const answer = 41;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.ts"), []byte("import { answer } from './lib'; export const result = answer + 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewCache()
	first, err := c.Build(BuildOptions{Root: root, Entry: "main.ts", API: "0.8"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Size == 0 {
		t.Fatal("first build is empty")
	}
	if got := c.Stats(); got.Hits != 0 || got.Misses != 1 {
		t.Fatalf("first stats = %+v, want one miss", got)
	}
	second, err := c.Build(BuildOptions{Root: root, Entry: "main.ts", API: "0.8"})
	if err != nil {
		t.Fatal(err)
	}
	if string(second.JS) != string(first.JS) {
		t.Fatal("cache changed bundle bytes")
	}
	if got := c.Stats(); got.Hits != 1 || got.Misses != 1 {
		t.Fatalf("second stats = %+v, want one hit and one miss", got)
	}
	if err := os.WriteFile(filepath.Join(root, "lib.ts"), []byte("export const answer = 42;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Build(BuildOptions{Root: root, Entry: "main.ts", API: "0.8"}); err != nil {
		t.Fatal(err)
	}
	if got := c.Stats(); got.Misses != 2 {
		t.Fatalf("after source change stats = %+v, want second miss", got)
	}
	if _, err := c.Build(BuildOptions{Root: root, Entry: "main.ts", API: "0.9"}); err != nil {
		t.Fatal(err)
	}
	if got := c.Stats(); got.Misses != 3 {
		t.Fatalf("after API change stats = %+v, want third miss", got)
	}
}
