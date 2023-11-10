package server

import (
	"bufio"
	"context"
	"fmt"
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
	ctx, cancel := context.WithCancel(ctx)
	group, ctx := errgroup.WithContext(ctx)

	numClients := 10

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
		return AcceptOrPanic(ctx, group, wg2, listener)
	})

	for i := 0; i < numClients; i++ {
		i := i
		c := make(chan struct{})
		group.Go(func() error {
			return client(ctx, wg1, i, c)
		})
	}

	wg1.Wait()
	require.NoError(ctx.Err())
	cancel()
	wg2.Wait()
}

func client(ctx context.Context, wg *sync.WaitGroup, clientIdx int, finished chan struct{}) error {
	defer wg.Done()

	conn, err := net.Dial("tcp", ":8080")
	if err != nil {
		return err
	}
	defer conn.Close()
	// if the test times out or any other client sees an error we'll cancel
	// all of them.  Closing the connection causes a Write or Read to return.
	removeAfterFunc := context.AfterFunc(ctx, func() {
		conn.Close()
	})
	defer removeAfterFunc()

	s := fmt.Sprintf("client %v\n", clientIdx)
	b := []byte(s)
	reader := bufio.NewReader(conn)

	for i := 0; i < 10; i++ {
		n, err := conn.Write(b)
		if err != nil {
			return err
		}
		if n != len(b) {
			return fmt.Errorf("failed to write entire message: %v", n)
		}

		readString, err := reader.ReadString('\n')
		// we don't expect EOF.  Treat it as an error
		if err != nil {
			return err
		}
		if readString != s {
			return fmt.Errorf(
				"expected to read message: %v. received: %v",
				s,
				readString)
		}
	}
	return nil
}
