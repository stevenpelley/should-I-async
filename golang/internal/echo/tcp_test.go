package server

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/go-errors/errors"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func createClientStopConditions(iterations int64) StopConditions {
	return StopConditions{
		NumIterations: iterations,
	}
}

func createServerStopConditions() StopConditions {
	return StopConditions{}
}

func createDialer(dialLimit int64) *Dialer {
	return &Dialer{
		Sem:     semaphore.NewWeighted(dialLimit),
		Network: "tcp",
		Address: ":8080",
	}
}

func TestServe(t *testing.T) {
	require := require.New(t)

	numClients := 500
	iterationsPerClient := int64(100)
	dialer := createDialer(100)
	var connMetricsSet ConnMetricsSet

	// create some contexts
	// timeout
	dur, err := time.ParseDuration(("10s"))
	require.NoError(err)
	ctx, cancelTimeout := context.WithTimeout(context.Background(), dur)
	defer cancelTimeout()

	// start the echo server
	listener, err := net.Listen("tcp", ":8080")
	require.NoError(err)
	defer listener.Close()
	removeAfterFunc := context.AfterFunc(ctx, func() {
		// stops the listener and unblocks calls to Accept
		listener.Close()
	})
	defer removeAfterFunc()

	serverGroup := errgroup.Group{}
	serverGroup.Go(func() error {
		return Accept(ctx, createServerStopConditions(), &connMetricsSet, listener)
	})

	clientErr := RunClients(
		ctx,
		numClients,
		createClientStopConditions(iterationsPerClient),
		dialer,
		&connMetricsSet)
	if clientErr != nil {
		clientErr = errors.Errorf("client: %w", clientErr)
	}

	slog.Info("RunClients", "ECONNRESET retry count", dialer.ConnResetCount.Load())

	listener.Close()
	serverErr := serverGroup.Wait()
	// Match assignability instead of errors.Is() because we _do_ want to see
	// any server conn handling errors.  Don't just match the first AcceptErr
	// and ignore the rest.
	_, ok := serverErr.(*AcceptErr)
	var isContextDone bool
	select {
	case <-ctx.Done():
		isContextDone = true
	default:
	}
	if ok && !isContextDone {
		// Accept returned as expected and returned an error to indicate
		// that Listener.Accept() was interrupted
		serverErr = nil
	}
	if serverErr != nil {
		serverErr = errors.Errorf("server: %w", serverErr)
	}

	joinedErr := errors.Join(clientErr, serverErr)
	if joinedErr != nil {
		require.NoError(joinedErr)
	}
	require.NoError(joinedErr)

	// times 2 because we count for both client and server
	require.Equal(2*int64(numClients)*iterationsPerClient, connMetricsSet.combined().Count)
}

// ECONNRESET occurs on MacOS because the queue of incoming TCP connection
// requests has exceeded the limit, which is listed as 5.  On MACOS this
// includes both connections still in handshaking as well as those just waiting
// to be Accept()ed.
//
// The strange thing is the error on the client comes after Dial() and calls to
// Write() return without error.  Write() makes sense as it returns once the data
// reaches the local kernel without any knowledge of the remote host.
//
// but I would have expected Dial to return an error.  This calls the connect()
// syscall under the hood and it's unclear at what point this responds.
//
// I suppose the connection could be ACKed and therefore in the ESTABLISHED state
// but not yet Accept()ed by the remote host, and there's no way for us to
// distinguish.  But it should not have been able to respond with SYN+ACK in the
// place, right?
//
// questions:
// at what point does Dial or the connect syscall return?  Must the connection be
// ESTABLISHED?
//
// if it must be ESTABLISHED then does macos use a 2 queue system and end up dropping
// the ESTABLISHED connection and closing that connection?
//
// The test below recreates the issue by not calling Accept for the listener.
// at 200 clients we observe the ECONNRESET error on the first client read.
//
// SOLUTION:
// change server to write "connected\n" for accepted connections.
// protect connection dialing with a semaphore, limiting the number of clients
// that will connect at once.
// synchronize all clients with a wait group after connecting.
// no need for keepalive since we control the server and there is no intervening NAT
// (is this true for docker virtual network?)
func TestDemoTcpAcceptQueueECONNRESET(t *testing.T) {
	require := require.New(t)

	dur, err := time.ParseDuration(("5s"))
	require.NoError(err)
	ctx, cancelTimeoutCtx := context.WithTimeout(context.Background(), dur)
	defer cancelTimeoutCtx()
	group, ctx := errgroup.WithContext(ctx)

	listener, err := net.Listen("tcp", ":8080")
	require.NoError(err)
	defer listener.Close()

	// 256 fails.  Clearly the queue is 256 large
	numClients := 257
	wg1 := new(sync.WaitGroup)
	wg1.Add(numClients)

	// never block dialing
	dialer := createDialer(int64(numClients))

	for i := 0; i < numClients; i++ {
		group.Go(func() error {
			defer wg1.Done()
			conn, err := dialer.dial(ctx)
			if err != nil {
				return err
			}
			defer conn.Close()
			defer context.AfterFunc(ctx, func() {
				conn.Close()
			})()

			reader := bufio.NewReader(conn)
			_, _, err = readBytes(reader)
			return err
		})
	}

	wg1.Wait()
	require.Error(ctx.Err())
	require.True(errors.Is(context.Cause(ctx), syscall.ECONNRESET))
}

// more fun:
// still seeing:
// 2023/11/12 20:50:59 ERROR handleConnection error="handleConnection io.Copy. bytesCopied: 1100: readfrom tcp 127.0.0.1:8080->127.0.0.1:61376: read tcp 127.0.0.1:8080->127.0.0.1:61376: use of closed network connection"
//
// I suspect that what's happening here is that the client sends its last data
// which only gets it to the kernel buffer but doesn't guarantee sending it.  It then
// closes the connection which starts the 4-way handshake, starting with a FIN packet.
// Is it possible that FIN arrives before EOF?
// appears to always be 1100 bytes copied at the point of the error.  This with 100 send/receives
// this would make sense for "clientXXX\n" being 11 bytes.
// with 101 iterations it says 1111 bytes, so this makes sense.  The error is always
// at the end.
//
// This error happens when the connection is closed by that side.
// see https://github.com/golang/go/issues/4373
// in this case I had passed the errgroup context to the server side even though
// those goroutines were not started via errgroup.Go.  As a result, when the test
// runner errgroup.Wait returned it canceled the context (as per its doc), which in
// turn closed the server side connection.  This is a race.  If the connection is closed
// before the server side is able to Read() EOF it will return an error.
//
// interestingly, this error is internal/not exported and so you can't query for it
// using errors.Is().  Russel cox (one of the guys who wrote golang) chimed in and
// concluded that since this can only happen when _your program_ closes the connection
// you aught to be aware of it (for example by examining the context the connection
// is tied to) and any other cause is a bug.  This certainty as to what causes the error
// helped me identify that I had a race.
func NoTestDemoSharedContextBetweenServerAndClient(t *testing.T) {
	// not provided and difficult to reproduce deterministically.
	// I believe that this actually requires some load so that the server connection
	// remains open and is not gracefully closed by a 4-way handshake before
	// all clients finish, the context is ended, the server's connection is closed
	// by AfterFunc, and the server attempts to read.
	require := require.New(t)

	dur, err := time.ParseDuration(("5s"))
	require.NoError(err)
	ctxTimeout, cancelTimeoutCtx := context.WithTimeout(context.Background(), dur)
	defer cancelTimeoutCtx()
	group, ctx := errgroup.WithContext(ctxTimeout)

	listener, err := net.Listen("tcp", ":8080")
	require.NoError(err)
	defer listener.Close()
	defer context.AfterFunc(ctx, func() {
		listener.Close()
	})()

	ch1 := make(chan struct{})
	chanErr := make(chan error)

	// go instead of group.Go!
	go func() error {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		defer conn.Close()
		// this is what closes the connection!  Only the client uses the waitgroup
		// and so when it finishes we'll close our connection here
		defer context.AfterFunc(ctx, func() {
			conn.Close()
		})

		// block on client closing
		select {
		case <-ctxTimeout.Done():
			return fmt.Errorf("server timeout on client closing: %w", ctxTimeout.Err())
		case <-ch1:
		}

		reader := bufio.NewReader(conn)
		_, _, err = readBytes(reader)
		if err == nil {
			err = errors.New("server: expected error but found nil")
		}
		chanErr <- err

		return nil
	}()

	// Client dials, writes anything, and closes
	// this _DOES_ use waitgroup.Go and so when it exits closes the associated ctx
	group.Go(func() error {
		conn, err := createDialer(1).dial(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		// write something, anything, so that the server has something to read
		// but its connection will close.  I believe what's happening is that even
		// if we close its connection and the read buffer is empty it gracefully
		// returns EOF
		bytes := []byte("asdf")
		bytesWritten, err := conn.Write(bytes)
		if err != nil {
			return err
		}
		if bytesWritten != len(bytes) {
			return err
		}
		return nil
	})

	require.NoError(group.Wait())
	// prove the context is done
	require.Error(ctx.Err())
	// now that we know the context is done we signal the server
	close(ch1)

	select {
	case err := <-chanErr:
		require.ErrorIs(err, syscall.ECONNRESET)
	case <-ctxTimeout.Done():
		t.Errorf("timeout prior to chanErr: %v", ctx.Err())
	}
}

// yet more fun:
// seeing ECONNRESET from server side only periodically with the new code.
// Caused because the client code was swapped from a loop of write then read,
// to a write, then a loop of "echo" (read then write).
// the key here is that the last action of the client previously was a read
// and now is a write.  A write followed by closing the connection apparently does not
// guarantee that the write reaches the peer before closing the connection, so the peer
// may send a message, including an ACK, to the client, which is responded to with RST.
//
// here's an amazingly direct explanation and demo of what's happening:
// https://cs.baylor.edu/~donahoo/practical/CSockets/TCPRST.pdf
// calling close() on a tcp connection while there are bytes in the receive queue
// causes the peer, on read, to get ECONNRESET.  In our case the client called Write,
// the server echoed it back, and then the client Close()ed the connection.
// Their solution is to "shutdown" before close.
//
// rsc declined a request to add this to TCPConnection
// https://groups.google.com/g/golang-dev/c/cq-Y0vDXdwg
//
// I assume the conclusion is that whoever closes the connection should do so after
// a read, not a write.  The conclusion may also be to always assume RST and handle
// gracefully.
//
// NOTE: I'm sometimes seeing a race here where the server's ReadBytes() gets done=true
// and no error.
func TestDemoCloseWithReadsAvailableECONNRESET(t *testing.T) {
	require := require.New(t)

	dur, err := time.ParseDuration(("5s"))
	require.NoError(err)
	ctx, cancelTimeoutCtx := context.WithTimeout(context.Background(), dur)
	defer cancelTimeoutCtx()
	group, ctx := errgroup.WithContext(ctx)

	listener, err := net.Listen("tcp", ":8080")
	require.NoError(err)
	defer listener.Close()
	defer context.AfterFunc(ctx, func() {
		listener.Close()
	})()

	ch1 := make(chan struct{})
	ch2 := make(chan struct{})
	chanErr := make(chan error)

	group.Go(func() error {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		defer conn.Close()
		defer context.AfterFunc(ctx, func() {
			conn.Close()
		})

		bytes := []byte("asdf")
		bytesWritten, err := conn.Write(bytes)
		if err != nil {
			return err
		}
		if bytesWritten != len(bytes) {
			return err
		}

		// signal to client that we are done writing
		close(ch1)

		// block on client closing
		select {
		case <-ctx.Done():
			return fmt.Errorf("server select on client closing: %w", ctx.Err())
		case <-ch2:
		}

		// Read, pass err to assert ECONNRESET
		reader := bufio.NewReader(conn)
		done, readBytes, err := readBytes(reader)
		if err == nil {
			slog.Error("expected server err on readBytes", "done", done, "readBytes", readBytes, "err", err)
		}
		chanErr <- err

		return nil
	})

	// Client dials (no test)
	group.Go(func() error {
		conn, err := createDialer(1).dial(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()

		// wait for the server to write
		select {
		case <-ctx.Done():
			return fmt.Errorf("client select for server write: %w", ctx.Err())
		case <-ch1:
		}

		conn.Close()

		// tell server that we closed
		close(ch2)

		return nil
	})

	select {
	case err := <-chanErr:
		require.ErrorIs(err, syscall.ECONNRESET)
	case <-ctx.Done():
		t.Errorf("context done prior to chanErr: %v", ctx.Err())
	}
}

func TestIsErrorRetriable(t *testing.T) {
	require := require.New(t)
	err1 := errors.Wrap(syscall.ECONNRESET, 0)
	err2 := &firstIterationErr{cause: err1}
	require.ErrorIs(err2, syscall.ECONNRESET)
	var fiErr *firstIterationErr
	require.ErrorAs(err2, &fiErr)
}
