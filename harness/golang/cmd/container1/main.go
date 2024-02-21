package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"

	"github.com/go-errors/errors"
	"github.com/stevenpelley/should-I-async/harness/golang/internal/eventloop"
	"github.com/stevenpelley/should-I-async/harness/golang/internal/harnessio"
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

func realMain() error {
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

The program will end immediately on any error prior to starting the first command.
if there is an error starting any command all started commands will be
SIGTERMed and all processes will be joined.
if all command start successfully it will run until they all terminate.
a SIGTERM to this process will SIGTERM all running processes in turn.
Therefore if any of the commands run indefinitely you are expected to SIGTERM
this process eventually

The output is stdout json logging of the execution trace.  This program's output
is interleaved with log lines for the stdout and stderr of the managed
processes' output, one log line per line of their output.  This is all in a
single flattened log for ease of use with docker, where stdin and stdout are
readily available but files require mounting.

Logging will contain attributes "app": "container1" to name log lines for this
process, and "trial_config": <object> to name the run/trial.
"trial_config" will take its value from environment variable TRIAL_CONFIG.  If
empty/undefined then "trial_config" will be omitted.

Useful log lines and jq queries:

error handling is logged at WARN level, and an error encountered at the
outer-most level is logged as ERROR before returning a non-zero exit code.
jq query to show all WARN/ERROR log entries
jq 'select(.level as $level | ["WARN", "ERROR"] | index($level))'

command management is logged as INFO with msg "command event".  Log
entries contain keys "event" to name the event, "command_index" to name the
command, and "pid" once started.
jq query to show keys msg, command_index, and pid for "command.*" log entries
jq 'select(.msg == "command event") | with_entries(select(.key as $key | ["event", "command_index", "pid"] | index([$key])))'

command stdout and stderr appears in log lines with message "command_output" and
keys "command_index", "name" (either stdout or stderr), and line containing a
line of text.  Newlines are omitted
jq query to reproduce stdout for command 0 (-r option is raw output, so lines will not be quoted)
jq -r 'select(.msg == "command output" and .command_index == 0 and .name == "stdout") | .line'`)
		fmt.Fprintln(flag.CommandLine.Output())
	}

	flag.Parse()

	if help {
		flag.Usage()
		os.Exit(1)
	}

	downstreamWriter := harnessio.NewLockingWriter(os.Stdout)

	var handler slog.Handler
	handler = slog.NewJSONHandler(harnessio.NewScanningWriter(downstreamWriter, bufio.ScanLines, []byte("\n")), nil)
	newAttrs := make([]slog.Attr, 1)
	newAttrs[0] = slog.String("app", "container1")

	trialConfig := os.ExpandEnv("$TRIAL_CONFIG")
	if len(trialConfig) > 0 {
		var m map[string]interface{}
		err := json.Unmarshal([]byte(trialConfig), &m)
		if err != nil {
			return errors.WrapPrefix(err, "unmarshaling TRIAL_CONFIG environment variable: ", 0)
		}
		newAttrs = append(newAttrs, slog.Any("trial_config", m))
	}

	handler = handler.WithAttrs(newAttrs)
	logger := slog.New(handler)
	slog.SetDefault(logger)

	ctx, _ := signal.NotifyContext(context.Background(), unix.SIGTERM)
	ctx, cancelFunc := context.WithCancel(ctx)
	defer cancelFunc()
	commandArgs, err := harnessio.ReadInputCommands(os.Stdin)
	if err != nil {
		return err
	}
	stdouts := make([]io.Writer, len(commandArgs))
	stderrs := make([]io.Writer, len(commandArgs))
	for i := 0; i < len(commandArgs); i++ {
		stdouts[i] = harnessio.CreateOutputWriter(
			harnessio.CreateLogWrapper(logger, i, "stdout"),
			downstreamWriter)
		stderrs[i] = harnessio.CreateOutputWriter(
			harnessio.CreateLogWrapper(logger, i, "stderr"),
			downstreamWriter)
	}
	_, err = eventloop.RunCommands(ctx, cancelFunc, nil, commandArgs, stdouts, stderrs)
	return err
}
