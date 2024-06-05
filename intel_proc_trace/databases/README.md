Keep this file here so that git clone creates the databases directory (database
files in directory are gitignored)

duckdb files are named with:
sync or async
retcomp for ipt return compression, default no return compression
kernelonly to indicate the trace is for kernel space only, default all
noswitchevents to indicate perf record --no-switch-events, default --switch-events