# TODO

This file acts as a rough list and description of tasks to be done or ideas under discussion

## Next Steps

Goals and todos:

1. compare comparable blocking vs async implementations of yield, futex, pipe, socket.  Vary # threads/pairs, some synthetic access with size and stride on each iteration, and cpu affinity.  The goal is to keep system calls as similar as possible -- yield async has none and task reschedules itself, futex schedules corresponding task as runnable, pipe and socket add to an epoll loop that schedule corresponding ready files as runnable.  Scheduling is done on a batch basis -- run all runnable tasks, which may schedule additional tasks to the _next_ batch; when done with all tasks in batch run any intra-batch runtime code (e.g., epoll)

    i. compare performance

    ii. compare IPC -- if IPC is similar then the overhead is direct.  Can a ~50-100ns per iteration kernel mode switching cost account for difference in IPC?

    iii. examine cache and TLB miss rates.

    iv. look at simple perf stack sampling and see if the overhead attributed to schedule() in kernel accounts for performance difference.

2. if necessary use PT to examine IPC over time to reproduce graph 1 and see if we observe a decrease in IPC around context switch or syscall boundaries.

Might be fun to look at rust async for (1) but will be a huge time sync (lol).  Doing this in a true single-threaded manner requires that the executor be "bi-phasic" -- a phase for progressing futures and then a phase for polling io.  This differs from implementations I see where epoll is called from a separate dedicated thread which calls wake() for the corresponding futures or otherwise alerts the executor itself.

async:
smol looks a bit too complicated, and tokio moreso.  In particular I care that everything will run on the main thread (or the thread delegated to within a future), and that additional instructions are minimized.  Obviously I'd also like it to be simple but I'd trade off for something that is already complete and tested.
basic architecture:

- sync: each outmost future/task will be in its own thread.  Socket/pipe "read" performs the context switch.  It will likely occur inside a different future for compatibility with async, in which case it calls a blocking read() and always returns complete.
- async: a "read" is a Future that wakes when the corresponding "write" occurs, or returns as complete if it previously occurred.  It then performs the read with NOWAIT and asserts that it received its data.  This means a write must wake the reading future.  There must be an object associated with each end of each socket where the waiting read and waking write can interact.  This object can hold a flag "is async" to determine whether or not to use this wait/wake as well as to determine the syscall flags (NOWAIT).

Executor: our executor assumes no external waking.  Every future either returns complete or is awoken by the polling of some other future.  So this can simply be a circular queue with a sufficient number of entries that is initialized with the root futures.  Execution is complete with the queue is empty.  There is no need (other than debugging) to track pending futures since there must be some other pollable future that will awaken them.  What is the required size of the queue?  Is it enough to track the root futures or do we need to also store the intermediate futures for an async read?  I _think_ it's enough for the root futures as the async read futures get embedded within these.

steps:

1. hello world async unit test.  Creates a future via async, runs it by polling it.
2. simple queue-based executor unit test.  Submits a bunch of tasks, polls for each.
3. test with futures that await another future.
4. test with futures that await multiple futures.  How does this impact the queue?
5. rewrite sync sockets
6. create and test async socket
7. write async socket version
8. update main app arguments.

what do the blocking ops look like?
yield - intra-batch executor phase calls wake on all yielded futures.
futex - executor tracks woken futexes and calls wake on their futures.
fd - executor calls epoll between batches and calls wake on corresponding ready fds.

But the above adds epoll calls, which I want to avoid for a true comparison -- we _know_ which fds are ready because of the pipe/socket writes.  So in that case each future is explicitly marking which other future is ready so that it or the executor may call wake() on it for the next batch.

-------

intel pt sees roughly 2x overhead on context switches due to recording pid, tid, cpu, etc on context switches.  But LBR amazingly seems much much worse performance, even with a low sampling frequency.

intel pt:
sudo perf_5.10 record --kcore -e intel_pt// timeout 1 taskset -a -c 0 /workspaces/should-I-async/rust/csgb/target/debug/csgb yield 2
just hard-coding whatever version of perf is available so that I can run in container.  Appears to work just fine

Containerize perf.  Overwrite or shadow /usr/bin/perf with whatever specific version I have in the container.
Figure out right way to grant rights specifically to the perf container.
Get back to scripting experiments.  Start container with trial, get pid, start perf container either looking at pid or -a.
Start to script analysis (perf script, perf report, etc).

investigation:
- look into running perf record -S with pt but without collecting any samples.  How does this impact performance?
- taking a single snapshot how large can we set the buffer and how long a time period does that give?
- do we see any overhead/function calls into perf itself?  This is what killed performance when recording continuously.
- try different clock/timing modes.  Start to look at IPC over time.  We want a low error over the time period at which IPC changes.
- end result: IPC over time, either in buckets or rolling average by time, with annotated lines for key events (enter syscall, return from syscall, context switch function in kernel)
- sweep #threads to see if it causes tlb thrashing.
- move on to other workloads


adding -S for snapshot doesn't improve performance.  It still reports waking up to write data.  From the man page it's clear that the buffer will be overwritten in a fifo manner but it's not clear that it _doesn't_ also wake up to write out the data when it can.  Does -S ensure that perf.data is typically never written?

using --aux-sample needs to define some other event on which to sample.
-e intel_pt// -e cycles -F 97

sudo perf record -a --kcore -e intel_pt// -Se -vv timeout 1 taskset -a -c 0 /home/spelley/should-I-async/rust/csgb/target/debug/csgb yield 2
terrible performance, less than 50% without PT.

exaimine with:
sudo perf script --call-ret-trace -C 0

want to see where PT is enabled and disabled as well, -F BE


time 272206.836150 to 272206.836152
need ns
sudo perf script --itrace=iybxwpe -C 0 -F+flags --time 272206.836150,272206.836153 --ns
nothing out of the ordinary.  Time jumps at about 200ns at a time.

What looks like perf stuff?
do_syscall_64 : __x64_sys_sched_yield : do_sched_yield : schedule : __schedule : prepare_task_switch : __perf_event_task_sched_out
..._schedule : __switch_to_asm (x ~25?) : finish_task_switch.isra.0 : __perf_event_task_sched_in

Answer: it looks like the kernel records information for perf on every context switch if "perf_sched_events": cpu, pid, tid, time, etc.  Unclear if this is used to later fill in the instruction trace or if it is incidental.  It essentially doubles the cost of a context switch



helpful link on tracing __switch_to, which switches tasks in kernel: https://perf.wiki.kernel.org/index.php/Perf_tools_support_for_Intel%C2%AE_Processor_Trace#Example:_Tracing_switch_to.28.29
also __schedule: https://perf.wiki.kernel.org/index.php/Perf_tools_support_for_Intel%C2%AE_Processor_Trace#Example:_Tracing_schedule.28.29

PT doc is intel SDM volume 3abcd chapter 33.
trace output: 33.2.7 describes that the output buffer is described as _physical memory_ that bypasses cache and tlb (good!), but that it can only then be written after disabling packet generation (clear TraceEn), insert a memory fence, and then read.
contexts switch considerations: 33.3.5.  If doing manually (without XSAVES/XRSTORS) you must clear TraceEn before reading MSRs although there is no mention of barriers (since we aren't necessarily flushing the trace).
I think the performance issue might be one of the following:
1. packet generation is halted on every context switch to check for data to write out (and possibly write it out).  This PT barrier may be expensive.
2. on context switch it must check if it should keep recording.  If switching to a different process that isn't being recorded then it needs to be disabled.  It might always disable and re-enable.
3. there are some registers that must be saved and restored related to PT on context switch.

perf record -a ... shows a slight improvement as it no longer needs to check on a context switch if it should continue recording.


https://www.youtube.com/watch?v=KXuZi9aeGTw
google talk acknowledging that the modern cost of a context switch is the direct cost (running instructions, not impact on IPC) of the scheduler.  They discuss the google-internal implementation of futex_swap.  The "delegate" story is interesting and I don't understand completely -- detecting and responding to unexpected blocking.
futex_swap -- https://lore.kernel.org/lkml/CAFTs51XVbZ4y5NrHrcfBBb5shrQRcX4y8SAjvm76T_=EbxDiYA@mail.gmail.com/t/#u
looks like they tried to start merging this in 2020 but unclear if it ever landed.
Peter Oskolkov from Google submitted the above.  posk@google.com later posk@posk.io
tried again as User Managed Concurrency Groups, UMCG from posk, later re-implemented by Peter Zijlstra.
UMCG 1st attempt https://lwn.net/ml/linux-kernel/20210520183614.1227046-1-posk@google.com/
2nd attempt https://lore.kernel.org/lkml/20210708194638.128950-1-posk@google.com/
Peter Z reimplementation https://lore.kernel.org/all/20211214204445.665580974@infradead.org/
posk keeps it going https://lore.kernel.org/all/20220211191346.280415-1-posk@google.com/
March 2023 https://lore.kernel.org/all/20230328210754.2745989-1-posk@google.com/ "UMCG - how should we proceed? Should we?"

at the same time futex2 is happening
https://lore.kernel.org/lkml/20210603195924.361327-1-andrealmeid@collabora.com/

conclusion of all this:
if context switch overhead is direct overhead in kernel scheduling we aught to observe this from simple system profiling and flamegraph analysis.  Still useful to reproduce the IPC-time series graph and prove this.  "Proving" still requires compareing a thread-blocking implementation against a user-mode scheduler implementation.

OLD

bind mount host perf executable for the container.  On ubuntu host this is in /usr/lib/linux-tools/... and the `which perf` binary locates and execs that one.  Need to bind mount the real one, not the wrapper, and this might be different for everyone.  Hard to make it portable.
change container to --privileged
update todo

perf just doesn't mesh with containers.  Perf inherently relies on the host, its versions, and its capabilities.  To deploy perf with a container would require bundling the specific host kernel's perf utilities in the container.  This is unfortunate, because the tooling around deploying perf could still benefit from containers.
Think about a a succinct way of bundling a container with host-specific perf installed.

A no-container way of restricting to a single CPU, using a timeout, and running the test:
perf stat timeout 1 taskset -a -c 0 ./csgb yield 2

current methodology:
sudo perf record --kcore -b timeout 1 taskset -a -c 0 ../should-I-async/rust/csgb/target/debug/csgb yield 2
sudo perf script -F comm,tid,brstackinsn

Need to parse the above.  Looking specifically for:
- each jump of from:to addresses + symbols.  # cycles in preceding block, # instructions in preceding block
- Using the above as nodes create a frequency flow graph.  Filter out low frequency transitions and try to create a "dominant cycle" that shows all function calls.
- Locate on the dominant cycle important code/events, such as syscall boundaries, context switch, etc.
- look at IPC around the dominant cycle and correlate to important events.  Try to normalize to some fixed size window in cycles (which is time)
- report the distributions of cycles that each branch stack provides.

need to change the sampling frequency (recommended 97 times a second to remove aliasing effects with things that are every ms or every 10ms)

figure out how I'm going to split work in and out of container.  Perf is much easier to do outside of container but I need to be able to automate and repeat things.






### Accessing perf from inside a container
The perf utility is tied to the specific kernel.  For debian and ubuntu, /usr/bin/perf is a shell script that searches in a number of predetermined locations, e.g., /usr/bin/perf-\`uname -r\` or /usr/lib/linux-tools/\`uname -r\`.  Additionally, perf inherently requires elevated privileges to access the PMU device, perf syscalls, and to expose system-wide and kernel data.  Here are some of the challenges I've faced:

- must run docker with `--privileged` or with specific capabilities (man 7 capabilities, e.g., CAP_PERFMON)
- since perf is generally a shell script to find the correct perf binary you'll need to locate the proper binary manually and run it.
- if mounting the host filesystem to access perf it may have dependencies on shared libraries that aren't available inside the container (e.g., libcrypto.so.3)
- if packaging the correct perf binary into the container you must have a distinct image for each host kernel, somewhat defeating the purpose of containers.

A common workaround I've seen is to soft-link any inside-container perf binary to the location and name expected for a perf binary matching the host kernel.  For example, on a host running `6.8.0-11-generic` and image expecting `5.10` and which has binary `/usr/bin/perf_5.10` one might `RUN sudo ln -s /usr/bin/perf_5.10 /usr/bin/perf_6.8`.  Note that in this case the image and its `/usr/bin/perf` script expect a short major.minor utility name, not the patch-and-build and so this suffices.

This _seems_ to work, but if there are any changes to syscalls, capabilities, hardware, data formats, or anything else this might break, possibly in silent ways.  `¯\_(ツ)_/¯`

conclusion:
1. if it's trivially easy to create a container with the same kernel as host then pre-package perf
2. try to `ln -s` to whatever perf you have
3. don't containerize perf.

### Perf Misc
`/proc/sys/kernel/perf_event_paranoid` set to -1 or figure out CAP for perf

### Test Harness and Profile Tooling
Docker test harness
Any necessary scripting to facilitate a test

profile using OS level tools
profile using any language-specific tools
consider instrumented profiling (added code or PIN)

### Specific investigation
Java platform threads is ~4x faster than virtual threads.  I see that there is a
huge disparity in the number of echos by thread, suggesting that some client
threads are remaining active and never switching out, which is why it is so
fast.  Test this theory:
How many context switches occur on virtual and platform thread modes?
Profile virtual threads with JFR.  What is the overhead for scheduling?  Is
there contention?  Even if virtual threads switch it should be nearly as fast as
platform threads.
Compare these to golang.
Vary number of clients and number of CPUs

### Golang
(none)

### Java
Add tcp echo.
measure application performance
add JFR

### Dev Tooling
potentially streamline dockerfile so that devcontainer and test harness start
from same image?

### Scripting and Job Control
Wrote a golang docker harness
Continue with harness/harness.py to drive trials.  Simplify and complete it