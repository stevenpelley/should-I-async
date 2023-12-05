package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	echo "github.com/stevenpelley/should-I-async/golang/internal/echo"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// top level flags
var (
	network              string
	address              string
	beforeRecordDuration time.Duration
	trialDuration        time.Duration
)

// client flags
var (
	numClients int64
)

// server flags
var (
	sleepDuration time.Duration
)

func main() {
	flag.StringVar(&network, "network", "tcp", "network from net package (e.g., tcp, udp, unix)")
	flag.StringVar(&address, "address", ":8080", "network address, ip and port")
	defaultBeforeRecordDuration, err := time.ParseDuration("2s")
	if err != nil {
		panic(err)
	}
	flag.DurationVar(
		&beforeRecordDuration,
		"beforeRecordDuration",
		defaultBeforeRecordDuration,
		"duration to run/warm up prior to starting metrics collection. Negative to never start collection")
	defaultTrialDuration, err := time.ParseDuration("30s")
	if err != nil {
		panic(err)
	}
	flag.DurationVar(
		&trialDuration,
		"trialDuration",
		defaultTrialDuration,
		"duration to run trial.  Negative to run indefinitely (until SIGTERM or SIGINT)")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		log.Fatal("Please specify a command.  Command must be one of: client, server")
	}
	cmd, args := args[0], args[1:]

	ctx, ctxCancelFunc := context.WithCancel(context.Background())
	defer ctxCancelFunc()

	flagSet := flag.NewFlagSet(cmd, flag.ExitOnError)
	switch cmd {
	case "client":
		flagSet.Int64Var(&numClients, "numClients", 0, "number of echo clients.  Required, must be positive")
		_ = flagSet.Parse(args)
		if numClients <= 0 {
			fmt.Fprintln(os.Stderr, "numClients required and must be positive")
			os.Exit(2)
		}
		group, ctxGroup := errgroup.WithContext(ctx)
		ctxKill, _ := context.WithCancel(ctxGroup)
		sem := semaphore.NewWeighted(100)
		dialer := &echo.Dialer{Sem: sem, Network: network, Address: address}
		echoArgs := echo.EchoArgs{CtxKill: ctxKill, CtxTerm: context.TODO()}
		for i := int64(0); i < numClients; i++ {
			i := i
			group.Go(func() error {
				return echo.RunClient(
					echoArgs,
					dialer,
					fmt.Sprintf("client%v\n", i))
			})
		}
		err = group.Wait()
		if err != nil {
			panic(err)
		}

	case "server":
		flagSet.DurationVar(&sleepDuration, "sleepDuration", 0, "sleep duration.  Must be non-negative (default 0)")
		_ = flagSet.Parse(args)
		if sleepDuration < 0 {
			fmt.Fprintln(os.Stderr, "sleepDuration must be non-negative")
			os.Exit(2)
		}

		listener, err := net.Listen(network, address)
		if err != nil {
			panic(err)
		}
		echoArgs := echo.EchoArgs{CtxKill: context.TODO(), CtxTerm: context.TODO()}
		echo.Accept(echoArgs, listener, echo.PanicErrorHandler)
	default:
		log.Fatalf("Unrecognized command %q.  Command must be one of: client, server", cmd)
	}
}
