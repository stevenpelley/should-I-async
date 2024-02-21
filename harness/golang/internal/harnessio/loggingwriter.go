package harnessio

import (
	"bytes"
	"context"
	"log/slog"
	"sync/atomic"
)

// Indicates how to log as a Writer.
// Logs to the provided logger, at the given level, with the msg used as the log
// message.  Any bytes provided to Write() will be logged in key "key"
type LogWrapper struct {
	logger *slog.Logger
	level  slog.Level
	msg    string
	key    string
}

func CreateLogWrapper(logger *slog.Logger, commandIdx int, outputStreamName string) LogWrapper {
	logger = logger.With("command_index", commandIdx, "output_stream_name", outputStreamName)
	return LogWrapper{
		logger: logger,
		level:  slog.LevelInfo,
		msg:    "command output",
		key:    "line",
	}
}
func (w *LogWrapper) write(p []byte) {
	newBytes := bytes.TrimRight(p, "\n\r")
	w.logger.LogAttrs(
		context.Background(),
		w.level,
		w.msg,
		slog.String(w.key, string(newBytes)))
}

// a Writer that logs each Write() according to the provided LogWrapper.
// NB: the boundaries of Write() are important.  Write([]byte("ab")) is
// different from Write([]byte("a")) followed by Write([]byte("b")).
// The former logs 1 line with "ab", the latter logs 2 lines, "a" and "b"
type LoggingWriter struct {
	wrapper           LogWrapper
	totalBytesWritten atomic.Uint64
}

func (w *LoggingWriter) Write(p []byte) (int, error) {
	w.wrapper.write(p)
	w.totalBytesWritten.Add(uint64(len(p)))
	return len(p), nil
}
