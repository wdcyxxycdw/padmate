// Command packageinstaller prepares and checks the installer (PKG-03,
// PKG-06); `task package:installer` runs it around electron-builder.
//
//	packageinstaller version -version v0.14.0
//	packageinstaller stage   -version v0.14.0 [-root ..] [-bin ../bin] [-out ../dist/installer-stage]
//	packageinstaller verify  -version v0.14.0 [-dist ../dist/installer] [-run=false]
//	                         [-signed SUBJECT [-allow-untrusted-root] [-timestamp=false]] [-update-info]
//
// version prints the installer's file name and the app version, or fails when
// the argument is not a release tag. stage checks the built executables and
// SDL3.dll and lays out the files electron-builder packs. verify reads the
// version back from the built artifacts and fails when any differs; it also
// runs the PE hygiene check (internal/pecheck) on the executables.
//
// -signed SUBJECT (a common name or a distinguished name) demands that
// padmate.exe, padmated.exe, padmatectl.exe and the installer be signed by
// SUBJECT with SHA-256, with a timestamp (-timestamp=false for the offline
// test), intact and, unless -allow-untrusted-root, chained to a trusted root;
// that SDL3.dll be unsigned; and that resources/app-update.yml name the signer
// as publisherName in the exact form electron-updater compares. An empty
// -signed is refused, not ignored. -update-info checks latest.yml against the
// installer (version, file name, SHA-512, and the size when latest.yml has one)
// and requires it to exist;
// without the flag it is checked when it is there.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/installerpkg"
	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror"
)

func main() {
	if err := publicmirror.RefusePackagingHere(); err != nil {
		fmt.Fprintln(os.Stderr, "packageinstaller:", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: packageinstaller version|stage|verify -version vX.Y.Z [flags]")
		return 2
	}
	fs := flag.NewFlagSet("packageinstaller "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	label := fs.String("version", "", "release tag: v and a semantic version")
	var root, bin, out, dist, signed *string
	var runBinaries, allowUntrustedRoot, timestamp, updateInfo *bool
	switch args[0] {
	case "version":
	case "stage":
		root = fs.String("root", "..", "repository root")
		bin = fs.String("bin", "../bin", "folder with the built padmated.exe, padmatectl.exe and SDL3.dll")
		out = fs.String("out", "../dist/installer-stage", "stage folder to make")
	case "verify":
		dist = fs.String("dist", "../dist/installer", "electron-builder's output folder: the installer and win-unpacked")
		runBinaries = fs.Bool("run", true, "also run padmated -version and padmatectl version")
		signed = fs.String("signed", "", "demand that padmate.exe, padmated.exe, padmatectl.exe and the installer are signed by this publisher (common name or distinguished name)")
		allowUntrustedRoot = fs.Bool("allow-untrusted-root", false, "with -signed: accept a certificate chain that ends in a root this machine does not trust (a self-signed test certificate)")
		timestamp = fs.Bool("timestamp", true, "with -signed: demand a timestamp in every signature (false for the offline test)")
		updateInfo = fs.Bool("update-info", false, "require latest.yml and check it against the installer (it is checked anyway when it exists)")
	default:
		fmt.Fprintf(stderr, "packageinstaller: unknown command %q\n", args[0])
		return 2
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "packageinstaller: unexpected positional argument")
		return 2
	}
	tag, err := installerpkg.ParseTag(*label)
	if err != nil {
		fmt.Fprintln(stderr, "packageinstaller:", err)
		return 1
	}
	// A package is built from the development repository, which has the
	// documentation that ships in it. main has refused in the mirror already;
	// this covers the root that was named. It comes after the check of the
	// version so that the tests, which run in whatever tree they are in, see
	// the same errors in both.
	if root != nil {
		if err := publicmirror.RefusePackagingAt(*root); err != nil {
			fmt.Fprintln(stderr, "packageinstaller:", err)
			return 1
		}
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "%s\n%s\n", tag.App, tag.InstallerName())
	case "stage":
		err = installerpkg.Stage(ctx, installerpkg.StageOptions{Tag: tag, Root: *root, BinDir: *bin, Out: *out, Log: stdout})
	case "verify":
		explicit := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
		// An empty -signed is a mistake, not "-signed was not given": a script
		// that passes "$PADMATE_SIGN_SUBJECT" with the variable unset must not
		// get a green verify of signatures nobody looked at.
		if explicit["signed"] && strings.TrimSpace(*signed) == "" {
			fmt.Fprintln(stderr, "packageinstaller: -signed needs the publisher the files must be signed by (a common name or a distinguished name), and it is empty: is PADMATE_SIGN_SUBJECT set?")
			return 2
		}
		wantSigned := explicit["signed"]
		if !wantSigned && (explicit["allow-untrusted-root"] || explicit["timestamp"]) {
			fmt.Fprintln(stderr, "packageinstaller: -allow-untrusted-root and -timestamp only go with -signed")
			return 2
		}
		installer := filepath.Join(*dist, tag.InstallerName())
		root := filepath.Join(*dist, "win-unpacked")
		problems := installerpkg.CheckTree(ctx, installerpkg.TreeOptions{Root: root, Tag: tag, Run: *runBinaries})
		problems = append(problems, installerpkg.CheckInstaller(installer, tag)...)
		if wantSigned {
			problems = append(problems, installerpkg.CheckSignatures(root, installer, installerpkg.SignOptions{
				Subject: *signed, AllowUntrustedRoot: *allowUntrustedRoot, Timestamp: *timestamp,
			})...)
		}
		latest := filepath.Join(*dist, installerpkg.UpdateFile)
		if _, statErr := os.Stat(latest); statErr == nil || *updateInfo {
			problems = append(problems, installerpkg.CheckUpdateInfo(latest, installer, tag)...)
		}
		if len(problems) > 0 {
			for _, p := range problems {
				fmt.Fprintln(stderr, "packageinstaller verify:", p)
			}
			return 1
		}
		fmt.Fprintf(stdout, "%s: every part is %s", installer, tag.Label)
		if wantSigned {
			fmt.Fprintf(stdout, ", signed by %s", *signed)
		}
		fmt.Fprintln(stdout)
	}
	if err != nil {
		fmt.Fprintln(stderr, "packageinstaller:", err)
		return 1
	}
	return 0
}
