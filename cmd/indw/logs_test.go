package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrintTail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agent.log")
	var all strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&all, "line %d\n", i)
	}
	if err := os.WriteFile(p, []byte(all.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(p)
	defer f.Close()
	var out bytes.Buffer
	off, err := printTail(&out, f, 200)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 200 || lines[0] != "line 101" || lines[199] != "line 300" {
		t.Fatalf("got %d lines, first=%q last=%q", len(lines), lines[0], lines[len(lines)-1])
	}
	if off != int64(all.Len()) {
		t.Fatalf("offset %d, want %d", off, all.Len())
	}

	// Empty file prints nothing.
	empty := filepath.Join(t.TempDir(), "empty.log")
	_ = os.WriteFile(empty, nil, 0o600)
	ef, _ := os.Open(empty)
	defer ef.Close()
	out.Reset()
	if _, err := printTail(&out, ef, 200); err != nil || out.Len() != 0 {
		t.Fatalf("empty file: err=%v out=%q", err, out.String())
	}
}

type syncBuf struct {
	ch chan string
}

func (s *syncBuf) Write(p []byte) (int, error) { s.ch <- string(p); return len(p), nil }

func TestFollowFileAppendsAndRotation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agent.log")
	_ = os.WriteFile(p, []byte("old\n"), 0o600)
	w := &syncBuf{ch: make(chan string, 10)}
	stop := make(chan struct{})
	done := make(chan error)
	go func() { done <- followFile(w, p, 4, 10*time.Millisecond, stop) }()

	expect := func(want string) {
		t.Helper()
		select {
		case got := <-w.ch:
			if got != want {
				t.Fatalf("followed %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}

	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("new 1\n")
	f.Close()
	expect("new 1\n")

	// Rotation: file replaced by a shorter one — follow restarts from 0.
	_ = os.WriteFile(p, []byte("r\n"), 0o600)
	expect("r\n")

	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRedirectToAgentLogRotates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AppData", home)
	p, err := agentLogPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), maxLogSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	t.Cleanup(func() { os.Stdout, os.Stderr = origOut, origErr })
	if err := redirectToAgentLog(); err != nil {
		t.Fatal(err)
	}
	fmt.Println("hello")
	os.Stdout.Close()
	os.Stdout, os.Stderr = origOut, origErr

	if fi, err := os.Stat(p + ".1"); err != nil || fi.Size() != maxLogSize+1 {
		t.Fatalf("rotated file: %v", err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "--- run-once") || !strings.HasSuffix(string(b), "hello\n") {
		t.Fatalf("new log = %q", b)
	}
}
