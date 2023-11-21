package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync/atomic"
	"syscall"

	"golang.org/x/sync/semaphore"
)

var DialResponse string = "connected\n"
var DialResponseBytes []byte = []byte(DialResponse)

var DoneResponse string = "done\n"
var DoneResponseBytes []byte = []byte(DoneResponse)

func CreateTCPListener(port int) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf(":%v", port))
}

// Accept on the listener until the listener is closed.
// The provided context is passed to the handler for each each accepted connection.
// Connections that end in an error are passed to the provided errorHandler.
func Accept(
	ctx context.Context,
	listener net.Listener,
	errorHandler func(error)) error {
	// forces Accept to return when the context closes
	defer context.AfterFunc(ctx, func() {
		listener.Close()
	})()
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

// Handle a single TCP listener connection.  Echoes everything read from the
// connection back to the peer.  This will return io.EOF if the client closes
// connection.
func handleConnection(ctx context.Context, conn net.Conn) error {
	defer conn.Close()
	// if the context is closed we close the connection to unblock any
	// blocking calls to Read and Write
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

	_, err = io.Copy(conn, conn)
	return err
}

// An Accept error handler to log the error.
func LoggingErrorHandler(err error) {
	slog.Error("handleConnection", "error", err)
}

// Write the provided message to the provided writer.
// Returns the number of bytes written and error or nil.
func WriteBytes(message []byte, writer *bufio.Writer) (
	numberBytes int, err error) {
	n, err := writer.Write(message)
	if err != nil {
		return n, fmt.Errorf("WriteBytes Write: %w", err)
	}
	if n != len(message) {
		return n, fmt.Errorf("WriteBytes Write failed to write entire message: %v", n)
	}
	err = writer.Flush()
	if err != nil {
		return n, fmt.Errorf("WriteBytes Flush: %w", err)
	}
	return n, nil
}

// Read from the provided bufio.Reader until a newline character.
// Returns whether the underlying reader is done, a []byte of the read bytes,
// and an error or nil
func ReadBytes(reader *bufio.Reader) (
	done bool, readBytes []byte, err error) {
	readBytes, err = reader.ReadBytes('\n')
	if err != nil {
		if err == io.EOF && len(readBytes) == 0 {
			return true, readBytes, nil
		} else {
			return true, readBytes, fmt.Errorf(
				"ReadBytes. readBytes: '%v': %w",
				readBytes,
				err)
		}
	}
	return false, readBytes, nil
}

// Dial on the provided network and address.  Returns a valid connection and
// nil, or nil and an error.
func Dial(
	ctx context.Context,
	network string,
	address string) (net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	return conn, nil
}

// Dial and test the new connection by reading until newline.  It expects to
// receive DialResponse.  Dialing is performed while holding sem to avoid a SYN
// flood.
func DialAndTest(
	ctx context.Context,
	sem *semaphore.Weighted,
	network string,
	address string,
	connResetCount *atomic.Int64) (
	net.Conn, error) {
	// dial and read in a loop.  Retry on ECONNRESET, which may occur when
	// the server establishes connections faster than it can Accept() them.
	err := sem.Acquire(ctx, 1)
	if err != nil {
		return nil, fmt.Errorf("acquire semaphore: %w", err)
	}
	defer sem.Release(1)

	for {
		conn, err := Dial(ctx, network, address)
		if err != nil {
			return nil, err
		}

		connOk, err := testNewConn(ctx, conn)
		if connOk {
			return conn, nil
		} else if err == nil {
			conn.Close()
			connResetCount.Add(1)
			continue
		} else {
			conn.Close()
			return nil, fmt.Errorf("testNewConn after dial: %w", err)
		}
	}
}

// test the new conn.  If connOk use the conn.  If !connOk and err == nil retry.
// Otherwise if err != nil abort.  Treats syscall.ECONNRESET as a retryable error.
// Does not close the connection
func testNewConn(ctx context.Context, conn net.Conn) (connOk bool, err error) {
	// close the connection, unblocking any Read(), if the context ends
	// defer "unregister" this AfterFunc
	defer context.AfterFunc(ctx, func() {
		conn.Close()
	})()

	reader := bufio.NewReader(conn)
	readString, err := reader.ReadString('\n')
	// we don't expect EOF.  Treat it as an error
	if errors.Is(err, syscall.ECONNRESET) {
		// retry Dial and read
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("client dial ReadString: %w", err)
	}

	if readString != DialResponse {
		return false, fmt.Errorf("client dial ReadString unexpected response: %v", readString)
	}

	return true, nil
}

// Run a single client.
// ctx - returns if the client completes.
// iterations - number of round trips the client performs.
// sem - semaphore to acquire to dial.
// message - message to send for each round trip
// connResetCount - atomic counter to increment for each syscall.ECONNRESET
func RunClient(
	ctx context.Context,
	iterations int,
	sem *semaphore.Weighted,
	message string,
	connResetCount *atomic.Int64) error {
	conn, err := DialAndTest(ctx, sem, "tcp", ":8080", connResetCount)
	if err != nil {
		return fmt.Errorf("client dialAndTest: %w", err)
	}
	defer conn.Close()

	// close the connection when the context ends to unblock any blocking calls
	// (i.e., Read or Write)
	defer context.AfterFunc(ctx, func() {
		conn.Close()
	})()

	messageBytes := []byte(message)

	readWriter := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	for i := 0; i < iterations; i++ {
		_, err = WriteBytes(messageBytes, readWriter.Writer)
		if err != nil {
			return fmt.Errorf("client: %w", err)
		}
		done, _, err := ReadBytes(readWriter.Reader)
		if err != nil {
			return fmt.Errorf("client: %w", err)
		} else if done {
			return fmt.Errorf("client unexpected EOF")
		}
	}

	return nil
}
