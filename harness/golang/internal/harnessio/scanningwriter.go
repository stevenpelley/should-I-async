package harnessio

import (
	"bufio"
	"io"

	"github.com/go-errors/errors"
)

// A WriteCloser that scans tokens by the delimiter splitFunc of NewScanWriter
// (see bufio.Scanner) and then subsequently writes writes each token and the
// delimeter to NewScanWriter's writer in a single call to Write.  Closing a
// ScanningWriter does _not_ close the destination writer passed to NewScanWriter as
// it may be shared.
type ScanningWriter struct {
	readerDoneChan chan struct{}
	pipeWriter     *io.PipeWriter
	err            error
}

func NewScanningWriter(writer io.Writer, splitFunc bufio.SplitFunc, outputDelimeter []byte) *ScanningWriter {
	w := &ScanningWriter{}
	var pipeReader *io.PipeReader
	pipeReader, w.pipeWriter = io.Pipe()
	scanner := bufio.NewScanner(pipeReader)
	if splitFunc != nil {
		scanner.Split(splitFunc)
	}
	w.readerDoneChan = make(chan struct{})
	go func() {
		for scanner.Scan() {
			bytes := scanner.Bytes()
			bytes = append(bytes, outputDelimeter...)
			_, err := writer.Write(bytes)
			if err != nil {
				w.err = errors.WrapPrefix(err, "ScanWriter destWriter Write(): ", 0)
				break
			}
		}
		// prohibit new writes to pipeWriter.  If we ended the above loop by
		// reaching EOF then it is already closed.
		readerCloseErr := pipeReader.Close()
		w.err = errors.Join(
			w.err,
			errors.WrapPrefix(scanner.Err(), "ScanWriter scanner Scan(): ", 0),
			errors.WrapPrefix(readerCloseErr, "ScanWriter pipereader Close(): ", 0),
		)
		close(w.readerDoneChan)
	}()
	return w
}

func (w *ScanningWriter) Write(p []byte) (n int, err error) {
	return w.pipeWriter.Write(p)
}

func (w *ScanningWriter) Close() error {
	err := w.pipeWriter.Close()
	<-w.readerDoneChan
	return errors.Join(w.err, err)
}
