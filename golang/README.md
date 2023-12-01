# Go client and server

## Error handling
For now I'm treating the client and server applications and "whole" applications, not individual requests.
As such, an error anywhere should propagate and terminate the process.
I will not recover() panics.
All errors should be assumed internal errors and add context required to debug (as opposed to user errors that need only a message to send the user).

My strategy is thus:
* Use go-errors/errors to add stack traces to errors.
* Wrap when returning errors.  If the wrapped error is not a *go-errors/errors.Error then a stack trace will be added.  If it is then Wrap() returns it directly and so it's a no-op.
* Force-wrap errors that cross goroutine boundaries using go-errors/errors.Errorf so that the stack trace from each goroutine is provided.  Errors cross goroutine boundaries explicitly through channels but also implicitly through contexts (among other places)
* WrapPrefix() anywhere an error is not simply wrapped and returned.  This allows tracking the error and adding context where a stack trace is insufficient to follow control flow.  Examples include pushing an error onto a channel, passing an error to a function for complex handling, or anywhere an error is handled with separate if-else cases.