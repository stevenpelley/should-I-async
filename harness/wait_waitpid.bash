#!/usr/bin/env bash
# this dir
WAITPID_WAITN_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )

# source this alongside wait.bash from github.com/stevenpelley/waitn
# and then call the waitn function

wait_cmd() {
    "$(realpath "$WAITPID_WAITN_DIR/waitpid")" -v -c 1 $@
}

wait_cmd_get_pid() {
    sed -n 's/^\(waitpid: could not open pid \([0-9]\+\): No such process\)\|\(PID \([0-9]\+\) finished\)$/\2\4/p'
}
