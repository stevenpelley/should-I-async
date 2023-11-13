package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

var connResetCount atomic.Int64

func TestServe(t *testing.T) {
	require := require.New(t)
	dur, err := time.ParseDuration(("10s"))
	require.NoError(err)
	ctxTimeout, cancelTimeout := context.WithTimeout(context.Background(), dur)
	defer cancelTimeout()
	ctxTest, ctxCancelFunc := context.WithCancel(ctxTimeout)
	defer ctxCancelFunc()
	group, ctxGroup := errgroup.WithContext(ctxTest)

	numClients := 100
	//sem := semaphore.NewWeighted(int64(numClients))
	sem := semaphore.NewWeighted(100)

	wg1 := new(sync.WaitGroup)
	wg1.Add(numClients)

	wg2 := new(sync.WaitGroup)
	wg2.Add(1)

	// start the echo server
	listener, err := TCPListener(8080)
	require.NoError(err)
	defer listener.Close()
	removeAfterFunc := context.AfterFunc(ctxTest, func() {
		// stops the listener and unblocks calls to Accept
		listener.Close()
	})
	defer removeAfterFunc()

	group.Go(func() error {
		defer wg2.Done()
		return Accept(ctxTest, listener, LoggingErrorHandler)
	})

	for i := 0; i < numClients; i++ {
		i := i
		group.Go(func() error {
			defer wg1.Done()
			return client(ctxGroup, sem, i)
		})
	}

	wg1.Wait()
	err = ctxGroup.Err()
	if ctxGroup.Err() != nil {
		ctxCancelFunc()
	}
	listener.Close()
	require.NoError(err)
	// if no error then give the server handlers a chance to see their connections
	// close
	wg2.Wait()

	slog.Info("connReset Count", "count", connResetCount.Load())
}

func dialAndTest(
	ctx context.Context,
	sem *semaphore.Weighted,
	network string,
	address string) (
	net.Conn, error) {
	// dial and read in a loop.  Retry on ECONNRESET, which may occur when
	// the server establishes connections faster than it can Accept() them.
	err := sem.Acquire(ctx, 1)
	if err != nil {
		return nil, fmt.Errorf("acquire semaphore: %w", err)
	}
	defer sem.Release(1)

	for {
		var dialer net.Dialer
		conn, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, fmt.Errorf("dial: %w", err)
		}

		connOk, err := testNewConn(ctx, conn)
		if connOk {
			return conn, nil
		} else if err == nil {
			conn.Close()
			(&connResetCount).Add(1)
			continue
		} else {
			conn.Close()
			return nil, fmt.Errorf("testNewConn after dial: %w", err)
		}
	}
}

// test the new conn.  If connOk use the conn.  If !connOk and err == nil retry.
// Otherwise if err != nil abort.
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

func client(ctx context.Context, sem *semaphore.Weighted, clientIdx int) error {
	conn, err := dialAndTest(ctx, sem, "tcp", ":8080")
	if err != nil {
		return fmt.Errorf("client dialAndTest: %w", err)
	}
	defer conn.Close()

	// close the connection when the context ends to unblock any blocking calls
	// (i.e., Read or Write)
	defer context.AfterFunc(ctx, func() {
		conn.Close()
	})()

	s := fmt.Sprintf("client %v\n", clientIdx)
	b := []byte(s)
	readWriter := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	for i := 0; i < 101; i++ {
		n, err := readWriter.Write(b)
		if err != nil {
			return fmt.Errorf("client Write: %w", err)
		}
		if n != len(b) {
			return fmt.Errorf("client Write failed to write entire message: %v", n)
		}
		err = readWriter.Flush()
		if err != nil {
			return fmt.Errorf("client Flush: %w", err)
		}

		readString, err := readWriter.ReadString('\n')
		// we don't expect EOF.  Treat it as an error
		if err != nil {
			return fmt.Errorf("client ReadString: %w", err)
		} else if readString != s {
			return fmt.Errorf(
				"client ReadString expected to read message: %v. received: %v",
				s,
				readString)
		}
	}

	return nil
}

// NOTES
// ECONNRESET appears, on macos to occur because the queue of incoming TCP connection
// requests has exceeded the limit, which is 5.  On MACOS this includes both connections
// still in handshaking as well as those just waiting to be Accept()ed.
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

func TestTcpBacklog(t *testing.T) {
	require := require.New(t)

	dur, err := time.ParseDuration(("5s"))
	require.NoError(err)
	ctx, cancelTimeoutCtx := context.WithTimeout(context.Background(), dur)
	defer cancelTimeoutCtx()
	group, ctx := errgroup.WithContext(ctx)

	listener, err := TCPListener(8080)
	require.NoError(err)
	defer listener.Close()

	numClients := 200
	wg1 := new(sync.WaitGroup)
	wg1.Add(numClients)

	// never block dialing
	sem := semaphore.NewWeighted(int64(numClients))

	for i := 0; i < numClients; i++ {
		i := i
		group.Go(func() error {
			defer wg1.Done()
			return client(ctx, sem, i)
		})
	}

	wg1.Wait()
	require.NoError(ctx.Err())
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
