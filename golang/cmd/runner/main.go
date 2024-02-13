package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/go-errors/errors"
)

// utility to run as docker process 1 and coordinate subcommands.  Intended for
// use running a service to be profiled alongside profiling processes.  Fowards
// SIGTERM to all subprocesses.
// pass -h/-help or see usage below in code for more details

func main() {
	err := realMain()
	if err != nil {
		slog.Error("error", "err", err)
		os.Exit(2)
	}
}

func realMain() (err error) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	var help bool
	flag.BoolVar(&help, "help", false, "displays help")
	flag.BoolVar(&help, "h", false, "displays help")

	flag.Usage = func() {
		fmt.Fprintln(
			flag.CommandLine.Output(),

			`wrapper to run a client or server alongside processes for performance
monitoring and tracing, and forward sigterm`)
		flag.PrintDefaults()
		fmt.Fprintln(flag.CommandLine.Output())
		fmt.Fprint(
			flag.CommandLine.Output(),
			`Expects input on stdin as a json array of arrays of strings.  Each array of
strings is the exec-style arguments to start a new process/command.  Every
string will undergo shell-like variable expansion (${var} or $var) of
environment variables defined by golang's os.Expand/ExpandEnv.  In addition,
commands after the first will expand "CMD1_PID" to the pid of the first
command.

The output is stdout json logging of the execution trace, which includes the
statuses and outputs of the started commands.
If an error is encountered its information will be logged in an
ERROR line and the exit code will be non-zero
any event resulting in a returned error will be logged as WARN
as each command completes a single INFO log line with msg
"process complete" is logged.  Fields include "index" (command
index), "process state", "pid", "stdout", and "stderr"

stdout and stderr of the commands are buffered in memory.  This
will need to change to run anything with large output

The program will end immediately on any error prior to starting the first command.
if there is an error starting any command all started commands will be
SIGTERMed and all processes will be joined.
if all command start successfully it will run until they all terminate.
a SIGTERM to this process will SIGTERM all running processes in turn.
Therefore if any of the commands run indefinitely you are expected to SIGTERM
this process eventually`)
		fmt.Fprintln(flag.CommandLine.Output())
	}

	flag.Parse()

	if help {
		flag.Usage()
		os.Exit(1)
	}

	ctx, _ := signal.NotifyContext(context.Background(), unix.SIGTERM)
	ctx, cancelFunc := context.WithCancel(ctx)
	defer cancelFunc()
	commandArgs, err := readInputCommands()
	if err != nil {
		return err
	}
	_, err = runCommands(ctx, cancelFunc, commandArgs, nil)
	return err
}

// run commands defined by the parameters.  This is intended to abstract away
// all the inputs for unit testing.
func runCommands(
	ctx context.Context,
	cancelFunc context.CancelFunc,
	commandArgs [][]string,
	injection func([]*exec.Cmd)) ([]*exec.Cmd, error) {
	// at this point if we see an error we'll still need to join all the processes.
	commandStarter := NewCommandStarter(ctx, commandArgs)
	err := commandStarter.startCommands()
	if err != nil {
		slog.Info("terminating processes after error starting commands")
		cancelFunc()
	}
	commands := commandStarter.commands

	if injection != nil {
		injection(commands)
	}

	slog.Warn("error starting commands", "err", err)

	return commands, errors.Join(err, joinCommands(commands, cancelFunc))
}

func readInputCommands() ([][]string, error) {
	bytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, errors.Errorf("reading stdin: %w", err)
	}

	var commandArgs [][]string
	err = json.Unmarshal(bytes, &commandArgs)
	if err != nil {
		return nil, errors.Errorf("unmarshalling stdin json to [][]string: %w", err)
	}

	numCommands := len(commandArgs)
	if numCommands == 0 {
		return nil, errors.Errorf("json args must not be empty. Requires at least one command")
	}

	for i, args := range commandArgs {
		if len(args) == 0 {
			return nil, errors.Errorf("command %v json args must not be empty.", i)
		}
	}

	return commandArgs, nil
}

type commandStarter struct {
	commandCtx  context.Context
	commandArgs [][]string

	commands []*exec.Cmd
}

// encapsulates the stdouts/stderrs writer type T and all of the inputs so that
// starting a command can indicate simply an index within all the slices.
func NewCommandStarter(
	commandCtx context.Context,
	commandArgs [][]string) *commandStarter {
	return &commandStarter{
		commandCtx:  commandCtx,
		commandArgs: commandArgs,
		commands:    make([]*exec.Cmd, len(commandArgs)),
	}
}

func (cs *commandStarter) createCommand(idx int) *exec.Cmd {
	expandFunc := func(s string) string {
		switch s {
		case "CMD1_PID":
			if idx == 0 {
				panic("expanding CMD1_PID in first command")
			}
			return strconv.Itoa(cs.commands[0].Process.Pid)
		default:
			return os.Getenv(s)
		}
	}

	expandedArgs := make([]string, len(cs.commandArgs[idx]))
	for i, arg := range cs.commandArgs[idx] {
		expandedArgs[i] = os.Expand(arg, expandFunc)
	}
	command := exec.CommandContext(cs.commandCtx, expandedArgs[0], expandedArgs[1:]...)
	command.Cancel = func() error {
		return command.Process.Signal(unix.SIGTERM)
	}

	command.Stdout = &strings.Builder{}
	command.Stderr = &strings.Builder{}
	return command
}

type startingCommandError struct {
	commandIndex int
	cause        error
}

func (err startingCommandError) Error() string {
	return fmt.Sprintf("starting process %v: %v", err.commandIndex, err.cause)
}

func (err startingCommandError) Unwrap() error {
	return err.cause
}

// starts the commands.  If any error is returned we will not attempt to clean
// up and will return the list of started commands.  Entries may be nil if they
// were not yet created.  It is the caller's responsibility to end and
// join any started processes.
func (cs *commandStarter) startCommands() error {
	for idx := 0; idx < len(cs.commandArgs); idx++ {
		command := cs.createCommand(idx)
		cs.commands[idx] = command
		slog.Info("starting command", "path", command.Path, "args", command.Args, "index", idx)
		if err := command.Start(); err != nil {
			return startingCommandError{commandIndex: idx, cause: err}
		}
		slog.Info("started command", "path", command.Path, "args", command.Args,
			"index", idx, "pid", command.Process.Pid)
	}
	return nil
}

// must be called after cmd.Wait returns.  waitErr is the error returned by
// cmd.Wait
func didExitGracefully(err error) bool {
	if err == nil {
		return true
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return false
	}

	// "exit code" implies the process "exited," meaning it returned in its
	// code.  Terminating by signal is not exiting, and is not a
	// platform-portable concept.  So here we use the exit error/process state's
	// platform-specific "sys"
	sys := exitErr.Sys()
	waitStatus := sys.(syscall.WaitStatus)
	return waitStatus.Signal() == unix.SIGTERM
}

type commandError struct {
	commandIndex int
	cause        error
}

func (err commandError) Error() string {
	return fmt.Sprintf("command index %v: %v", err.commandIndex, err.cause)
}

func (err commandError) Unwrap() error {
	return err.cause
}

func joinCommands(commands []*exec.Cmd, cancelFunc context.CancelFunc) error {
	type finishedProcess struct {
		idx int
		err error
	}

	// send the index and error and only write to stderr/out from the main
	// goroutine as writing files is not threadsafe
	processDoneCh := make(chan finishedProcess)
	var startedProcessCount int
	for idx, command := range commands {
		// skip if never attempted to start or if start failed
		if command == nil || command.Process == nil {
			continue
		}
		startedProcessCount += 1
		command := command
		idx := idx
		go func() {
			err := command.Wait()
			processDoneCh <- finishedProcess{idx: idx, err: err}
		}()
	}
	if startedProcessCount == 0 {
		return nil
	}

	cancelOnceFunc := sync.OnceFunc(func() {
		slog.Info("terminating remaining processes")
		cancelFunc()
	})

	var finishedCount int
	var err error
	for {
		finishedProcess := <-processDoneCh
		idx := finishedProcess.idx
		command := commands[idx]
		stdout := command.Stdout.(*strings.Builder).String()
		stderr := command.Stderr.(*strings.Builder).String()
		slog.Info("process complete", "index", idx,
			"process state", command.ProcessState.String(), "pid", command.ProcessState.Pid(),
			"stdout", stdout, "stderr", stderr)
		if !didExitGracefully(finishedProcess.err) {
			slog.Warn("process nongraceful exit", "index", idx,
				"process state", command.ProcessState.String(), "pid", command.ProcessState.Pid(),
				"err", finishedProcess.err)
			commandError := commandError{commandIndex: idx, cause: finishedProcess.err}
			err = errors.Join(err, commandError)
			// may be called multiple times.
			cancelOnceFunc()
		}
		if idx == 0 {
			cancelOnceFunc()
		}
		finishedCount += 1
		if finishedCount == startedProcessCount {
			break
		}
	}

	return err
}
