package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
)

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

	_, err := io.Copy(conn, conn)
	return err
}

func LoggingErrorHandler(err error) {
	slog.Error("handleConnection", "error", err)
}
