#!/usr/bin/env bash

source wait.bash
source wait_waitpid.bash

mkdir -p volume/output/
declare -A pids
# put them in associative array "pids" and assign var CMD1_PID
# the first entry is the "primary" process
source $1

exit_status=0
killed_jobs=false

# we kill from the SIGTERM handler, so here we just skip wait waking up due to
# signal.  We're return 1 to indicate this, but we don't actually use it.
wait_for_job() {
    local finished_pid
    waitn -p finished_pid "${!pids[@]}"
    local wait_ret=$?
    [ -z "$finished_pid" ] && return 1
    echo "FINISHED ${pids[$finished_pid]} exit code $wait_ret @${SECONDS}"
    unset pids[$finished_pid]

    # we want to return the nonzero status of CMD1, and otherwise the first
    # nonzero status.  Ignore any SIGTERM code if we SIGTERMed processes (either
    # because CMD1 finished or we trapped SIGTERM)
    if ( [ $exit_status -eq 0 ] || [ "$finished_pid" -eq $CMD1_PID ] ) && 
        ( ! $killed_jobs || [ $wait_ret -ne 143 ] ) &&
        [ $wait_ret -ne 0 ]; then
        exit_status=$wait_ret
    fi

    if [ "$finished_pid" -eq $CMD1_PID ] && [ ${#pids[@]} -gt 0 ]; then
        echo "primary process ended. killing jobs @${SECONDS}"
        killed_jobs=true
        kill -TERM -- "${!pids[@]}"
    fi
}

handled_term=false
term_handler() {
    handled_term=true
    echo "killing jobs from handler @${SECONDS}"
    killed_jobs=true
    kill -TERM "${!pids[@]}"
}
trap term_handler TERM

while [ ${#pids[@]} -gt 0 ]; do
    wait_for_job
done

if $handled_term; then
    # reset
    trap - TERM
    # term self after having forwarded and joining all children
    echo "terming self"
    kill -TERM -- $$
fi

echo "exiting $exit_status"
exit $exit_status