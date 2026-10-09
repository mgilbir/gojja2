// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
)

// windowsHostileNames are template names that mean something other than a file
// under the root to the Win32 path layer: a drive, a device namespace, a
// reserved device, an alternate data stream, and segments that become ".." once
// Win32 strips their trailing dots and spaces. safeJoin refuses only "..", so on
// Windows it is os.DirFS's own validation that stands between these and the
// parent directory.
var windowsHostileNames = []string{
	`C:secret.html`, `C:/Windows/win.ini`, `\\?\C:\secret.html`, `\\.\CON`,
	`CON`, `aux`, `NUL.txt`, `COM1`, `con/x.html`,
	`page.html::$DATA`, `page.html:s`,
	`.. /secret.html`, `... /secret.html`, `.. `, `..\./secret.html`,
	`page.html.`, `page.html `, `PAGE.HTML`,
}

// TestFSLoaderOnWindowsHostileNames loads each of those names through an
// FSLoader over os.DirFS, with a secret one level above the root. None may
// serve the secret; each either serves the page that really is under the root
// or is TemplateNotFound -- never another error, and never a hang on a device.
//
// It is portable and runs everywhere, but it can only fail on Windows, where
// the names are hostile. So it also opens each through a filesystem that joins
// names the naive way, and on Windows requires that to escape at least once:
// otherwise the names are not dangerous on this runner, and the assertion
// above would pass for the wrong reason.
func TestFSLoaderOnWindowsHostileNames(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		filepath.Join(dir, "secret.html"): "SECRET",
		filepath.Join(root, "page.html"):  "PAGE",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	loader := gojja2.FSLoader{FS: os.DirFS(root)}
	naive := naiveFS(root)
	escapes := 0
	for _, name := range windowsHostileNames {
		src, err := loader.Load(name)
		switch {
		case err == nil && src == "SECRET":
			t.Errorf("%q served the file above the root", name)
		case err == nil && src != "PAGE":
			t.Errorf("%q served %q", name, src)
		case err != nil && !errors.Is(err, gojja2.ErrNotFound):
			t.Errorf("%q: %v, want TemplateNotFound", name, err)
		}
		// Only names that could reach the secret go through the control:
		// opening a device such as CON or COM1 the naive way can block on
		// it, which is the hang the loader is being held to avoid.
		if !strings.Contains(name, "secret") && !strings.Contains(name, "..") {
			continue
		}
		if b, err := fs.ReadFile(naive, name); err == nil && string(b) == "SECRET" {
			escapes++
		}
	}
	if runtime.GOOS == "windows" && escapes == 0 {
		t.Error("no name escapes even a naive join on this Windows runner, " +
			"so the FSLoader assertions above prove nothing here")
	}
	t.Logf("%s: a naive join escapes with %d of %d names", runtime.GOOS, escapes, len(windowsHostileNames))
}

// naiveFS opens a name by joining it to a root with the host's path rules and
// nothing else -- the control the test above measures os.DirFS against.
type naiveFS string

func (r naiveFS) Open(name string) (fs.File, error) {
	return os.Open(filepath.Join(string(r), filepath.FromSlash(name)))
}
