package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/abinashstack/indmoney-watch/internal/config"
)

const (
	logTailLines = 200
	// maxLogSize triggers a single-generation rotation (agent.log → agent.log.1)
	// when the poller writes the log itself, so it can't grow without bound.
	maxLogSize = 5 << 20
)

func agentLogPath() (string, error) {
	d, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "agent.log"), nil
}

// redirectToAgentLog points this process's stdout and stderr at agent.log
// (append), rotating it first if it has grown past maxLogSize.
func redirectToAgentLog() error {
	p, err := agentLogPath()
	if err != nil {
		return err
	}
	if fi, err := os.Stat(p); err == nil && fi.Size() > maxLogSize {
		_ = os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	fmt.Fprintf(f, "--- run-once %s\n", time.Now().Format(time.RFC3339))
	os.Stdout = f
	os.Stderr = f
	return nil
}

// cmdLogs prints the last lines of agent.log and, with -f, follows it. It's
// implemented in Go rather than shelling out to tail(1) so it behaves the
// same on macOS and Windows.
func cmdLogs(args []string) error {
	p, err := agentLogPath()
	if err != nil {
		return err
	}
	follow := false
	for _, a := range args {
		if a == "-f" || a == "--follow" {
			follow = true
		}
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("(no log yet — agent hasn't run; use `indw start` to install it, or `indw run-once` to test)")
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	off, err := printTail(os.Stdout, f, logTailLines)
	if err != nil || !follow {
		return err
	}
	return followFile(os.Stdout, p, off, time.Second, nil)
}

// printTail writes the last n lines of f to w and returns f's size, i.e. the
// offset to follow from. It reads at most the final 1 MiB.
func printTail(w io.Writer, f *os.File, n int) (int64, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := fi.Size()
	start := size - 1<<20
	if start < 0 {
		start = 0
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return 0, err
	}
	buf = bytes.TrimRight(buf, "\n")
	lines := bytes.Split(buf, []byte("\n"))
	if start > 0 && len(lines) > 1 {
		lines = lines[1:] // first line is probably partial
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	bw := bufio.NewWriter(w)
	for _, l := range lines {
		if len(l) == 0 && len(lines) == 1 {
			break
		}
		bw.Write(l)
		bw.WriteByte('\n')
	}
	return size, bw.Flush()
}

// followFile polls path and copies anything appended after off to w. If the
// file shrinks or is replaced (rotation), it starts again from the top. It
// returns when stop is closed (nil stop: runs until the process exits).
func followFile(w io.Writer, path string, off int64, every time.Duration, stop <-chan struct{}) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return nil
		case <-t.C:
		}
		f, err := os.Open(path)
		if err != nil {
			continue // mid-rotation
		}
		if fi, err := f.Stat(); err == nil {
			if fi.Size() < off {
				off = 0
			}
			if fi.Size() > off {
				n, _ := io.Copy(w, io.NewSectionReader(f, off, fi.Size()-off))
				off += n
			}
		}
		f.Close()
	}
}
