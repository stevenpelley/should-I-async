# TODO

This file acts as a rough list and description of tasks to be done or ideas under discussion

## Next Steps

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