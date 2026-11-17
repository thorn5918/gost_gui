// Package util 提供日志、IP 地区查询等基础能力。
package util

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LogEntry 一条日志。
type LogEntry struct {
	Time time.Time
	Text string
}

// Logger 线程安全日志器：环形缓冲 + 文件 + 实时订阅。
type Logger struct {
	mu      sync.Mutex
	buf     []LogEntry // 环形缓冲，最多 maxEntries 条
	file    *os.File
	subMu   sync.Mutex
	subs    map[chan LogEntry]struct{}
	maxEnt  int
	fileDir string
}

var Log = NewLogger(2000)

func NewLogger(maxEntries int) *Logger {
	return &Logger{
		buf:    make([]LogEntry, 0, maxEntries),
		subs:   make(map[chan LogEntry]struct{}),
		maxEnt: maxEntries,
	}
}

// SetFileDir 设置日志文件目录（与 exe 同目录）。空则不写文件。
func (l *Logger) SetFileDir(dir string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fileDir = dir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "s5picker.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	l.file = f
	// 把已有缓冲写入新文件
	for _, e := range l.buf {
		l.writeLocked(e)
	}
}

func (l *Logger) writeLocked(e LogEntry) {
	if l.file != nil {
		io.WriteString(l.file, e.Time.Format("[15:04:05] ")+e.Text+"\n")
	}
}

func (l *Logger) log(tag, format string, args ...any) {
	text := "[" + tag + "] " + fmt.Sprintf(format, args...)
	if text != "" && strings.HasSuffix(text, "\n") {
		text = strings.TrimRight(text, "\n")
	}
	l.mu.Lock()
	e := LogEntry{Time: time.Now(), Text: text}
	l.buf = append(l.buf, e)
	if len(l.buf) > l.maxEnt {
		l.buf = l.buf[len(l.buf)-l.maxEnt:]
	}
	l.writeLocked(e)
	l.mu.Unlock()

	l.subMu.Lock()
	for ch := range l.subs {
		select {
		case ch <- e:
		default: // 订阅者阻塞时不拖累其他组件
		}
	}
	l.subMu.Unlock()
}

func (l *Logger) Infof(format string, args ...any)  { l.log("信息", format, args...) }
func (l *Logger) Checkf(format string, args ...any) { l.log("检测", format, args...) }
func (l *Logger) Switchf(format string, args ...any) { l.log("切换", format, args...) }
func (l *Logger) FwdLogf(format string, args ...any) { l.log("转发", format, args...) }
func (l *Logger) Errorf(format string, args ...any) { l.log("错误", format, args...) }

// Subscribe 返回一个日志订阅通道。调用方负责调用 Unsubscribe。
func (l *Logger) Subscribe() chan LogEntry {
	ch := make(chan LogEntry, 256)
	l.subMu.Lock()
	l.subs[ch] = struct{}{}
	l.subMu.Unlock()
	return ch
}

func (l *Logger) Unsubscribe(ch chan LogEntry) {
	l.subMu.Lock()
	delete(l.subs, ch)
	l.subMu.Unlock()
}

// Snapshot 返回当前缓冲的副本。
func (l *Logger) Snapshot() []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LogEntry, len(l.buf))
	copy(out, l.buf)
	return out
}

func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}

// ExeDir 返回可执行文件所在目录；失败则返回当前目录。
func ExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}
