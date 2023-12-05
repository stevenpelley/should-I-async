package server

import (
	"bufio"
	"context"
	"io"
	"log"
	"log/slog"
	"net"
	"sync/atomic"
	"syscall"

	"github.com/go-errors/errors"
	"golang.org/x/sync/semaphore"
)

var DialResponse string = "connected\n"
var DialResponseBytes []byte = []byte(DialResponse)

var DoneResponse string = "done\n"
var DoneResponseBytes []byte = []byte(DoneResponse)

// conditions on which we'll stop.  Note that there are always ctxKill and
// ctxTerm which we'll respond do.  The zero value provides no conditions
type StopCondition struct {
	// if 0 we will not stop based on number of iterations
	numIterations int64
}

func (sc StopCondition) panicIfInvalid() {
	if sc.numIterations < 0 {
		panic("StopCondition may not have negative numIterations")

	}
}

func NewStopConditionOnIterations(numIterations int64) StopCondition {
	return StopCondition{numIterations: numIterations}
}

type Dialer struct {
	Sem            *semaphore.Weighted
	ConnResetCount atomic.Int64
	Network        string
	Address        string
}

func (d *Dialer) GetConnResetCount() int64 {
	return d.ConnResetCount.Load()
}

// dial on the provided network and address.  Returns a valid connection and
// nil, or nil and an error.
func (d *Dialer) dial(ctx context.Context) (net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, d.Network, d.Address)
	if err != nil {
		return nil, errors.Wrap(err, 0)
	}
	return conn, nil
}

// Dial and test the new connection by reading until newline.  It expects to
// receive DialResponse.  Dialing is performed while holding sem to avoid a SYN
// flood.
func (d *Dialer) dialAndTest(ctx context.Context) (net.Conn, error) {
	// dial and read in a loop.  Retry on ECONNRESET, which may occur when
	// the server establishes connections faster than it can Accept() them.
	err := d.Sem.Acquire(ctx, 1)
	if err != nil {
		return nil, errors.Wrap(err, 0)
	}
	defer d.Sem.Release(1)

	for {
		conn, err := d.dial(ctx)
		if err != nil {
			return nil, errors.Wrap(err, 0)
		}

		connOk, err := testNewConn(ctx, conn)
		if connOk {
			return conn, nil
		} else if err == nil {
			conn.Close()
			d.ConnResetCount.Add(1)
			continue
		} else {
			conn.Close()
			return nil, errors.Wrap(err, 0)
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
		return false, errors.Wrap(err, 0)
	}

	if readString != DialResponse {
		return false, errors.Errorf("client dial ReadString unexpected response: %v", readString)
	}

	return true, nil
}

// state that controls the execution and termination of the echo client and server
// EchoArgs may be passed by value and copied
type EchoArgs struct {
	// as in SIGKILL.  Close connections and return immediately when finished
	CtxKill context.Context
	// as SIGTERM.  Attempt to gracefully finish quickly when finished.
	CtxTerm context.Context
	// describes additional conditions as to when it should stop
	StopCondition StopCondition
}

type ErrorHandler func(err error)

var LoggingErrorHandler ErrorHandler = func(err error) {
	slog.Error("handleConnection", "error", err)
}

var PanicErrorHandler ErrorHandler = func(err error) {
	log.Panic(err)
}

// Accept on the listener until the listener is closed.
// The provided context is passed to the handler for each each accepted connection.
// Connections that end in an error are passed to the provided errorHandler.
func Accept(
	echoArgs EchoArgs,
	listener net.Listener,
	errorHandler ErrorHandler) error {
	// forces Accept to return when the context closes
	defer context.AfterFunc(echoArgs.CtxKill, func() {
		listener.Close()
	})()
	defer context.AfterFunc(echoArgs.CtxTerm, func() {
		listener.Close()
	})()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return errors.Wrap(err, 0)
		}
		go func() {
			err := handleConnection(echoArgs, conn)
			if err != nil {
				errorHandler(err)
			}
		}()
	}
}

// Run a single client.
func RunClient(
	echoArgs EchoArgs,
	dialer *Dialer,
	message string) error {
	conn, err := dialer.dialAndTest(echoArgs.CtxKill)
	if err != nil {
		return errors.Wrap(err, 0)
	}
	defer conn.Close()

	// close the connection when the context ends to unblock any blocking calls
	// (i.e., Read or Write)
	defer context.AfterFunc(echoArgs.CtxKill, func() {
		conn.Close()
	})()

	return runRoundTripLoop(echoArgs, conn, []byte(message))
}

// Handle a single TCP listener connection.  Echoes everything read from the
// connection back to the peer.  This will return io.EOF if the client closes
// connection.
func handleConnection(echoArgs EchoArgs, conn net.Conn) error {
	defer func() {
		conn.Close()
	}()
	// if the context is closed we close the connection to unblock any
	// blocking calls to Read and Write
	defer context.AfterFunc(echoArgs.CtxKill, func() {
		conn.Close()
	})()

	bytesWritten, err := conn.Write(DialResponseBytes)
	if err != nil {
		return errors.Wrap(err, 0)
	}
	if bytesWritten != len(DialResponseBytes) {
		return errors.Errorf(
			"handleConnection: wrote incomplete DialResponse. bytes: %v",
			bytesWritten)
	}

	reader := bufio.NewReader(bufio.NewReader(conn))
	done, bytesRead, err := readBytes(reader)
	if err != nil {
		return errors.Wrap(err, 0)
	} else if done {
		return errors.Errorf("server unexpected EOF")
	}

	return runRoundTripLoop(echoArgs, conn, bytesRead)
}

// Write the provided message to the provided writer.
// Returns the number of bytes written and error or nil.
func writeBytes(message []byte, writer *bufio.Writer) (
	numberBytes int, err error) {
	n, err := writer.Write(message)
	if err != nil {
		return n, errors.Wrap(err, 0)
	}
	if n != len(message) {
		return n, errors.Errorf("WriteBytes Write failed to write entire message: %v", n)
	}
	err = writer.Flush()
	if err != nil {
		return n, errors.Wrap(err, 0)
	}
	return n, nil
}

// Read from the provided bufio.Reader until a newline character.
// Returns whether the underlying reader is done, a []byte of the read bytes,
// and an error or nil
func readBytes(reader *bufio.Reader) (
	done bool, readBytes []byte, err error) {
	readBytes, err = reader.ReadBytes('\n')
	if err != nil {
		if err == io.EOF && len(readBytes) == 0 {
			return true, readBytes, nil
		} else {
			return true, readBytes, errors.Errorf(
				"ReadBytes. readBytes: '%v': %w",
				readBytes,
				err)
		}
	}
	return false, readBytes, nil
}

// Run the round trip loop, writing and then listening on the provided
// connection.  The first write uses firstBytes, subsequent writes will echo the
// previous read.  The loop continues until the condition within stopCondition
// is met.  To return immediately when a context finishes have the completion of
// the context close the provided connection.  The ctxTerm acts as a stop
// condition to gracefully end the loop without closing the connection prior to
// sending EOF.
func runRoundTripLoop(
	echoArgs EchoArgs,
	conn net.Conn,
	firstBytes []byte) error {
	echoArgs.StopCondition.panicIfInvalid()
	readWriter := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	runIteration := func(bytes []byte) (done bool, read []byte, err error) {
		_, err = writeBytes(bytes, readWriter.Writer)
		if err != nil {
			return true, nil, errors.Wrap(err, 0)
		}
		done, read, err = readBytes(readWriter.Reader)
		if err != nil {
			return done, read, errors.Wrap(err, 0)
		}
		return done, read, nil
	}

	done := false
	bytes := firstBytes
	var err error

	stopOnIterations := echoArgs.StopCondition.numIterations != 0
	for i := 0; true; i++ {
		if stopOnIterations && i >= int(echoArgs.StopCondition.numIterations) {
			return nil
		}
		select {
		case <-echoArgs.CtxTerm.Done():
			return nil
		default:
		}

		done, bytes, err = runIteration(bytes)
		if err != nil {
			return err
		}
		if stopOnIterations && done {
			return errors.Errorf("unexpected EOF.  Should stop on iterations")
		}
		if done {
			return nil
		}
	}
	// shouldn't reach but compiler can't figure it out
	return nil
}
