package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"

	"golang.org/x/sync/errgroup"
)

func TCPListener(port int) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf(":%v", port))
}

func AcceptOrPanic(
	ctx context.Context,
	group *errgroup.Group,
	wg *sync.WaitGroup,
	listener net.Listener) error {
	defer wg.Done()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		group.Go(func() error {
			return handleConnection(ctx, conn)
		})
	}
}

// expected to return io.EOF if the client closes connection
func handleConnection(ctx context.Context, conn net.Conn) error {
	defer conn.Close()
	// if there is an error elsewhere the context will be closed and
	// a blocking call to io.Copy below will return
	removeAfterFunc := context.AfterFunc(ctx, func() {
		conn.Close()
	})
	defer removeAfterFunc()

	_, err := io.Copy(conn, conn)
	return err
}
