# should-I-async
tools and investigation into measuring blocking io overhead; providing guidance on when to switch to async

## Goals
Based on my professional experience, as well as articles and conversations I've observed (largely HN), there is a poor understanding of the technical mechanisms involved in thread-blocking IO and async IO.  I've been apart of several discussions where, having observed high task latency, cpu utilization, or linux load average someone would implicate blocking IO and recommend switching from thread-blocking IO to async.  In every case I recall further profiling would show that higher-than-expected CPU utlization was caused by something other than blocking IO.  I never saw any measurable observation indicating that blocking IO contributed meaningfully to CPU time.  However, after serveral conversations, I was never able to determine what those observations could even be.  **There is no tool to profile an application and measure the overhead of blocking IO; or there is a severe lack of understanding and knowledge surrounding the technical mechanisms at play and available tooling.**

This project aims to build such a tool or demonstrate/explain why such a tool is inherently out of reach.
The goals of this project are:
* Reproducibility: create a test harness for different async and thread-blocking clients and servers to demonstrate and measure their performance and resource-usage characteristics.
* Investigate and measure: explore various profiling tools to see how blocking and async executions differ.  Attempt to find some metric that definitively points to blocking IO overhead.
* Build: incorporate such metrics into a profiling tool to answer "how much CPU time is spent due to thread blocking IO?" and ultimately "should I switch to async?"
* Share and Teach: write a blog on what I built, or on why such a tool cannot be built today.

## Background
This is essentially a justification for this project.

https://without.boats/blog/why-async-rust/ summarizes the desire for async IO, noting:
> 1. Context-switching between the kernel and userspace is expensive in terms of CPU cycles.
> 2. OS threads have a large pre-allocated stack, which increases per-thread memory overhead.
The rest of the article is an interesting history of and justification for the Rust async design.

I would also add that with today's virtual memory systems even large thread stacks often do not lead to high physical memory usage.  Thread stacks are only a problem when threads are long lived and see a large stack depth _at some point_.  Additionally, for server applications these days you often have quite a bit of memory.  1MB stacks * 1000 threads = 1GB memory may be a reasonable cost.

For this reason I'm going to focus on the CPU cost (point 1 above).

I enjoy reading https://www.usenix.org/legacy/event/osdi10/tech/full_papers/Soares.pdf for their description of system call overhead.  Note that this paper discusses syscalls in general and does not discuss context switches and IO-specific syscalls.  Generally, the cost of a system call is the time to run the actual system call code plus the "indirect" cost of the system call -- the inefficiences introduced by the system call.  This tends to be a reduction in IPC (instructions per cycle) associated with lower cache and TLB hit rates.  Unfortunately, the experiments of the paper generally measure these qualities over long durations and compare controlled configurations; the same methodology cannot be used to profile a single application to determine if blocking IO changes IPC materially compared to async IO _without also implementing the async version_.  It may simply be that syscalls or context switches per time (or instructions executed per syscall/context switches) is a sufficient proxy to determine this, but this requires investigation.

Figure 1 in the above paper shows a time series of IPC during and after a syscall.  It's not clear from the text how this data is generated -- that is, it's not clear how to measure IPC over 1000-cycle periods outside of simulation, and the authors do not explain.  Section 2.3, which discusses sycall impact on user mode IPC, does not provide a methodology for this figure.  If we could do this, and then aggregate IPC in periods based on whether events did or did not occur in or before those periods, we'd have a great tool to correlate events (like syscalls, context switches) with changes in IPC.  I have no idea if such intersecting performance counters are available and I doubt they are.

## Plan
1. Create a simple async echo http, tcp, or udp server in golang.  Golang is inherently async and so this should be simple and performant.  We want to allow the _client_ to be the bottleneck, so the server should be fast and efficient.
2. Create a simple async client in golang.
3. Create a simple thread-blocking client in java.
4. Determine independent variables for experiments (e.g., a fixed number of outstanding requests at all times, a target cpu core utilization, a target request latency).
5. Create a test harness running a client and server in distinct docker containers.
6. Measure and Profile each of the client and server.  Profile the overall system to ensure that the container and virtual network do not impose overheads independent of the type of client that will impact our conclusions.  Compare the request throughput between async and thread-blocking.
7. Build additional clients for comparison.  Examples might be thread-blocking and async in the same language (C/C++, Rust, Java with virtual threads).
8. Repeat measurements and profiling.  Search for measurements that measure the IPC effect of syscalls and context switches as directly as possible

## Additional References
* intel rdpmc from userspace https://community.intel.com/t5/Software-Tuning-Performance/Configuring-the-PMU-to-read-Perfomance-Counters/m-p/1177708
* PAPI https://github.com/icl-utk-edu/papi tool intended to instrument your source code with "calipers" to read performance counters within a section.
* perf toggle event https://lwn.net/Articles/568602/ perf event to trigger enabling/disabling perf counters in kernel.  This is what I want, assuming latency to toggle is small (100s ns).  Unfortunately it appears this was never merged and no one continued working on it.
* PINTool https://www.intel.com/content/www/us/en/developer/articles/tool/pin-a-dynamic-binary-instrumentation-tool.html consider dynamically instrumenting code randomly in the hope of discovering ~5-10us segments following return from syscalls without introducing too many other injection points.
