package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/template"
	"time"

	"github.com/go-errors/errors"
)

// wrapper to run a client or server, run any processes for performance
// monitoring and tracing, and forward sigterm/sigint
//
// accept a file path that will contain a json list of list of strings, each
// the arguments to exec.
// start the secondary processes, substituting in the PID or figuring out
// env expansion.  The arguments, provided by the json doc, will include
// output file locations so we don't have to do this here.
// set up the signal handler.
// start goroutines to block on each process and then signal a "done"
// channel
//
// if first process finishes: start shutdown sequence.
// if sigterm/sigint: start shutown sequence.
// shutdown sequence:
// sigterm all processes.
// wait up to 3 seconds for all of them to finish, SIGKILL after 2

type finishedProcess struct {
	idx int
	err error
}

func main() {
	os.Exit(realMain())
}

func parseCliArgs() (ta templateArg, inputFile string, testDuration time.Duration, err error) {
	flag.StringVar(
		&inputFile,
		"input",
		"",
		"input json file providing list of list of strings as each processes' exec args")
	flag.StringVar(
		&ta.OutputDir,
		"outputDir",
		"",
		"output directory for stdout and stderr files.")
	flag.IntVar(&ta.NumClients, "numClients", 0, "number of clients when running as client")
	flag.IntVar(&ta.ServerSleepDurationMillis, "serverSleepDurationMillis", 0, "server sleep duration in millis")
	flag.DurationVar(&testDuration, "testDuration", 0, "test duration prior to sending processes SIGTERM, 0 or negative to run until interrupt")

	flag.Parse()

	if inputFile == "" {
		err = errors.Errorf("--input required\n")
		return
	}
	if ta.OutputDir == "" {
		err = errors.Errorf("--outputDir required\n")
		return
	}
	if ta.NumClients <= 0 {
		err = errors.Errorf("numClients must be positive")
		return
	}
	if ta.ServerSleepDurationMillis < 0 {
		err = errors.Errorf("serverSleepDurationMillis must be non-negative")
		return
	}
	return
}

// these will be passed to the template executor
// the first time Pid will be 0 (unset) so this must not be relied upon
// in the first command -- it is this command's pid that will be assigned here
type templateArg struct {
	OutputDir                 string
	NumClients                int
	ServerSleepDurationMillis int
	Pid                       int
}

// execute template and parse json
func templateJson(data string, templateArg templateArg) ([][]string, error) {
	var buf *bytes.Buffer = new(bytes.Buffer)
	templ, err := template.New("").Parse(data)
	if err != nil {
		return nil, errors.Errorf("templateJson parsing template: %w", err)
	}
	err = templ.Execute(buf, templateArg)
	if err != nil {
		return nil, errors.Errorf("templateJson executing template: %w", err)
	}
	processArgs := make([][]string, 0)
	err = json.Unmarshal(buf.Bytes(), &processArgs)
	if err != nil {
		return nil, errors.Errorf("templateJson unmarshalling: %w", err)
	}

	return processArgs, nil
}

func createOutputFile(outputDir string, cmdIdx int, suffix string) (*os.File, error) {
	f, err := os.Create(filepath.Join(outputDir, fmt.Sprintf("%v-%v", cmdIdx, suffix)))
	if err != nil {
		return nil, errors.Errorf("creating command output file: %v-%v: %w", cmdIdx, suffix, err)
	}
	return f, nil
}

type outputFiles struct {
	stdout *os.File
	stderr *os.File
}

func createAllOutputFiles(outputDir string, numCommands int) ([]outputFiles, error) {
	success := false
	ret := make([]outputFiles, numCommands)
	defer func() {
		if !success {
			for i := 0; i < numCommands; i++ {
				pair := ret[i]
				if pair.stdout != nil {
					pair.stdout.Close()
				}
				if pair.stderr != nil {
					pair.stderr.Close()
				}
			}
		}
	}()

	for i := 0; i < numCommands; i++ {
		stdout, err := createOutputFile(outputDir, i, "stdout")
		if err != nil {
			return nil, err
		}
		ret[i].stdout = stdout

		stderr, err := createOutputFile(outputDir, i, "stderr")
		if err != nil {
			return nil, err
		}
		ret[i].stderr = stderr
	}

	// deactivate the defered close
	success = true
	return ret, nil
}

type commandKiller struct {
	signal os.Signal
}

func (ck *commandKiller) setSignal(signal os.Signal) {
	ck.signal = signal
}

func (ck *commandKiller) kill(command *exec.Cmd) error {
	return command.Process.Signal(ck.signal)
}

func createCommand(
	processCtx context.Context,
	args []string,
	outputFilePair outputFiles,
	commandKiller *commandKiller) *exec.Cmd {
	command := exec.CommandContext(processCtx, args[0], args[1:]...)
	command.Cancel = func() error {
		return commandKiller.kill(command)
	}
	command.WaitDelay = time.Second * 2
	command.Stdout = outputFilePair.stdout
	command.Stderr = outputFilePair.stderr

	return command
}

// caller must defer callDefer() or callDefer() immediately
func setup(
	commandKiller *commandKiller) (
	commands []*exec.Cmd,
	testDuration time.Duration,
	processCancelFunc context.CancelFunc,
	callDefer func() error,
	err error) {

	returnedDefer := make([]func() error, 0)
	callDefer = func() error {
		var err error
		for _, f := range returnedDefer {
			innerErr := f()
			if innerErr != nil {
				err = errors.Join(err, innerErr)
			}
		}
		return err
	}

	ta, inputFile, td, err := parseCliArgs()
	testDuration = td
	if err != nil {
		return
	}

	inputBytes, err := os.ReadFile(inputFile)
	if err != nil {
		return
	}
	inputString := string(inputBytes)

	// templateArgs at this point do not have a PID.  We will use this only for
	// starting the first command, at which point we will set the PID and
	// execute the template again
	processArgs, err := templateJson(inputString, ta)
	if err != nil {
		return
	}

	numCommands := len(processArgs)
	if numCommands == 0 {
		err = errors.Errorf("json args must not be empty. Requires at least one command")
		return
	}

	err = os.MkdirAll(ta.OutputDir, 0770)
	if err != nil {
		err = errors.Errorf("creating output directory and parent: %w", err)
		return
	}

	outputFilePairs, err := createAllOutputFiles(ta.OutputDir, numCommands)
	if err != nil {
		return
	}
	for _, pair := range outputFilePairs {
		returnedDefer = append(returnedDefer, pair.stdout.Close)
		returnedDefer = append(returnedDefer, pair.stderr.Close)
	}

	processCtx, cancel := context.WithCancel(context.Background())
	processCancelFunc = cancel
	returnedDefer = append(returnedDefer, func() error {
		cancel()
		return nil
	})

	commands = make([]*exec.Cmd, 1)

	// start command 1 and get its pid
	commands[0] = createCommand(processCtx, processArgs[0], outputFilePairs[0], commandKiller)
	fmt.Fprintf(os.Stdout, "starting command 0. name: %v. Args: %v\n", commands[0].Path, commands[0].Args)
	err = commands[0].Start()
	if err != nil {
		err = errors.Errorf("starting process %v: %w", 0, err)
		return
	}
	firstPid := commands[0].Process.Pid

	// replace templates in the subsequent commands to pass the pid
	ta.Pid = firstPid
	processArgs, err = templateJson(inputString, ta)
	if err != nil {
		return
	}

	if numCommands != len(processArgs) {
		err = errors.Errorf(
			"number of commands changed on 2nd invocation of template. Whatever you are doing is too complicated.  Previously %v now %v",
			numCommands,
			len(processArgs))
		return
	}

	// on any error starting a process we'll still exit immediately and let the
	// caller clean up.
	for idx := 1; idx < numCommands; idx++ {
		args := processArgs[idx]
		command := createCommand(processCtx, args, outputFilePairs[idx], commandKiller)
		commands = append(commands, command)
		fmt.Fprintf(os.Stdout, "starting command %v. name; %v. Args: %v\n", idx, command.Path, command.Args)
		err = command.Start()
		if err != nil {
			err = errors.Errorf("starting process %v. You must clean up processes!: %w", idx, err)
			return
		}
	}

	return
}

// must be called after cmd.Wait returns.  waitErr is the error returned by
// cmd.Wait
func didExitGracefully(cmd *exec.Cmd, waitErr error) bool {
	// exited gracefully on no error/exit code 0 or exiting with SIGTERM
	if waitErr != nil {
		exitErr, ok := waitErr.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 143 { // 143 is SIGTERM
			return false
		}
	}
	return true
}

func eventLoop(
	commands []*exec.Cmd,
	processCancelFunc context.CancelFunc,
	testDuration time.Duration,
	commandKiller commandKiller) error {

	numCommands := len(commands)

	// send the index and error and only write to stderr/out from the main
	// goroutine as writing files is not threadsafe
	processDoneCh := make(chan finishedProcess)
	for idx, command := range commands {
		command := command
		idx := idx
		go func() {
			err := command.Wait()
			processDoneCh <- finishedProcess{idx: idx, err: err}
		}()
	}

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM)

	// initially nil.  Will be assigned when we start shutdown
	var timeoutCh <-chan struct{}
	startShutdown := func(signal os.Signal) context.CancelFunc {
		if timeoutCh != nil {
			return nil
		}
		// signal all remaining processes and start a SIGKILL timer
		// SIGKILL timer is baked into all commands based on the cancel context
		commandKiller.setSignal(signal)
		processCancelFunc()

		timeoutCtx, cancelTimeoutFunc := context.WithTimeout(context.Background(), time.Second*3)
		timeoutCh = timeoutCtx.Done()
		return cancelTimeoutFunc
	}

	testDurationContext := context.Background()
	if testDuration > 0 {
		var cancelTestDurationContext context.CancelFunc
		testDurationContext, cancelTestDurationContext = context.WithTimeout(
			context.Background(), testDuration)
		defer cancelTestDurationContext()
	}

	finishedPids := make(map[int]struct{})
	commandErrs := make([]error, numCommands)

	isTimedOut := false
	isSigInt := false
loop:
	for {
		select {
		case finishedProcess := <-processDoneCh:
			idx := finishedProcess.idx
			command := commands[idx]
			err := finishedProcess.err
			commandErrs[idx] = err
			fmt.Fprintf(os.Stdout, "process %v exit status %v\n", idx, command.ProcessState.ExitCode())
			if !didExitGracefully(command, err) {
				fmt.Fprintf(os.Stderr,
					"process %v nongraceful exit. exit status %v. err: %v\n",
					idx, command.ProcessState.ExitCode(), err)
			}
			finishedPids[command.Process.Pid] = struct{}{}
			if len(finishedPids) == numCommands {
				break loop
			}

			// primary command terminated
			if idx == 0 {
				cancelTimeout := startShutdown(syscall.SIGTERM)
				if cancelTimeout != nil {
					defer cancelTimeout()
				}
			}

		case sig := <-signalCh:
			fmt.Fprintf(os.Stdout, "received signal %v\n", sig)
			isSigInt = isSigInt || sig == os.Interrupt
			cancelTimeout := startShutdown(sig)
			if cancelTimeout != nil {
				defer cancelTimeout()
			}

		case <-testDurationContext.Done():
			cancelTimeout := startShutdown(syscall.SIGTERM)
			if cancelTimeout != nil {
				defer cancelTimeout()
			}

		case <-timeoutCh:
			isTimedOut = true
			break loop
		}
	}

	// if we timed out waiting for children to return always end in error
	if isTimedOut {
		return errors.Errorf("timed out waiting for processes to finish")
	}

	// if any child ended in error we end in error
	for idx, cmd := range commands {
		err := commandErrs[idx]
		if !didExitGracefully(cmd, err) {
			status := cmd.ProcessState.ExitCode()
			return errors.Errorf("command failed.  Idx %v exit code %v: %w", idx, status, err)
		}
	}

	// if we were interrupted but all children somehow succeeded we should
	// provide a failure exit code
	if isSigInt {
		return errors.Errorf("interrupted, all processes succeeded")
	}

	return nil
}

func realMain() int {
	var commandKiller commandKiller = commandKiller{signal: syscall.SIGTERM}
	commands, testDuration, processCancelFunc, callDefer, err := setup(&commandKiller)
	// needs to be called even if there is an error
	defer callDefer()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	// after this point we do not exit immediately on errors
	err = eventLoop(commands, processCancelFunc, testDuration, commandKiller)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	return 0
}
