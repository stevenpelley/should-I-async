package harnessio

import (
	"io"
	"sync"
)

type LockingWriter struct {
	locker sync.Locker
	writer io.Writer
}

func NewLockingWriter(writer io.Writer) *LockingWriter {
	return &LockingWriter{locker: &sync.Mutex{}, writer: writer}
}

func (w *LockingWriter) Write(p []byte) (int, error) {
	w.locker.Lock()
	defer w.locker.Unlock()
	return w.writer.Write(p)
}
