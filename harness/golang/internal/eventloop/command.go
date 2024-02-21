package eventloop

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"

	"github.com/go-errors/errors"
	"golang.org/x/sys/unix"
)

// run commands defined by the parameters.  This is intended to abstract away
// all the inputs for unit testing.
//
// ctx - context to pass to command creation.  Finishing terminates the commands
// cancelFunc - function expected to cancel the above ctx
// injection - injection test function called after starting commands and prior
// to joining them.
// commandArgs - exec-style strings to start commands.  Uses shell-style $VAR
// and ${VAR} environment variable substituion, and in addition will sub
// CMD1_PID with 1st commands pid for subsequent commands
// stdouts/stderrs - list of objects to connect to commands stdouts and stderrs
func RunCommands(
	ctx context.Context,
	cancelFunc context.CancelFunc,
	injection func([]*exec.Cmd),
	commandArgs [][]string,
	stdouts []io.Writer,
	stderrs []io.Writer) ([]*exec.Cmd, error) {
	// at this point if we see an error we'll still need to join all the commands.
	commandStarter := newCommandStarter(ctx, commandArgs, stdouts, stderrs)
	err := commandStarter.startCommands()
	if err != nil {
		slog.Warn("error starting commands", "err", err)
		slog.Info("terminating commands after error starting commands")
		cancelFunc()
	}
	commands := commandStarter.commands

	if injection != nil {
		injection(commands)
	}

	return commands, errors.Join(err, joinCommands(commands, cancelFunc))
}

type commandStarter struct {
	commandCtx  context.Context
	commandArgs [][]string
	stdouts     []io.Writer
	stderrs     []io.Writer

	commands []*exec.Cmd
}

// encapsulates the stdouts/stderrs writer type T and all of the inputs so that
// starting a command can indicate simply an index within all the slices.
func newCommandStarter(
	commandCtx context.Context,
	commandArgs [][]string,
	stdouts []io.Writer,
	stderrs []io.Writer) *commandStarter {
	return &commandStarter{
		commandCtx:  commandCtx,
		commandArgs: commandArgs,
		stdouts:     stdouts,
		stderrs:     stderrs,
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

	command.Stdout = cs.stdouts[idx]
	command.Stderr = cs.stderrs[idx]
	return command
}

type startingCommandError struct {
	commandIndex int
	cause        error
}

func (err startingCommandError) Error() string {
	return fmt.Sprintf("starting command %v: %v", err.commandIndex, err.cause)
}

func (err startingCommandError) Unwrap() error {
	return err.cause
}

// starts the commands.  If any error is returned we will not attempt to clean
// up and will return the list of started commands.  Entries may be nil if they
// were not yet created.  It is the caller's responsibility to end and
// join any started commands.
func (cs *commandStarter) startCommands() error {
	for idx := 0; idx < len(cs.commandArgs); idx++ {
		command := cs.createCommand(idx)
		cs.commands[idx] = command
		slog.Info("command event",
			"event", "starting",
			"path", command.Path,
			"args", command.Args,
			"command_index", idx)
		if err := command.Start(); err != nil {
			return startingCommandError{commandIndex: idx, cause: err}
		}
		slog.Info("command event",
			"event", "started",
			"path", command.Path,
			"args", command.Args,
			"command_index", idx,
			"pid", command.Process.Pid)
	}
	return nil
}

// must be called after cmd.Wait returns.  waitErr is the error returned by
// cmd.Wait.  A command exited gracefully if it produced no error (exit code 0),
// exited with 143/SIGTERM, or terminated (without exiting) from SIGTERM
func didExitGracefully(err error) bool {
	if err == nil {
		return true
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return false
	}
	if exitErr.ExitCode() == 143 { // SIGTERM code
		return true
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
	return fmt.Sprintf("command_index %v: %v", err.commandIndex, err.cause)
}

func (err commandError) Unwrap() error {
	return err.cause
}

func joinCommands(commands []*exec.Cmd, cancelFunc context.CancelFunc) error {
	type finishedCommand struct {
		idx int
		err error
	}

	// send the index and error and only write to stderr/out from the main
	// goroutine as writing files is not threadsafe
	commandDoneCh := make(chan finishedCommand)
	var startedCommnandCount int
	for idx, command := range commands {
		// skip if never attempted to start or if start failed
		if command == nil || command.Process == nil {
			continue
		}
		startedCommnandCount += 1
		command := command
		idx := idx
		go func() {
			err := command.Wait()
			commandDoneCh <- finishedCommand{idx: idx, err: err}
		}()
	}
	if startedCommnandCount == 0 {
		return nil
	}

	cancelOnceFunc := sync.OnceFunc(func() {
		slog.Info("terminating remaining commands")
		cancelFunc()
	})

	var finishedCount int
	var err error
	for {
		finishedCommand := <-commandDoneCh
		idx := finishedCommand.idx
		command := commands[idx]
		if stdout, ok := command.Stdout.(io.WriteCloser); ok {
			if myerr := stdout.Close(); myerr != nil {
				err = errors.Join(err, errors.Errorf("stdout err idx %v: %w", idx, myerr))
			}
		}
		if stderr, ok := command.Stderr.(io.WriteCloser); ok {
			if myerr := stderr.Close(); myerr != nil {
				err = errors.Join(err, errors.Errorf("stderr err idx %v: %w", idx, myerr))
			}
		}

		slog.Info("command event",
			"event", "terminated",
			"command_index", idx,
			"process state", command.ProcessState.String(),
			"pid", command.ProcessState.Pid())
		if !didExitGracefully(finishedCommand.err) {
			slog.Warn("command nongraceful exit", "command_index", idx,
				"process state", command.ProcessState.String(), "pid", command.ProcessState.Pid(),
				"err", finishedCommand.err)
			commandError := commandError{commandIndex: idx, cause: finishedCommand.err}
			err = errors.Join(err, commandError)
			// may be called multiple times.
			cancelOnceFunc()
		}
		if idx == 0 {
			cancelOnceFunc()
		}
		finishedCount += 1
		if finishedCount == startedCommnandCount {
			break
		}
	}

	return err
}
