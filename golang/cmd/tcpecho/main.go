package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	echo "github.com/stevenpelley/should-I-async/golang/internal/echo"
	"golang.org/x/sync/semaphore"
)

// top level flags
var (
	network       string
	address       string
	trialDuration time.Duration
)

// client flags
var (
	numClients int
)

// server flags
var (
	sleepDuration time.Duration
)

func main() {
	flag.StringVar(&network, "network", "tcp", "network from net package (e.g., tcp, udp, unix)")
	flag.StringVar(&address, "address", ":8080", "network address, ip and port")
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

	// set up contexts and stop conditions.
	ctx, ctxCancelFunc := context.WithCancel(context.Background())
	defer ctxCancelFunc()
	gracefulStopCh := make(chan struct{})
	stopConditions := echo.StopConditions{StopCh: gracefulStopCh}

	// connect SIGINT and SIGTERM to gracefulStopCh.
	// and 2s after gracefulStopCh is set we will finish ctx
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM)
	finishedCh := make(chan struct{})

	go func() {
		select {
		case <-finishedCh:
			return
		case <-signalCh:
		}
		close(gracefulStopCh)
		timeoutCtx, cancelTimeout := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelTimeout()
		select {
		case <-finishedCh:
			return
		case <-timeoutCtx.Done():
		}

		fmt.Fprint(os.Stderr, "hang after signal detected.  Producing stacks to debug")
		debug.SetTraceback("all")
		panic("hang after signal detected")
	}()

	var cm echo.ConnMetricsSet

	flagSet := flag.NewFlagSet(cmd, flag.ExitOnError)
	switch cmd {
	case "client":
		flagSet.IntVar(&numClients, "numClients", 0, "number of echo clients.  Required, must be positive")
		_ = flagSet.Parse(args)
		if numClients <= 0 {
			fmt.Fprintln(os.Stderr, "numClients required and must be positive")
			os.Exit(2)
		}
		sem := semaphore.NewWeighted(100)
		dialer := &echo.Dialer{Sem: sem, Network: network, Address: address}
		err = echo.RunClients(ctx, numClients, stopConditions, dialer, &cm)
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
		defer listener.Close()

		// close the listener on graceful stop
		go func() {
			select {
			case <-finishedCh:
				return
			case <-gracefulStopCh:
				listener.Close()
			}
		}()

		echo.Accept(ctx, stopConditions, &cm, listener)
	default:
		log.Fatalf("Unrecognized command %q.  Command must be one of: client, server", cmd)
	}

	// turn off the watchdog timer
	close(finishedCh)

	combined, err := cm.Combined()
	if err != nil {
		panic(err)
	}
	fmt.Println(combined)
}
