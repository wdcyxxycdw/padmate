// Command sdlfetch downloads the pinned SDL3.dll (PKG-01, `task sdl`).
//
// Run it from daemon/:
//
//	go run ./tools/sdlfetch [-out DIR] [-cache DIR]
//
// It writes DIR/SDL3.dll (default ../bin) with the SHA-256 that
// internal/sdlpin pins. When that file is already there with the pinned
// hash, it prints "up to date" and exits 0 without using the network.
// Otherwise it takes the release zip from the cache directory if the
// cached copy has the pinned size and SHA-256, or else downloads it and
// checks both. It extracts only SDL3.dll, checks the DLL's SHA-256 and
// writes it atomically (a temporary file renamed over the target). A
// downloaded zip is then stored in the cache (default
// <os.UserCacheDir()>/padmate/sdl; -cache "" disables it), so `task clean`
// followed by `task sdl` works offline.
//
// On a size or hash mismatch it writes nothing and the message names both
// values. It exits 0 on success, 1 when the DLL cannot be fetched or
// verified, and 2 on bad flags.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlpin"
)

// downloadTimeout bounds the whole download, redirects included.
const downloadTimeout = 5 * time.Minute

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sdlfetch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", filepath.Join("..", "bin"), "directory to write "+sdlpin.DLLName+" into")
	cache := fs.String("cache", defaultCacheDir(), `directory that keeps the downloaded zip ("" disables the cache)`)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "sdlfetch: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	f := &fetcher{
		pin:      pinned(),
		outDir:   *out,
		cacheDir: *cache,
		client:   &http.Client{Timeout: downloadTimeout},
		log:      stderr,
	}
	how, err := f.fetch(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "sdlfetch: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: %s (SDL %s, sha256 %s)\n",
		filepath.Join(*out, sdlpin.DLLName), how, sdlpin.Version, sdlpin.DLLSHA256)
	return 0
}

// pinned returns the release that internal/sdlpin pins.
func pinned() pin {
	return pin{
		ZipURL:    sdlpin.ZipURL,
		ZipName:   sdlpin.ZipName,
		ZipSize:   sdlpin.ZipSize,
		ZipSHA256: sdlpin.ZipSHA256,
		DLLName:   sdlpin.DLLName,
		DLLSHA256: sdlpin.DLLSHA256,
	}
}

// defaultCacheDir is <os.UserCacheDir()>/padmate/sdl, or "" (no cache)
// when the platform has no user cache directory.
func defaultCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "padmate", "sdl")
}
