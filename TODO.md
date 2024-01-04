# TODO

This file acts as a rough list and description of tasks to be done or ideas under discussion

## Next Steps

### Test Harness and Profile Tooling
top level build all sub projects and create container
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
Add server-side sleep

### Java
Add tcp echo.
Add server-side sleep
measure application performance
add JFR

### Dev Tooling
(done) Devcontainer up and running