package runnerio

import (
	"bufio"
	"io"
	"log/slog"

	"github.com/go-errors/errors"
)

type WriteCountCloser interface {
	Write(p []byte) (n int, err error)
	Close() error
	GetTotalBytesWritten() uint64
}

// asserts that it satisfies the interface
var _ WriteCountCloser = &OutputLoggerWriter{}

// object to connect exec.Cmd's stdout and stderr to logging.
// should not be created directly, instead call NewOutputLoggerWriter
// must call Close() when after corresponding command terminates to flush/log
// remaining output.
type OutputLoggerWriter struct {
	totalBytes     uint64
	readerDoneChan chan struct{}
	pipeWriter     *io.PipeWriter
	err            error
}

// commandIdx "names" the command (corresponds to some slice of commands)
// outputName names the stream in log messages, i.e., stdout or stderr
func NewOutputLoggerWriter(commandIdx int, outputName string) *OutputLoggerWriter {
	w := &OutputLoggerWriter{}
	var pipeReader *io.PipeReader
	pipeReader, w.pipeWriter = io.Pipe()
	scanner := bufio.NewScanner(pipeReader)
	w.readerDoneChan = make(chan struct{})
	go func() {
		for scanner.Scan() {
			slog.Info(
				"command output",
				"command_index", commandIdx,
				"name", outputName,
				"line", scanner.Text())
			w.totalBytes += uint64(len(scanner.Bytes()))
		}
		if err := scanner.Err(); err != nil {
			w.err = errors.Errorf("scanner idx %v name %v: %w", commandIdx, outputName, err)
		}
		if err := pipeReader.Close(); err != nil {
			w.err = errors.Join(w.err, errors.Errorf(
				"pipeReader close idx %v name %v: %w", commandIdx, outputName, err))
		}
		close(w.readerDoneChan)
	}()
	return w
}

func (w *OutputLoggerWriter) Write(p []byte) (n int, err error) {
	return w.pipeWriter.Write(p)
}

func (w *OutputLoggerWriter) Close() error {
	err := w.pipeWriter.Close()
	<-w.readerDoneChan
	return errors.Join(w.err, err)
}

func (w *OutputLoggerWriter) GetTotalBytesWritten() uint64 {
	return w.totalBytes
}
