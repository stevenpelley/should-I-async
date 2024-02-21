# run the test harness.

import concurrent.futures
import json
import os
import signal
import subprocess
import threading
import time
import traceback


# TODO: this got more complicated than it needs to be:
# as a driver I don't think we need to catch signals.  Or we can add it later.
# running the server and client needs only 1 async job.  Run the server async, run
# the client blocking with a timeout, and then stop the server.  This can be done by
# running the server detached and then stopping with "docker stop" or by running
# attached and then terming the pid.  Doing this removes any need for futures.
# Clean up the variable management.  There are lots of paths and options and this should
# be the primary complexity, but no more.


# this is a driver to run trials, including:
# - catching signals so that we can terminate gracefully, cleaning up processes
# and resources
# - running bg processes simultaneously and connecting them to each other,
# managing their output and status codes.
# - running with various configurations to sweep a parameter space or identify a
# parameter set based on conditions.


# implementation note:
# the main thread sets a signal handler and then should only wait on futures.
# Signals do not cause syscall-like functions, even from the os module, to
# return immediately.  All concurrent tasks must be cancellable by externally
# triggering any waited condition (e.g., setting a threading.Event, killing a
# process being waited on).
# Futures are used as it is easy to wait for "all" or "first"


# I add a nonstandard attribute _harness_label to futures so that they may be
# labelled with a task identifier
class BgProcess(object):
    _p = None
    _future = None
    _out_file = None

    def __init__(self, output_path, executor, future_label, popen_args, popen_kwargs={}):
        self._out_file = open(output_path, "w")
        self._p = subprocess.Popen(
            *popen_args, **popen_kwargs, stderr=subprocess.STDOUT, stdout=self._out_file)

        def task():
            result = self._p.wait()
            print("subprocess future complete {}: {}".format(future_label, result))
            return result

        self._future = executor.submit(task)
        self._future.add_done_callback(lambda: self._out_file.close())
        self._future._harness_label = future_label

    def future(self):
        return self._future

    def close(self):
        self._p.terminate()


def host_templates_path():
    return "./templates"


def host_output_path():
    pass


CONTAINER_WORKSPACE_PATH = "/workspace"


def container_volume_path():
    return os.path.join(CONTAINER_WORKSPACE_PATH, "volume")


def container_templates_path():
    return os.path.join(container_volume_path(), "templates")


def container_output_path():
    return os.path.join(container_volume_path(), "volume", "output")


class Trial(object):
    _driver_name = None
    _num_clients = None
    _server_sleep_duration_millis = None
    _trial_name = None
    _test_duration_str = None

    def __init__(self, driver_name, num_clients, server_sleep_duration_millis, test_duration_str):
        self._driver_name = driver_name
        self._num_clients = num_clients
        self._server_sleep_duration_millis = server_sleep_duration_millis
        self._test_duration_str = test_duration_str
        self._trial_name = "numClients-{}_serverSleepDurationMillis-{}_driver-{}".format(
            self._num_clients, self._server_sleep_duration_millis, self._driver_name)

    def _create_template_name(self, mode):
        return "driver-{}_mode-{}".format(self._driver_name, mode)

    def create_container_name(self, mode):
        return "{}_mode-{}".format(self._trial_name, mode)

    def _create_args(self, mode):
        """Mode is either "client" or "server"
        """
        template_name = self._create_template_name(mode)
        container_name = self.create_container_name(mode)
        return [
            "docker",
            "run",
            "--name", "{}".format(container_name),
            "--mount", "type=volume,source=harness_vol,destination={}".format(
                container_volume_path()),
            "--mount", "type=bind,source={},destination={},readonly".format(
                host_templates_path(), container_templates_path()),
            "harness:1",
            "--input", "{}.json".format(os.path.join(
                container_templates_path(), template_name)),
            "--outputDir", "{}".format(os.path.join(
                container_output_path(), self._trial_name, mode)),
            "--numClients", "{}".format(self._num_clients),
            "--serverSleepDurationMillis", "{}".format(
                self._server_sleep_duration_millis),
        ]

    def _create_server_args(self):
        return self._create_args("server")

    def _create_client_args(self):
        args = self._create_args("client")
        args.extend(["--testDuration", self._test_duration_str])
        return args

    def run(self, signal_manager, executor, process_output_dir):
        os.makedirs(
            os.path.join(process_output_dir, self._trial_name),
            exist_ok=True)
        # start the server
        server_p = BgProcess(
            popen_args=[self._create_server_args()],
            output_path=os.path.join(
                process_output_dir, self._trial_name, "server"),
            executor=executor,
            future_label="server")

        # wait until the server is accepting connections
        # note that we could start another docker container that tries to
        # connect in a loop.  Might be more resilient.
        time.sleep(0.5)

        # start the client
        client_p = BgProcess(
            popen_args=[self._create_client_args()],
            output_path=os.path.join(
                process_output_dir, self._trial_name, "client"),
            executor=executor,
            future_label="client")

        # wait for processes to complete, a timeout, or a signal
        server_f = server_p.future()
        client_f = client_p.future()
        signal_f = signal_manager.get_signal_future()
        fs = [server_f, client_f, signal_f]
        self._futures = {f._harness_label: f for f in fs}

        try:
            f_iter = concurrent.futures.as_completed(fs, timeout=15)
            f = next(f_iter)
            self._handle_first_completion(f)
        except TimeoutError:
            self._handle_timeout()
            pass
        finally:
            client_p.close()
            server_p.close()
            concurrent.futures.wait([server_f, client_f])

    def _handle_first_completion(self, future):
        if future.exception() is not None:
            s = "".join(traceback.format_exception(
                future.exception()))
            print("exception in {}:\n{}".format(
                future._harness_label, s))
        else:
            print("{}: {}".format(future._harness_label, future.result()))

    def _handle_timeout(self):
        print("timed out")

    def copy_docker_volume(self, destination_path):
        completed = subprocess.run([
            "docker",
            "cp",
            "{}:{}".format(self.create_container_name("server"),
                           container_output_path()),
            destination_path])
        completed.check_returncode()

    def read_client_metrics(self, destination_path):
        with open(os.path.join(
            destination_path,
            self._trial_name,
            "client",
            "0-stdout")
        ) as f:
            text = f.read()
            return json.loads(text)

    def close(self):
        completed = subprocess.run([
            "docker",
            "rm",
            self.create_container_name("server"),
            self.create_container_name("client")])
        completed.check_returncode()


def main():
    print("this pid: {}".format(os.getpid()))
    executor = concurrent.futures.ThreadPoolExecutor()
    signal_manager = SignalManager(executor)
    trial = Trial("java", 20, 0, "10s")

    try:
        trial.run(signal_manager, executor, "docker_output/")
        trial.copy_docker_volume("trial_output")
    finally:
        trial.close()
        signal_manager.close()
    d = trial.read_client_metrics("trial_output/")
    print("count: {}".format(d['count']))


if __name__ == "__main__":
    main()
