package server

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestServe(t *testing.T) {
	require := require.New(t)
	dur, err := time.ParseDuration(("10s"))
	require.NoError(err)
	ctx, cancelTimeoutCtx := context.WithTimeout(context.Background(), dur)
	defer cancelTimeoutCtx()
	group, ctx := errgroup.WithContext(ctx)

	numClients := 100

	wg1 := new(sync.WaitGroup)
	wg1.Add(numClients)

	wg2 := new(sync.WaitGroup)
	wg2.Add(1)

	// start the echo server
	listener, err := TCPListener(8080)
	require.NoError(err)
	defer listener.Close()
	removeAfterFunc := context.AfterFunc(ctx, func() {
		// stops the listener and unblocks calls to Accept
		listener.Close()
	})
	defer removeAfterFunc()

	group.Go(func() error {
		defer wg2.Done()
		return Accept(ctx, listener, LoggingErrorHandler)
	})

	for i := 0; i < numClients; i++ {
		i := i
		group.Go(func() error {
			defer wg1.Done()
			return client(ctx, i)
		})
	}

	wg1.Wait()
	require.NoError(ctx.Err())
	listener.Close()
	wg2.Wait()
}

func client(ctx context.Context, clientIdx int) error {
	conn, err := net.Dial("tcp", ":8080")
	if err != nil {
		slog.Error("dial", "error", err)
		return err
	}
	defer conn.Close()
	// close the connection when the context ends to unblock any blocking calls
	// (i.e., Read or Write)
	defer context.AfterFunc(ctx, func() {
		conn.Close()
	})()

	s := fmt.Sprintf("client %v\n", clientIdx)
	b := []byte(s)
	reader := bufio.NewReader(conn)

	for i := 0; i < 100; i++ {
		n, err := conn.Write(b)
		if err != nil {
			slog.Error("client Write", "error", err)
			return fmt.Errorf("client Write: %w", err)
		}
		if n != len(b) {
			err = fmt.Errorf("client Write failed to write entire message: %v", n)
			slog.Error("failed to write entire message", "error", err)
			return err
		}

		readString, err := reader.ReadString('\n')
		// we don't expect EOF.  Treat it as an error
		if err != nil {
			err = fmt.Errorf("client ReadString: %w", err)
			slog.Error("client ReadString", "error", err)
			return err
		}
		if readString != s {
			err = fmt.Errorf(
				"client ReadString expected to read message: %v. received: %v",
				s,
				readString)
			slog.Error("client ReadString bad message", "error", err)
			return err
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
// SOLUTION: retry, potentially with jittered sleeps, on the first read.
// the server should respond with a fixed message first allowing the client
// to test the connection.
// barrier/wait group on all clients connecting, which may mean that we need
// to add a keepalive via net.ListenConfig

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

	numClients := 100
	wg1 := new(sync.WaitGroup)
	wg1.Add(numClients)

	for i := 0; i < numClients; i++ {
		i := i
		group.Go(func() error {
			defer wg1.Done()
			return client(ctx, i)
		})
	}

	wg1.Wait()
	require.NoError(ctx.Err())
}
