package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync/atomic"
	"syscall"

	"github.com/go-errors/errors"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// Conditions on which we'll stop gracefully.  There will always be contexts
// passed for which execution will stop immediately and return with an error
// (even if just to say it was interrupted).  Stopping with any of the
// conditions here should not return an error.
type StopConditions struct {
	// if 0 we will not stop based on number of iterations
	NumIterations int64
	// stop gracefully when the channel closes
	StopCh chan struct{}
}

func (sc StopConditions) panicIfInvalid() {
	if sc.NumIterations < 0 {
		panic("StopCondition may not have negative numIterations")
	}
}

type Dialer struct {
	Sem            *semaphore.Weighted
	ConnResetCount atomic.Int64
	Network        string
	Address        string
}

// error representing an error while dialing.  These may be transient and retriable
type dialErr struct {
	cause error
}

func (f *dialErr) Error() string {
	return fmt.Sprintf("dial err: %v", f.cause)
}

func (f *dialErr) Unwrap() error {
	return f.cause
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
	conn, err := d.dial(ctx)
	if err != nil {
		return nil, &dialErr{cause: errors.Wrap(err, 0)}
	}
	return conn, nil
}

func (d *Dialer) dial(ctx context.Context) (net.Conn, error) {
	return net.Dial(d.Network, d.Address)
}

type AcceptErr struct {
	childErr error
}

func (a *AcceptErr) Error() string {
	return fmt.Sprintf("accept: %v", a.childErr)
}

// Accept on the listener until the listener is closed or the context is
// cancelled, in which case we close the listener.  A closed listener will still
// return an error, which we wrap in AcceptErr to allow matching with errors.As.
// It it up to the caller to determine how this error should be treated.
// Additionally, Accept blocks until all accepted connections are handled.  Such
// connections should stop gracefully based on the conditions of stopConditions,
// and should stop immediately and return an error when ctx finishes.
// should the call to Listener.Accept and connection handling both return errors
// they will be joined and returned.
func Accept(
	ctx context.Context,
	stopConditions StopConditions,
	listener net.Listener) (err error) {
	// forces Accept to return when the context closes
	defer context.AfterFunc(ctx, func() {
		listener.Close()
	})()

	group, ctxGroup := errgroup.WithContext(ctx)
	defer func() {
		// join any errgroup error with other returned error
		// overwrites err as a named return value to do so
		err2 := group.Wait()
		if err2 == nil {
			// keep on returning err, which might be nil
			return
		}
		if err == nil {
			err = err2
			return
		}
		// otherwise both are non-nil
		err = errors.Join(err, err2)
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			return &AcceptErr{childErr: err}
		}
		group.Go(func() error {
			err := handleConnection(ctxGroup, stopConditions, conn)
			if err != nil {
				return errors.Errorf("first server handle err: %w", err)
			}
			return nil
		})
	}
}

// Run a set of clients.  Returns when all client goroutines return which will
// happen when the provided context finishes or when one of the stopConditions
// is reached.
func RunClients(
	ctx context.Context,
	numClients int,
	stopConditions StopConditions,
	dialer *Dialer) error {

	group, ctxGroup := errgroup.WithContext(ctx)

	for i := 0; i < numClients; i++ {
		i := i
		group.Go(func() error {
			return runClientWithRetry(
				ctxGroup,
				stopConditions,
				dialer,
				fmt.Sprintf("client%v\n", i))
		})
	}
	return group.Wait()
}

// Run a single client.
func runClientWithRetry(
	ctx context.Context,
	stopConditions StopConditions,
	dialer *Dialer,
	message string) (err error) {
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		i := i
		err = runClient(ctx, stopConditions, dialer, message)
		if isErrorRetriable(err) {
			slog.Info("retrying runRoundTripLoop", "iteration", i, "err", err)
			continue
		}
		return err
	}
	return errors.Errorf("exhausted %v retries: %w", maxRetries, err)
}

func isErrorRetriable(err error) bool {
	var fiErr *firstIterationErr
	isFirstIterationErr := errors.As(err, &fiErr)
	var dErr *dialErr
	isDialErr := errors.As(err, &dErr)
	return err != nil && ((errors.Is(err, syscall.ECONNRESET) && isFirstIterationErr) ||
		(errors.Is(err, syscall.EPIPE) && isFirstIterationErr) ||
		isDialErr)
}

// Run a single client without retry.  The connection used in this method will
// be closed and must not escape this scope.
func runClient(
	ctx context.Context,
	stopConditions StopConditions,
	dialer *Dialer,
	message string) (err error) {
	conn, err := dialer.dialAndTest(ctx)
	if err != nil {
		return errors.Wrap(err, 0)
	}
	defer conn.Close()

	// close the connection when the context ends to unblock any blocking calls
	// (i.e., Read or Write)
	defer context.AfterFunc(ctx, func() {
		conn.Close()
	})()

	return runRoundTripLoop(stopConditions, conn, []byte(message))
}

// Handle a single TCP listener connection.  Echoes everything read from the
// connection back to the peer.  This will return io.EOF if the client closes
// connection.
func handleConnection(ctx context.Context, stopConditions StopConditions, conn net.Conn) error {
	defer func() {
		conn.Close()
	}()
	// if the context is closed we close the connection to unblock any
	// blocking calls to Read and Write
	defer context.AfterFunc(ctx, func() {
		conn.Close()
	})()

	reader := bufio.NewReader(bufio.NewReader(conn))
	done, bytesRead, err := readBytes(reader)
	if err != nil {
		return errors.Wrap(err, 0)
	} else if done {
		return errors.Errorf("server unexpected EOF")
	}

	return runRoundTripLoop(stopConditions, conn, bytesRead)
}

// firstIterationErr is a wrapping error used when the first iteration of
// runRoundTripLoop encounters an error using its connection.  Such an error may
// indicate a transient error condition that can be retried by first creating a
// new connection.  It appears that some tcp handshake errors, races, or rare
// states result in both sides connecting successfully but then the first use
// resulting in a RST response, at which point we get an ECONNRESET error.
// This happens under load with a high rate of packet loss due to OS network
// buffers being full, so I suspect the problem is that the handshake completes
// but then some downstream buffer is full and so when the first packet arrives
// it cannot associate it with an open connection and responds with RST.
type firstIterationErr struct {
	cause error
}

func (f *firstIterationErr) Error() string {
	return fmt.Sprintf("runRoundTripLoop first iteration: %v", f.cause)
}

func (f *firstIterationErr) Unwrap() error {
	return f.cause
}

// Run the round trip loop, writing and then listening on the provided
// connection.  The first write uses firstBytes, subsequent writes will echo the
// previous read.  The loop continues until the condition within stopCondition
// is met.  To return immediately when a context finishes have the completion of
// the context close the provided connection.  The ctxTerm acts as a stop
// condition to gracefully end the loop without closing the connection prior to
// sending EOF.
//
// runRoundTripLoop does not accept a context.  Any hard timeout or stop
// condition that should result in an error must already be set up to close conn
// or set its timeout.
// graceful stopping conditions are provided in stopConditions.
func runRoundTripLoop(
	stopConditions StopConditions,
	conn net.Conn,
	firstBytes []byte) error {
	stopConditions.panicIfInvalid()
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

	stopOnIterations := stopConditions.NumIterations != 0
	for i := 0; true; i++ {
		// check for graceful stop conditions
		if done {
			return nil
		}
		if stopOnIterations && i >= int(stopConditions.NumIterations) {
			return nil
		}
		select {
		case <-stopConditions.StopCh:
			return nil
		default:
		}

		// done will be checked at start of next iteration
		done, bytes, err = runIteration(bytes)
		if err != nil && i == 0 {
			// error on first iteration is likely related to handshake and retry
			// may succeed
			return &firstIterationErr{cause: errors.Wrap(err, 0)}
		}
		if err != nil {
			return errors.Wrap(err, 0)
		}
		if stopOnIterations && done {
			return errors.Errorf("unexpected EOF.  Should stop on iterations")
		}
	}
	// shouldn't reach but compiler can't figure it out
	return nil
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
	if err == io.EOF && len(readBytes) == 0 {
		return true, readBytes, nil
	}
	if err != nil {
		return true, readBytes, errors.Errorf(
			"ReadBytes. readBytes: '%v': %w",
			readBytes,
			err)
	}
	return false, readBytes, nil
}
