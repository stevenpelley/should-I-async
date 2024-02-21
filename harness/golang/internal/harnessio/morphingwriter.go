package harnessio

import (
	"bufio"
	"encoding/json"
	"io"

	"github.com/go-errors/errors"
)

// a Writer that:
// - accepts data up until a delimeter
// - constructs (via caller-provided function) a new writer for the stream based
//   on the data up until that delimeter
// - writes and forwards the entire data stream to this new writer, including
//   the data up to the delimeter and data arriving later.
//
// note that because data is buffered in order to find the first occurrence of
// the delimeter, the forwarding of Write() calls does not necessarily observe
// the same _boundaries_, but it will observe the same bytes and order.

type MorphingWriter struct {
	// used for reading until the delimeter.
	pipeWriter *io.PipeWriter

	// eventual morphed writer.  Written from the reader goroutine before
	// closing readerDone
	writer io.Writer
	// any error(s) observed by the reader goroutine.  Multiple errors may be
	// joined.
	readerErr error
	// closed after writer and readerErr are set and the before the reader
	// goroutine returns.
	readerDone chan struct{}
}

func NewMorphingWriter(delim byte, morph func(firstToken []byte) io.Writer) *MorphingWriter {
	pr, pw := io.Pipe()
	reader := bufio.NewReader(pr)
	w := MorphingWriter{pipeWriter: pw, readerDone: make(chan struct{})}

	go func() {
		firstToken, err := reader.ReadBytes(delim)
		pipeClosed := err == io.EOF || err == io.ErrClosedPipe
		if err != nil && !pipeClosed {
			w.readerErr = errors.WrapPrefix(err, "MorphingWriter reader ReadBytes: ", 0)
		}
		// make writers fail.  They'll block for readerDone to close
		pr.Close()
		// even if we see an error we need to try to send the data to a
		// downstream writer.
		w.writer = morph(firstToken)
		var err1 error
		if len(firstToken) > 0 {
			_, err1 = w.writer.Write(firstToken)
		}
		_, err2 := io.CopyN(w.writer, reader, int64(reader.Buffered()))
		w.readerErr = errors.Join(
			w.readerErr,
			errors.WrapPrefix(err1, "MorphingWriter reader Write firstToken: ", 0),
			errors.WrapPrefix(err2, "MorphingWriter reader CopyN: ", 0))

		close(w.readerDone)
	}()

	return &w
}

// assumes caller observed that w.readerDone is closed
func (w *MorphingWriter) writeDelegate(p []byte) (int, error) {
	if w.readerErr != nil {
		return 0, w.readerErr
	}
	return w.writer.Write(p)
}

func (w *MorphingWriter) Write(p []byte) (int, error) {
	var ok bool
	select {
	case _, ok = <-w.readerDone:
	default:
	}
	if ok {
		return w.writeDelegate(p)
	}

	// need to write to the pipe
	n, err := w.pipeWriter.Write(p)
	// includes nil
	if err != io.ErrClosedPipe {
		return n, err
	}

	// on closed pipe we are switching to the new writer
	<-w.readerDone
	n2, err := w.writeDelegate(p)
	return n + n2, err
}

// close the pipeWriter and retrieve any error from the reader goroutine
func (w *MorphingWriter) Close() error {
	err := w.pipeWriter.Close()
	err = errors.WrapPrefix(err, "MorphingWriter Close pipeWriter: ", 0)
	// wait for the reader goroutine to flush the buffer and to see if it
	// produces any error.
	<-w.readerDone
	err = errors.Join(err, errors.Wrap(w.readerErr, 0))
	return err
}

// Returns the underlying morphed writer.  Useful to close, if needed.
func (w *MorphingWriter) GetMorphedWriter() io.Writer {
	var ok bool
	select {
	case _, ok = <-w.readerDone:
	default:
	}
	if !ok {
		panic("GetMorphedWriter: reader is not done, writer has not been created")
	}
	return w.writer
}

// returns a morphing writer that morphs either into a "passthrough" writer
// (writing bytes unmodified) or a "log writer" that passes written lines as the
// field of json log messages.  Morphing is determined by examining the first
// line of data: if it is valid json and is an object containing keys "time",
// "level", and "msg" then it is assumed to already be a json log and will pass
// through.  Data is either passed through or logged to the provided writer.  If this morphs to a logging
func PassthroughOrLogJsonWriter(writer io.Writer, logWrapper LogWrapper) *MorphingWriter {
	return NewMorphingWriter('\n', func(firstToken []byte) io.Writer {
		var m map[string]interface{}
		err := json.Unmarshal(firstToken, &m)
		if err != nil || m == nil || !mapContainsAll(m, []string{"time", "level", "msg"}) {
			// not json, wrap in a logger
			return &LoggingWriter{wrapper: logWrapper}
		}
		return writer
	})
}

func mapContainsAll(m map[string]interface{}, keys []string) bool {
	for _, k := range keys {
		_, ok := m[k]
		if !ok {
			return false
		}
	}
	return true
}

// create a writer suitable to connect to an exec.Cmd's stdout or stderr
func CreateOutputWriter(logWrapper LogWrapper, downstreamWriter io.Writer) io.WriteCloser {
	return PassthroughOrLogJsonWriter(
		// tokenize by line, assuming the downstream is a locking writer
		NewScanningWriter(downstreamWriter, bufio.ScanLines, []byte("\n")),
		logWrapper)
}
