# TODO

This file acts as a rough list and description of tasks to be done or ideas under discussion

## Overall Vision

Goals and todos:

1. compare comparable blocking vs async implementations of socket echo.  Vary # threads/pairs, some synthetic access with size and stride on each iteration, and cpu affinity.  The goal is to keep system calls as similar as possible -- the async implementation performs no epoll, performs a read only when the corresponding write has already occurred, and we verify after the fact that no blocking/context switches are occuring.

    i. compare performance

    ii. compare IPC -- similar IPC supports direct overhead.

    iii. examine cache and TLB miss rates.

    iv. look at simple perf stack sampling and see if the overhead attributed to schedule() in kernel accounts for performance difference.

2. Use PT to examine IPC over time to reproduce graph 1 and see if we observe a decrease in IPC around context switch or syscall boundaries.

## Current task

I have a skeleton workflow for profling with pt and producing ipc of time buckets along with error bound.

1. turn off frequency scaling.  Repeat perf record
1. use perf script callstack+ret to find an interesting time range (steady state; 1-3 iterations)
1. plot ipc.  I want 3 lines indicating linear interpolation, conservative, and optimistic (these are the error bound)
1. change the ipc plot to take configurable time range, bucket size, and bucket step (allowing for overlapping buckets/rolling window)
1. add events/event ranges to the ipc plot: usermode vs kernel; cbr and psb packets; any timed packet (to interpret error bounds); syscall enter/exit (with syscall function if possible); context switch; important functions like __schedule()

# Work Log

async implementation: tokio provides everything we need.
a current thread executor will be entirely single threaded.  IO and timer polling is interleaved with future polling if necessary.  In our case we do not include IO and timer feature flags and do not enable them, so these facilities will never be polled.
We can also use a local set to constrain sets of tasks to the same thread and avoid "Send" futures.
Use async notify pairs alongside the socket endpoints to notify the read side of a socket pair after writing to the socket.  This yields a future at read if the socket hasn't been written and resumes the read future when it is written.

result: with debug build they have comperable performance.  This makes sense since without context switches there is much more to do in userspace (a surprising amount for a tokio schedule).  With release build async is about 2.5x faster than context switch, which again makes sense.
profiling is stymied by the rust standard library not have frame pointer (should be changed in 1.79 -- https://github.com/rust-lang/rust/pull/122646) and then need to use frame pointer in the build using -C force-frame-pointers=yes.
--call-graph lbr does pretty well but can't correlate kernel calls.  It at least gets the relative weighting.
Upwards of 75% of time spent in run_async_task (75% of 88% so more like 85% until I can account for the other 12%).  Of this about 40% of time spent in each of read and write syscall, 20% in tokio.

Next:
look into why lbr call graphs don't provide kernel symbols
look a bit more precisely at #threads/tasks.
try to create IPC over time (either sliding window or time buckets) graph using pt.
look at linux schedulers and their impact on context switch scheduling overhead
look at kernel mechanisms that flush caches on context switch

new command to provide more accurate timing:
sudo perf record --kcore -Se -C 0 -e intel_pt/cyc,noretcomp,mtc_period=9/ timeout 1 taskset -a -c 0 /home/spelley/should-I-async/rust/csgb/target/release/csgb socket 1
-Se is a snapshot only at the end
-C 0 is everything on cpu 0, and the taskset limits to the same core
cyc -- issue cyc data with each packet
noretcomp -- don't compress return packets.  Each return packet gets cyc data
mtc-period=9 -- coarse-grained mtc since we are using cyc.

still looking at call/ret trace with
sudo perf script --call-ret-trace -C 0

tokio notify and schedule is really expensive -- ~4us for task cycle.  Read and write syscalls each ~1us.  So user code still ~50% of time.  Perf stat suggested it's even more so this could also be some pt overhead?  Performance is really quite similar so I don't think so.  Perf stat says ~68% in user mode so this could just be accounting at the boundary.

I want to get a flame graph from a perf run.  "perf report -g" fails with weird errors.  "perf report -g --stdio" works but is missing some top level tokio calls and only accounts for up to 68%.  Even running with a full record (not -Se) which generates 800mb of trace doesn't include the top levels.
I believe the problem is that perf report only synthesizes call graphs up to 16 deep.  This can be increased with:
sudo perf report -g --stdio --itrace=ig1024xe
where --itrace=igxe is the default set of itrace options for perf report with intel-pt.  g1024 increases to the max of 1024 stack depth (overkill)
now the coverage increases to ~90%!

grr, all that I and was using a debug build

release build:
37.5% of time in user
It looks like ~10% of time is in tokio notify/wake/schedule.
I suspect that syscall_return_via_sysret is accounted as user mode?

Now onto an IPC trace.
sudo perf script --call-ret-trace -C 0 -F+ipc
gives ipc but only at call and ret boundaries, calculated since the last report.  This is quite uneven in turn of number of cycles in each sample (as low as 3, as high as 226 at a quick glance).  I need this to be more consistent.
sudo perf script -C 0 --itrace=i100us --ns
gives a trace of instruction count synthesized per time period, instruction count, or tick.
Problems:
if synchesized per time it consistently under-estimates time.  Even at 100us, which is much much larger than the clock granularity, it gives samples around every 60us.
Time is always in ns, not in cycles.  I want cycles and can't figure out how to get perf script to report it.  There's no option and I see no -F field.  The cycles field is quite coarse grained.
way forward: use i100i or some other instruction count.  Use time as a direct proxy for cycles.  Look at power events to see changes in frequency:
sudo perf script -C 0 --itrace=i100ip --ns
Separately, use the call/return trace to identify a time period of interest and the interesting times within (syscalls, syscall returns, context switch)
Anything more specific and complex will require building perf from scratch to get all of the perf script capabilities and scripts.

But let's do it!
Need a new container environment for building perf.  Can either do this in the devcontainer or create a new one.  Probably create a new one.
Checkout linux source for current kernel.  See uname -r, but then likely need to strip the end.
Get the current kernel config.  Can get from parent in /boot, might be able to get from /proc.


#### Random notes on futures, dropping, and cancellation.
I've been a bit confused regarding cancellation of futures.  It's frequently repeated in rust literature that a future is cancelled by dropping it.  A dropped future cannot be polled and thus it cannot make progress.  My confusion is this: while a dropped future cannot be polled, there is still some ongoing work and event the future is awaiting; does dropping the future cancel this work or does the work continue?  This work may consume some sort of resource (file descriptor; remote resource like a distributed lease) and so it's important to at least document what dropping the future does and does not release.
The answer so far as I can tell is "it depends."  For example, in tokio with a unix epoll reactor creating a dropping a future does nothing regarding the underlying file descriptor and syscall; those resources are managed by the TcpStream.  As soon as the TcpStream is created it is registered with the reactor and epolled for "readiness events" (readable and writeable).  The future simply polls for this event and then performs the associated non-blocking syscall to read or write.  To cancel any associated syscalls the PollEvented<TcpStream> is dropped, which calls TcpStream (or io source)::deregister.

My take: I'm sure this works well but it results in a confusing story regarding whether the "work" and "resources" are associated with the future or some other object.  Compare this to golang, where with tcp (which does not accept a context to cancel) it is clear that in order to cancel an operation you must either set the timeout to 0 or else close the stream.  It's clear that the work is associated with the stream, not with the action/method.  I'd like to see some similar delineation in rust.
My instinct is that it's a good idea to associate resources with an _object_ and not the future.  The future represents an event and value only.

#### pt vs lbr and profiling performance
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


#### resources while investigating profiled performance

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

#### research on context switch and thread scheduling overhead

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

#### Notes on running perf in containers

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


### Building and running custom perf

would like to stick this in a container.  Challenge is that git checkout for linux kernel is slow.  Can/should I mount a git repos?

```
  [ from ~/kernel]
  795  2024-04-27 18:03:32 git clone git://git.kernel.org/pub/scm/linux/kernel/git/stable/linux-stable.git
  799  2024-04-29 12:35:24 cd kernel/
  800  2024-04-29 12:35:27 cd linux-stable/
  806  2024-04-29 12:37:44 git tag -l
  807  2024-04-29 12:37:48 git checkout v6.8
  808  2024-04-29 12:38:29 sudo apt-get install build-essential flex bison
  809  2024-04-29 12:38:49 sudo apt-get install libelf-dev libnewt-dev libdw-dev libaudit-dev libiberty-dev libunwind-dev libcap-dev libzstd-dev libnuma-dev libssl-dev python3-dev python3-setuptools binutils-dev gcc-multilib liblzma-dev
  811  2024-04-29 12:40:26 sudo apt install systemtap-sdt-dev
  812  2024-04-29 12:40:37 sudo apt install clang
  813  2024-04-29 12:40:59 sudo apt install libperl-dev
  814  2024-04-29 12:41:26 sudo apt install libbabeltrace-ctf-dev
  815  2024-04-29 12:42:09 sudo apt install libpfm4-dev
  818  2024-04-29 12:45:43 sudo apt install libtraceevent-dev
  824  2024-04-29 12:57:21 sudo apt install pkgconf
  826  2024-04-29 12:57:38 PYTHON=python3 make -C tools/perf
  829  2024-04-29 13:22:26 cd ~/perf_playground/
  830  2024-04-29 13:23:01 sudo /home/spelley/kernel/linux-stable/tools/perf/perf record --kcore -Se -C 0 -e intel_pt/cyc,noretcomp,mtc_period=9/ timeout 1 taskset -a -c 0 /home/spelley/should-I-async/rust/csgb/target/release/csgb socket 1
  831  2024-04-29 13:24:38 sudo /home/spelley/kernel/linux-stable/tools/perf/perf script -C 0 --itrace=i100ip --ns
  833  2024-04-29 13:25:55 sudo apt-get install sqlite3 python3-pyside2.qtsql libqt5sql5-sqlite
  834  2024-04-29 13:26:50 sudo apt-get install python3-pyside2.qtcore python3-pyside2.qtgui python3-pyside2.qtsql python3-pyside2.qtwidgets
  839  2024-04-29 13:31:44 sudo /home/spelley/kernel/linux-stable/tools/perf/perf script --itrace=bep -s /home/spelley/kernel/linux-stable/tools/perf/scripts/python/export-to-sqlite.py pt.db branches calls
```
Note that there is a typo in `https://perf.wiki.kernel.org/index.php/Perf_tools_support_for_Intel%C2%AE_Processor_Trace#Downloading_and_building_the_latest_perf_tools` where it should install `libqt5sql5-sqlite` instead of `libqt5sql5-psql`

### Interpretting sqlite data
samples_view is the main set of events.  I generally see psb (samples delination for indexing), cbr (power events, see cbr_view for cpu frequency updates), and branches, which are the events we're truly interested in.  For each sample we get thinks like command, cpu, pid, tid, time in ns; and then location and branch information like instruction pointer, symbol, dso, and branch destination of these things.  If the record is the first for a new timestamp it also includes #cycles and #instructions since the last timestamp update.  Finally, we get flags -- see man perf-intel-pt for flags.

flags:
```
The flags are "bcrosyiABExghDt" which stand for branch, call,
       return, conditional, system, asynchronous, interrupt, transaction
       abort, trace begin, trace end, in transaction, VM-entry, VM-exit,
       interrupt disabled, and interrupt disable toggle respectively.
```
so all 'branches' samples should have 1.
call has 2
return has 4.

for understanding function calls:
calls_view -- the set of function calls and returns with call_id and return_id joining to samples id.
call_paths_view -- joins to calls_view on id.  Represents stacks via self join on parent_id.

getting the samples with a cycle/instruction count.  Some of these appear to not have a new timestamp because the sample's timestamp is the same as the previous sample with ns precision:
```
select id, time, insn_count, cyc_count, ipc from samples_view where insn_count > 0 or cyc_count > 0 order by id limit 100;
```

### disabling frequency scaling
Frequency changes throughout the recording, evidenced by cbr packets (e.g., between cbr 28 and 29 for 2796 and 2895Mhz, respectively)

Locate cbr packets in perf.data:
```
sudo /home/spelley/kernel/linux-stable/tools/perf/perf script --itrace=p | grep cbr
```

I've tried to disable with:
```
sudo cpupower set -b 0
sudo cpupower frequency-set -g performance
```
But this is insufficient.  It stays near 2.8, but switches between two nearby frequencies.

setting a specific frequency did not work:
```
sudo cpupower frequency-set -f 2.8GHz

Setting cpu: 0
Error setting new values. Common errors:
- Do you have proper administration rights? (super-user?)
- Is the governor you requested available and modprobed?
- Trying to set an invalid policy?
- Trying to set a specific frequency, but userspace governor is not available,
   for example because of hardware which cannot be set to a specific frequency
   or because the userspace governor isn't loaded?
```

https://unix.stackexchange.com/questions/153693/cant-use-userspace-cpufreq-governor-and-set-cpu-frequency
suggests:
```
disable the current driver: add intel_pstate=disable to your kernel boot line
boot, then load the userspace module: modprobe cpufreq_userspace
set the governor: sudo cpupower frequency-set --governor userspace
set the frequency: sudo cpupower --cpu all frequency-set --freq 2000MHz
```

kernel boot param instructions: https://wiki.ubuntu.com/Kernel/KernelBootParameters
add option to /etc/default/grub GRUB_CMDLINE_LINUX_DEFAULT
sudo update-grub

This appears to have worked (see sudo cpupower -c all frequency-info) but the greatest settable frequency is 2.00 GHz.  The cbr packet says that the frequency is 1997MHz but there's only one so I'll take it.


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

# Really really old and outdated plan

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