// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

//go:build unix

package gojja2_test

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
)

// The other half of why jinja2 gates on isfile: opening a FIFO with no writer
// blocks forever. A template chooses the name an include resolves, so a loader
// that opens whatever it is handed can be made to hang -- and a hang is not
// something the render budget can interrupt, because it happens before the
// render starts.
//
// A real filesystem is needed here; fstest.MapFS synthesises its entries and
// has no irregular files to offer.
func TestFSLoaderDoesNotOpenAnIrregularFile(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe.html"), 0o600); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}
	env := gojja2.New(gojja2.WithLoader(gojja2.FSLoader{FS: os.DirFS(dir)}))

	done := make(chan error, 1)
	go func() {
		_, err := env.GetTemplate("pipe.html")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errs.TemplateNotFound) {
			t.Errorf("got %v, want TemplateNotFound", err)
		}
	case <-time.After(30 * time.Second):
		// Left blocked on purpose: failing here is the point, and the
		// goroutine cannot be unblocked without a writer.
		t.Fatal("loading a FIFO blocked; it must be reported as not found")
	}
}
