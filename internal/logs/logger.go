package logs

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Logger struct {
	logger *log.Logger
	file   *os.File
	path   string
	mu     sync.Mutex
}

func New(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}

	writer := io.MultiWriter(os.Stdout, file)
	return &Logger{
		logger: log.New(writer, "", log.LstdFlags),
		file:   file,
		path:   path,
	}, nil
}

func (l *Logger) Close() error {
	if l.file == nil {
		return nil
	}
	return l.file.Close()
}

func (l *Logger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logger.Printf(format, args...)
}

func (l *Logger) Path() string {
	return l.path
}

func TailLines(path string, lineCount int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	if lineCount <= 0 {
		return string(data), nil
	}

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) <= lineCount {
		return strings.Join(lines, "\n"), nil
	}

	return strings.Join(lines[len(lines)-lineCount:], "\n"), nil
}
