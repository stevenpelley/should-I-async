package eventloop

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunCommands(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	require := require.New(t)
	var err error
	var ctx context.Context
	var cancelTimeout context.CancelFunc
	var exitErr *exec.ExitError
	var commandErr commandError

	slog.Info("TEST: 1 command timeout")
	ctx, cancelTimeout = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelTimeout()
	err = oneCommand(ctx, []string{"sleep", "10"}, nil)
	require.NoError(err)

	slog.Info("TEST: 1 command exit 0")
	err = oneCommandNoTimeout([]string{"sleep", "0.01"}, nil)
	require.NoError(err)

	slog.Info("TEST: 1 command exit 1")
	err = oneCommandNoTimeout([]string{"false"}, nil)
	assertExitCode(t, err, 1)

	slog.Info("TEST: 1 command SIGTERM")
	err = oneCommandNoTimeout([]string{"sleep", "10"}, func(commands []*exec.Cmd) {
		commands[0].Process.Signal(syscall.SIGTERM)
	})
	require.NoError(err)

	slog.Info("TEST: 2 commands timeout")
	ctx, cancelTimeout = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelTimeout()
	err = twoCommands(ctx, [][]string{{"sleep", "10"}, {"sleep", "10"}}, nil)
	require.NoError(err)

	slog.Info("TEST: 2 commands. 1st exits 0")
	err = twoCommandsNoTimeout([][]string{{"sleep", "0.01"}, {"sleep", "10"}}, nil)
	require.NoError(err)

	slog.Info("TEST: 2 commands. 1st exits 1")
	err = twoCommandsNoTimeout([][]string{{"false"}, {"sleep", "10"}}, nil)
	require.ErrorAs(err, &exitErr)
	require.Equal(1, exitErr.ExitCode())
	require.ErrorAs(err, &commandErr)
	require.Equal(0, commandErr.commandIndex)

	slog.Info("TEST: 2 commands. 1st terminated")
	err = twoCommandsNoTimeout([][]string{{"sleep", "10"}, {"sleep", "10"}}, func(commands []*exec.Cmd) {
		commands[0].Process.Signal(syscall.SIGTERM)
	})
	require.NoError(err)

	slog.Info("TEST: 2 commands. 2nd exits 0")
	err = twoCommandsNoTimeout([][]string{{"sleep", "0.5"}, {"sleep", "0.01"}}, nil)
	require.NoError(err)

	slog.Info("TEST: 2 commands. 2nd exits 1")
	err = twoCommandsNoTimeout([][]string{{"sleep", "0.5"}, {"false"}}, nil)
	require.ErrorAs(err, &exitErr)
	require.Equal(1, exitErr.ExitCode())
	require.ErrorAs(err, &commandErr)
	require.Equal(1, commandErr.commandIndex)

	slog.Info("TEST: 2 commands. 2nd terminated")
	err = twoCommandsNoTimeout([][]string{{"sleep", "0.5"}, {"sleep", "10"}}, func(commands []*exec.Cmd) {
		commands[1].Process.Signal(syscall.SIGTERM)
	})
	require.NoError(err)

	ctx, cancelTimeout = context.WithTimeout(context.Background(), 1*time.Second)
	defer cancelTimeout()
	slog.Info("Test: 1 command with stdout and stderr")
	ctx, cancelFunc := context.WithCancel(ctx)
	commands, err := RunCommands(
		ctx,
		cancelFunc,
		nil,
		[][]string{{
			"bash",
			"-c",
			"echo -n \"this is stdout\"; echo -n \"this is stderr\" 1>&2"}},
		[]io.Writer{&strings.Builder{}},
		[]io.Writer{&strings.Builder{}})
	require.NoError(err)
	require.Equal("this is stdout", commands[0].Stdout.(*strings.Builder).String())
	require.Equal("this is stderr", commands[0].Stderr.(*strings.Builder).String())

	var startingCommandErr startingCommandError

	slog.Info("TEST: 1 command failure to start")
	err = oneCommandNoTimeout([]string{"asdfqwer"}, nil)
	require.ErrorAs(err, &startingCommandErr)
	require.Equal(0, startingCommandErr.commandIndex)

	slog.Info("TEST: 2 commands 2nd failure to start")
	err = twoCommandsNoTimeout([][]string{{"sleep", "0.5"}, {"asdfqwer"}}, nil)
	require.ErrorAs(err, &startingCommandErr)
	require.Equal(1, startingCommandErr.commandIndex)
}

func assertExitCode(t *testing.T, err error, expectedExitCode int) {
	require := require.New(t)
	var exitErr *exec.ExitError
	require.ErrorAs(err, &exitErr)
	require.Equal(1, exitErr.ExitCode())
}

func oneCommand(ctx context.Context, args []string, injection func([]*exec.Cmd)) error {
	ctx, cancelFunc := context.WithCancel(ctx)
	_, err := RunCommands(
		ctx,
		cancelFunc,
		injection,
		[][]string{args},
		[]io.Writer{&strings.Builder{}},
		[]io.Writer{&strings.Builder{}})
	return err
}

func oneCommandNoTimeout(args []string, injection func([]*exec.Cmd)) error {
	ctx, cancelTimeout := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancelTimeout()
	return oneCommand(ctx, args, nil)
}

func twoCommands(ctx context.Context, args [][]string, injection func([]*exec.Cmd)) error {
	ctx, cancelFunc := context.WithCancel(ctx)
	_, err := RunCommands(
		ctx,
		cancelFunc,
		injection,
		args,
		[]io.Writer{&strings.Builder{}, &strings.Builder{}},
		[]io.Writer{&strings.Builder{}, &strings.Builder{}})
	return err
}

func twoCommandsNoTimeout(args [][]string, injection func([]*exec.Cmd)) error {
	ctx, cancelTimeout := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancelTimeout()
	return twoCommands(ctx, args, nil)
}
