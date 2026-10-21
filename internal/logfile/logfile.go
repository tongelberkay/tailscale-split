// Package logfile is a small size-capped log with secrets redacted.
package logfile

import (
	"fmt"
	"os"
	"regexp"
	"sync"
	"time"
)

const maxSize = 5 << 20

type Log struct {
	path string
	mu   sync.Mutex
	f    *os.File
	size int64
	echo bool
}

// Open appends to path, rotating it to path+".old" past 5 MB. With echo,
// lines are also written to stderr.
func Open(path string, echo bool) (*Log, error) {
	l := &Log{path: path, echo: echo}
	return l, l.open()
}

func (l *Log) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	fi, _ := f.Stat()
	l.f, l.size = f, 0
	if fi != nil {
		l.size = fi.Size()
	}
	return nil
}

func (l *Log) Logf(format string, args ...any) {
	line := time.Now().Format("2006-01-02 15:04:05.000 ") + Redact(fmt.Sprintf(format, args...))
	if len(line) == 0 || line[len(line)-1] != '\n' {
		line += "\n"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.echo {
		os.Stderr.WriteString(line)
	}
	if l.f == nil {
		return
	}
	if l.size+int64(len(line)) > maxSize {
		l.f.Close()
		os.Rename(l.path, l.path+".old")
		if l.open() != nil {
			l.f = nil
			return
		}
	}
	n, _ := l.f.WriteString(line)
	l.size += int64(n)
}

func (l *Log) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
}

var (
	loginURL = regexp.MustCompile(`https://login\.tailscale\.com/[A-Za-z0-9/_?=&.%~-]+`)
	keys     = regexp.MustCompile(`(?:privkey:|nodekey:|mkey:|discokey:|tskey-)[A-Za-z0-9_-]+`)
)

// Redact hides sign-in links and keys. Logs can still contain device names,
// IP addresses and paths.
func Redact(s string) string {
	s = loginURL.ReplaceAllString(s, "[sign-in link hidden]")
	return keys.ReplaceAllString(s, "[key hidden]")
}
