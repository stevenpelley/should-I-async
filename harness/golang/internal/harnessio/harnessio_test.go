package harnessio

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoggingWriter(t *testing.T) {
	require := require.New(t)
	b := &bytes.Buffer{}
	handler := slog.NewJSONHandler(b, nil)
	logger := slog.New(handler)
	lw := &LoggingWriter{wrapper: CreateLogWrapper(logger, 0, "stdout")}

	var err error
	_, err = lw.Write([]byte("asdf\n"))
	require.NoError(err)
	bytes := b.Bytes()
	var m map[string]interface{}
	err = json.Unmarshal(bytes, &m)
	require.NoError(err)
	require.Contains(m, "time")
	require.Contains(m, "level")
	require.Contains(m, "msg")
	require.Contains(m, "command_index")
	require.Contains(m, "output_stream_name")
	require.Contains(m, "line")
	require.Equal("INFO", m["level"])
	require.Equal("command output", m["msg"])
	require.EqualValues(0, m["command_index"])
	require.Equal("stdout", m["output_stream_name"])
	require.Equal("asdf", m["line"])
}

type sliceRecorderWriter struct {
	bytes [][]byte
}

func (w *sliceRecorderWriter) Write(p []byte) (int, error) {
	w.bytes = append(w.bytes, p)
	return len(p), nil
}

func TestScanWriter(t *testing.T) {
	require := require.New(t)
	var input [][]byte
	var output *sliceRecorderWriter

	input = [][]byte{[]byte("asdf")}
	output = testScanWriter(require, input)
	require.EqualValues([][]byte{[]byte("asdf\n")}, output.bytes)

	input = [][]byte{{}, {}}
	output = testScanWriter(require, input)
	require.EqualValues([][]byte(nil), output.bytes)

	input = [][]byte{[]byte("asdf\nqwer\n\n")}
	output = testScanWriter(require, input)
	require.EqualValues([][]byte{[]byte("asdf\n"), []byte("qwer\n"), []byte("\n")}, output.bytes)

	input = [][]byte{[]byte("asdf\n"), []byte("qwe"), []byte("r"), []byte("\n\n")}
	output = testScanWriter(require, input)
	require.EqualValues([][]byte{[]byte("asdf\n"), []byte("qwer\n"), []byte("\n")}, output.bytes)
}

func testScanWriter(require *require.Assertions, writes [][]byte) *sliceRecorderWriter {
	var writer sliceRecorderWriter
	w := NewScanningWriter(&writer, bufio.ScanLines, []byte("\n"))
	for _, bytes := range writes {
		_, err := w.Write(bytes)
		require.NoError(err)
	}
	err := w.Close()
	require.NoError(err)
	return &writer
}

func TestMorphingWriter(t *testing.T) {
	require := require.New(t)
	var writer1 *sliceRecorderWriter
	var writer2 *sliceRecorderWriter

	writer1, writer2 = testMorphingWriter(require, [][]byte{[]byte("a\nb\n")})
	require.EqualValues([]byte("a\nb\n"), bytes.Join(writer1.bytes, []byte{}))
	require.EqualValues([]byte{}, bytes.Join(writer2.bytes, []byte{}))

	writer1, writer2 = testMorphingWriter(require, [][]byte{[]byte("b\nb\n")})
	require.EqualValues([]byte{}, bytes.Join(writer1.bytes, []byte{}))
	require.EqualValues([]byte("b\nb\n"), bytes.Join(writer2.bytes, []byte{}))

	writer1, writer2 = testMorphingWriter(require, [][]byte{{}, {}})
	require.EqualValues([]byte{}, bytes.Join(writer1.bytes, []byte{}))
	require.EqualValues([]byte{}, bytes.Join(writer2.bytes, []byte{}))
}

func testMorphingWriter(require *require.Assertions, writes [][]byte) (*sliceRecorderWriter, *sliceRecorderWriter) {
	var writer1 sliceRecorderWriter
	var writer2 sliceRecorderWriter
	morph := func(firstToken []byte) io.Writer {
		if len(firstToken) > 1 && firstToken[0] == byte('a') {
			return &writer1
		}
		return &writer2
	}
	writer := NewMorphingWriter('\n', morph)
	for _, bytes := range writes {
		_, err := writer.Write(bytes)
		require.NoError(err)
	}

	err := writer.Close()
	require.NoError(err)
	return &writer1, &writer2
}

func TestOutputWriter(t *testing.T) {
	require := require.New(t)
	var output string
	var err error

	output = testOutputWriter(require, "")
	require.Equal("", output)

	output = testOutputWriter(require, "asdf\n")
	var m map[string]interface{}
	err = json.Unmarshal([]byte(output), &m)
	require.NoError(err)
	require.Contains(m, "line")
	require.Equal("asdf", m["line"])

	output = testOutputWriter(require, "asdf\nqwer")
	var buffer bytes.Buffer
	_, err = buffer.WriteString(output)
	require.NoError(err)
	scanner := bufio.NewScanner(&buffer)
	var maps []map[string]interface{}
	for scanner.Scan() {
		var m map[string]interface{}
		b := scanner.Bytes()
		err = json.Unmarshal(b, &m)
		require.NoError(err)
		maps = append(maps, m)
	}
	require.NoError(scanner.Err())
	require.Equal(2, len(maps))
	require.Contains(maps[0], "line")
	require.Contains(maps[1], "line")
	require.Equal("asdf", maps[0]["line"])
	require.Equal("qwer", maps[1]["line"])
}

func testOutputWriter(require *require.Assertions, input string) string {
	// will either pass through or log to here
	downstream := bytes.Buffer{}
	handler := slog.NewJSONHandler(&downstream, nil)
	logger := slog.New(handler)
	wrapper := CreateLogWrapper(logger, 0, "stdout")
	writer := CreateOutputWriter(wrapper, &downstream)
	_, err := writer.Write([]byte(input))
	require.NoError(err)
	err = writer.Close()
	require.NoError(err)
	return downstream.String()
}
