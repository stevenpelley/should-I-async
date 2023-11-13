package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"syscall"
)

var DialResponse string = "connected\n"
var DialResponseBytes []byte = []byte(DialResponse)

var DoneResponse string = "done\n"
var DoneResponseBytes []byte = []byte(DoneResponse)

func TCPListener(port int) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf(":%v", port))
}

func Accept(
	ctx context.Context,
	listener net.Listener,
	errorHandler func(error)) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go func() {
			err := handleConnection(ctx, conn)
			if err != nil {
				errorHandler(err)
			}
		}()
	}
}

// expected to return io.EOF if the client closes connection
func handleConnection(ctx context.Context, conn net.Conn) error {
	defer conn.Close()
	// if the context is closed we close the connection to unblock any
	// blocking calls (i.e., io.Copy)
	defer context.AfterFunc(ctx, func() {
		conn.Close()
	})()

	bytesWritten, err := conn.Write(DialResponseBytes)
	if err != nil {
		return fmt.Errorf("write DialResponse: %w", err)
	}
	if bytesWritten != len(DialResponseBytes) {
		return fmt.Errorf(
			"handleConnection: wrote incomplete DialResponse. bytes: %v",
			bytesWritten)
	}

	readWriter := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	var previousString *string
	for {
		readString, err := readWriter.ReadString('\n')
		if err != nil {
			if err == io.EOF && len(readString) == 0 {
				return nil
			} else {
				return fmt.Errorf(
					"handleConnection ReadString. readString: '%v', previousString: '%v': %w",
					readString,
					*previousString,
					err)
			}
		}
		previousString = &readString

		b := []byte(readString)
		n, err := readWriter.Write(b)
		if err != nil {
			return fmt.Errorf("handleConnection Write: %w", err)
		}
		if n != len(b) {
			return fmt.Errorf("handleConnection Write failed to write entire message: %v", n)
		}
		err = readWriter.Flush()
		if err != nil {
			return fmt.Errorf("handleConnection Flush: %w", err)
		}
	}
}

func LoggingErrorHandler(err error) {
	var l slog.Level
	if errors.Is(err, syscall.ECONNRESET) {
		l = slog.LevelInfo
	} else {
		l = slog.LevelError
	}
	slog.Log(context.Background(), l, "handleConnection", "error", err)
}
