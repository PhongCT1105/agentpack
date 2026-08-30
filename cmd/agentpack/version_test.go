package main

import (
	"bytes"
	"strings"
	"testing"
)

// execute runs the CLI with the given args and returns combined stdout output.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestVersionCommand(t *testing.T) {
	out, err := execute(t, "version")
	if err != nil {
		t.Fatalf("version command failed: %v", err)
	}
	if !strings.Contains(out, "agentpack") {
		t.Errorf("version output missing program name: %q", out)
	}
	if !strings.Contains(out, version) {
		t.Errorf("version output missing version string %q: %q", version, out)
	}
	if !strings.Contains(out, "commit:") {
		t.Errorf("version output missing commit line: %q", out)
	}
}

func TestVersionFlag(t *testing.T) {
	out, err := execute(t, "--version")
	if err != nil {
		t.Fatalf("--version failed: %v", err)
	}
	if !strings.Contains(out, version) {
		t.Errorf("--version output missing version string %q: %q", version, out)
	}
}

func TestRootShowsHelpWithoutArgs(t *testing.T) {
	out, err := execute(t)
	if err != nil {
		t.Fatalf("bare invocation failed: %v", err)
	}
	if !strings.Contains(out, "Usage:") {
		t.Errorf("bare invocation should print help, got: %q", out)
	}
}

func TestUnknownCommandFails(t *testing.T) {
	_, err := execute(t, "no-such-command")
	if err == nil {
		t.Fatal("unknown command should return an error")
	}
}
