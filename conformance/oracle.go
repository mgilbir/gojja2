// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Oracle is a live CPython jinja2, kept warm behind a pipe.
//
// The batch tool starts an interpreter per run, which costs about a tenth of a
// second and is irrelevant for a corpus of a thousand files. A differential
// fuzzer asks hundreds of thousands of questions, so it needs the interpreter
// to stay up: this drives tools/oracle/oracle_server.py over stdin and stdout,
// one JSON object per line.
type Oracle struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *strings.Builder

	// mu serialises requests, because the server is a single interpreter
	// answering one line at a time.
	mu sync.Mutex
}

// OracleRequest is one render to perform.
type OracleRequest struct {
	Name      string            `json:"name,omitempty"`
	Source    string            `json:"src"`
	Context   json.RawMessage   `json:"ctx,omitempty"`
	Settings  map[string]any    `json:"settings,omitempty"`
	Templates map[string]string `json:"templates,omitempty"`
}

// OracleResult is what CPython jinja2 did with it.
type OracleResult struct {
	OK     bool         `json:"ok"`
	Output string       `json:"output"`
	Error  *GoldenError `json:"error"`
	// Resource is set when the render hit the server's time or memory
	// limit rather than failing on its own terms. Such a result says
	// nothing about conformance and is discarded.
	Resource bool `json:"resource"`
}

// ErrNoOracle reports that no CPython jinja2 is available to ask.
var ErrNoOracle = errors.New("conformance: no CPython jinja2 oracle available (run `make venv`)")

// RepoRoot finds the module root by walking up from the working directory.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("conformance: go.mod not found above the working directory")
		}
		dir = parent
	}
}

// StartOracle launches the oracle server.
//
// It returns ErrNoOracle when the virtualenv is missing, so a test can skip
// rather than fail on a checkout that has not run `make venv`.
func StartOracle() (*Oracle, error) {
	root, err := RepoRoot()
	if err != nil {
		return nil, err
	}

	python := os.Getenv("GOJJA2_ORACLE_PYTHON")
	if python == "" {
		python = filepath.Join(root, ".venv", "bin", "python")
	}
	script := filepath.Join(root, "tools", "oracle", "oracle_server.py")
	for _, path := range []string{python, script} {
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("%w: %s missing", ErrNoOracle, path)
		}
	}

	cmd := exec.Command(python, script)
	cmd.Dir = filepath.Join(root, "tools", "oracle")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoOracle, err)
	}

	o := &Oracle{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReaderSize(stdout, 1<<20),
		stderr: stderr,
	}
	// Prove the interpreter answers before any caller depends on it.
	if _, err := o.Render(OracleRequest{Source: "{{ 1 + 1 }}"}); err != nil {
		_ = o.Close()
		return nil, fmt.Errorf("%w: handshake failed: %v", ErrNoOracle, err)
	}
	return o, nil
}

// Render asks the oracle what CPython jinja2 makes of a template.
func (o *Oracle) Render(req OracleRequest) (*OracleResult, error) {
	line, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	if _, err := o.stdin.Write(append(line, '\n')); err != nil {
		return nil, fmt.Errorf("oracle write: %w (stderr: %s)", err, o.stderr)
	}
	reply, err := o.stdout.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("oracle read: %w (stderr: %s)", err, o.stderr)
	}
	var result OracleResult
	if err := json.Unmarshal(reply, &result); err != nil {
		return nil, fmt.Errorf("oracle reply %q: %w", reply, err)
	}
	return &result, nil
}

// Close shuts the interpreter down.
func (o *Oracle) Close() error {
	_ = o.stdin.Close()
	return o.cmd.Wait()
}
