package main

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeDLL is the content of SDL3.dll in the synthetic release.
var fakeDLL = []byte("MZ fake SDL3.dll for the sdlfetch tests")

// makeZip returns a zip holding files, in order: name, content, name, ...
func makeZip(t *testing.T, files ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i < len(files); i += 2 {
		w, err := zw.Create(files[i])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, files[i+1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releaseZip is a synthetic SDL release zip, laid out like the real one.
func releaseZip(t *testing.T) []byte {
	t.Helper()
	return makeZip(t,
		"SDL3.dll", string(fakeDLL),
		"README.md", "# SDL3\n",
		"LICENSE.txt", "zlib\n",
	)
}

// serve starts a server that answers GET /<name> with body, or 404 when
// body is nil, and counts the requests.
func serve(t *testing.T, name string, body []byte) (url string, hits *atomic.Int32) {
	t.Helper()
	hits = new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if body == nil || r.URL.Path != "/"+name {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/" + name, hits
}

// pinFor returns the pin that matches zipData and dll.
func pinFor(url string, zipData, dll []byte) pin {
	return pin{
		ZipURL:    url,
		ZipName:   "SDL3-9.9.9-win32-x64.zip",
		ZipSize:   int64(len(zipData)),
		ZipSHA256: sha256Hex(zipData),
		DLLName:   "SDL3.dll",
		DLLSHA256: sha256Hex(dll),
	}
}

// tree returns every file under root as slash-separated relative path →
// content.
func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFetch(t *testing.T) {
	good := releaseZip(t)
	noDLL := makeZip(t, "include/SDL3.dll", "nested", "README.md", "x")
	const cachedZip = "cache/SDL3-9.9.9-win32-x64.zip"
	tests := []struct {
		name    string
		serve   []byte // what the server returns; nil: 404
		zipData []byte // what the pin is computed from; nil: good
		before  map[string]string
		noCache bool
		edit    func(*pin)

		wantHow  outcome
		wantErr  []string // substrings of the error; nil: success
		wantHits int32
		want     map[string]string // the files afterwards
	}{
		{
			name:     "download",
			serve:    good,
			wantHow:  downloaded,
			wantHits: 1,
			want:     map[string]string{"bin/SDL3.dll": string(fakeDLL), cachedZip: string(good)},
		},
		{
			name:     "download without cache",
			serve:    good,
			noCache:  true,
			wantHow:  downloaded,
			wantHits: 1,
			want:     map[string]string{"bin/SDL3.dll": string(fakeDLL)},
		},
		{
			name:    "cache hit",
			before:  map[string]string{cachedZip: string(good)},
			wantHow: fromCache,
			want:    map[string]string{"bin/SDL3.dll": string(fakeDLL), cachedZip: string(good)},
		},
		{
			name:     "bad cache is replaced",
			serve:    good,
			before:   map[string]string{cachedZip: "truncated"},
			wantHow:  downloaded,
			wantHits: 1,
			want:     map[string]string{"bin/SDL3.dll": string(fakeDLL), cachedZip: string(good)},
		},
		{
			name:    "up to date",
			before:  map[string]string{"bin/SDL3.dll": string(fakeDLL)},
			wantHow: upToDate,
			want:    map[string]string{"bin/SDL3.dll": string(fakeDLL)},
		},
		{
			name:     "wrong DLL is replaced",
			serve:    good,
			before:   map[string]string{"bin/SDL3.dll": "old SDL3.dll"},
			wantHow:  downloaded,
			wantHits: 1,
			want:     map[string]string{"bin/SDL3.dll": string(fakeDLL), cachedZip: string(good)},
		},
		{
			name:     "zip hash mismatch",
			serve:    good,
			edit:     func(p *pin) { p.ZipSHA256 = strings.Repeat("ab", 32) },
			wantErr:  []string{"SDL3-9.9.9-win32-x64.zip: sha256 mismatch", "got " + sha256Hex(good), "want " + strings.Repeat("ab", 32)},
			wantHits: 1,
			want:     map[string]string{},
		},
		{
			name:     "zip too long",
			serve:    append(slices.Clone(good), 0),
			wantErr:  []string{"size mismatch: got more than"},
			wantHits: 1,
			want:     map[string]string{},
		},
		{
			name:     "zip too short",
			serve:    good[:len(good)-1],
			wantErr:  []string{"size mismatch: got", "want"},
			wantHits: 1,
			want:     map[string]string{},
		},
		{
			name:     "DLL hash mismatch",
			serve:    good,
			edit:     func(p *pin) { p.DLLSHA256 = strings.Repeat("cd", 32) },
			wantErr:  []string{"SDL3.dll in SDL3-9.9.9-win32-x64.zip: sha256 mismatch", "got " + sha256Hex(fakeDLL), "want " + strings.Repeat("cd", 32)},
			wantHits: 1,
			want:     map[string]string{},
		},
		{
			name:     "DLL hash mismatch keeps the old DLL and the cache",
			before:   map[string]string{"bin/SDL3.dll": "old SDL3.dll", cachedZip: string(good)},
			edit:     func(p *pin) { p.DLLSHA256 = strings.Repeat("cd", 32) },
			wantErr:  []string{"sha256 mismatch"},
			wantHits: 0,
			want:     map[string]string{"bin/SDL3.dll": "old SDL3.dll", cachedZip: string(good)},
		},
		{
			name:     "no DLL in the zip",
			serve:    noDLL,
			zipData:  noDLL,
			wantErr:  []string{"no SDL3.dll in the zip"},
			wantHits: 1,
			want:     map[string]string{},
		},
		{
			name:     "HTTP error",
			wantErr:  []string{"404 Not Found"},
			wantHits: 1,
			want:     map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for name, data := range tt.before {
				writeFile(t, filepath.Join(root, filepath.FromSlash(name)), []byte(data))
			}
			url, hits := serve(t, "SDL3-9.9.9-win32-x64.zip", tt.serve)
			zipData := tt.zipData
			if zipData == nil {
				zipData = good
			}
			p := pinFor(url, zipData, fakeDLL)
			if tt.edit != nil {
				tt.edit(&p)
			}
			f := &fetcher{
				pin:      p,
				outDir:   filepath.Join(root, "bin"),
				cacheDir: filepath.Join(root, "cache"),
				client:   http.DefaultClient,
				log:      io.Discard,
			}
			if tt.noCache {
				f.cacheDir = ""
			}

			how, err := f.fetch(context.Background())
			switch {
			case tt.wantErr == nil && err != nil:
				t.Fatalf("fetch: %v", err)
			case tt.wantErr == nil && how != tt.wantHow:
				t.Errorf("fetch = %v, want %v", how, tt.wantHow)
			case tt.wantErr != nil && err == nil:
				t.Errorf("fetch = %v, want an error containing %q", how, tt.wantErr)
			case tt.wantErr != nil:
				for _, s := range tt.wantErr {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("fetch error %q does not contain %q", err, s)
					}
				}
			}
			if got := hits.Load(); got != tt.wantHits {
				t.Errorf("%d requests, want %d", got, tt.wantHits)
			}
			// Also catches leftover temporary files.
			if got := tree(t, root); !maps.Equal(got, tt.want) {
				t.Errorf("files afterwards = %q, want %q", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(tt.want)))
			}
		})
	}
}

// TestSecondRunOffline runs fetch twice; the second run must report "up to
// date" with the server gone.
func TestSecondRunOffline(t *testing.T) {
	good := releaseZip(t)
	root := t.TempDir()
	hits := new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(good)
	}))
	f := &fetcher{
		pin:      pinFor(srv.URL+"/SDL3-9.9.9-win32-x64.zip", good, fakeDLL),
		outDir:   filepath.Join(root, "bin"),
		cacheDir: filepath.Join(root, "cache"),
		client:   srv.Client(),
		log:      io.Discard,
	}
	if how, err := f.fetch(context.Background()); err != nil || how != downloaded {
		t.Fatalf("first fetch = %v, %v; want %v", how, err, downloaded)
	}
	srv.Close()
	dll := filepath.Join(root, "bin", "SDL3.dll")
	before, err := os.Stat(dll)
	if err != nil {
		t.Fatal(err)
	}

	if how, err := f.fetch(context.Background()); err != nil || how != upToDate {
		t.Fatalf("second fetch = %v, %v; want %v", how, err, upToDate)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("%d requests, want 1", got)
	}
	after, err := os.Stat(dll)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("second fetch rewrote %s", dll)
	}
}

func TestLogMessages(t *testing.T) {
	good := releaseZip(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cache", "SDL3-9.9.9-win32-x64.zip"), []byte("truncated"))
	writeFile(t, filepath.Join(root, "bin", "SDL3.dll"), []byte("old"))
	url, _ := serve(t, "SDL3-9.9.9-win32-x64.zip", good)
	var log strings.Builder
	f := &fetcher{
		pin:      pinFor(url, good, fakeDLL),
		outDir:   filepath.Join(root, "bin"),
		cacheDir: filepath.Join(root, "cache"),
		client:   http.DefaultClient,
		log:      &log,
	}
	if _, err := f.fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		"SDL3.dll has sha256 " + sha256Hex([]byte("old")) + ", want " + sha256Hex(fakeDLL) + "; replacing it",
		"ignoring the cached zip: SDL3-9.9.9-win32-x64.zip: size mismatch",
		"downloading " + url,
	} {
		if !strings.Contains(log.String(), s) {
			t.Errorf("log %q does not contain %q", log.String(), s)
		}
	}
}

func TestOutcomeString(t *testing.T) {
	for o, want := range map[outcome]string{
		upToDate:   "up to date",
		fromCache:  "extracted from the cached zip",
		downloaded: "downloaded",
		outcome(9): "outcome(9)",
	} {
		if got := o.String(); got != want {
			t.Errorf("outcome(%d).String() = %q, want %q", int(o), got, want)
		}
	}
}

func TestRunFlags(t *testing.T) {
	tests := []struct {
		args     []string
		want     int
		inStderr string
	}{
		{[]string{"-h"}, 0, "-cache"},
		{[]string{"-bogus"}, 2, "flag provided but not defined"},
		{[]string{"extra"}, 2, `unexpected argument "extra"`},
	}
	for _, tt := range tests {
		var stdout, stderr bytes.Buffer
		if got := run(tt.args, &stdout, &stderr); got != tt.want {
			t.Errorf("run(%q) = %d, want %d", tt.args, got, tt.want)
		}
		if !strings.Contains(stderr.String(), tt.inStderr) {
			t.Errorf("run(%q) stderr = %q, want it to contain %q", tt.args, stderr.String(), tt.inStderr)
		}
		if stdout.Len() != 0 {
			t.Errorf("run(%q) stdout = %q, want nothing", tt.args, stdout.String())
		}
	}
}
