package util

// I've abandoned this for now.  The goal was to create an errgroup.Group that could either:
// 1. accept a context for Go() and Wait() such that a blocking all would return
// when the context finished.
// 2. provide a Close() method that would result in blocked callers of Go() and
// Wait() to return immediately and assign an ErrClosed error to the group if it
// had not already been assigned an error or completed.  This is roughly the
// semantics of net.Conn whose methods do not accept a context but where
// blockers are released when the connection is Close()ed.
//
// This turned out to be difficult and complex for 2 reasons:
//
// The official errgroup does not have any means of interrupting these method
// calls.  Neither do some related types such as WaitGroup.  As a result there
// are no easy and obvious tools with which to compose our extended errgroup.
//
// Additionally, the semantics quickly get complex.
// Typically the derived context of an errgroup is assigned when the first
// caller to Wait returns.  This must be modified.  If Wait() accepts a context
// then I don't want an interrupted Wait() to impact the execution within the
// errgroup, so there needs to be some notion of whether the call to Wait() is
// "registered".  The context finishing "unregisters" the Wait().  And so the
// context is completed the first time all submitted tasks complete while there
// is a "registered Wait()".  Go() has a similar sort of race between scheduling
// a goroutine and the submission context finishing, but this is at least more
// of an internal implementation detail (not semantics)
//
// Using (2) above with Close() simplifies these semantics a bit:
// if all outstanding tasks finish while there is a call to Wait() then the group is closed without error.
// if Close() is called with and there are no outstanding tasks then the group is closed without error.
// if any task resulted in an error and the group was collectively cancelled
// then it will take its error from the task and subsequent calls to Close() do
// not change its state.
// if Close() is called and there are outstanding tasks then the group is
// cancelled with error ErrClosed and all blockers on Wait() and Go() return
// immediately.
//
// the latter is my preferred semantic but this still ended up being difficult to implement.
// everything must now revolve around a "closed" channel and all blockers must select this.
// Since the original uses WaitGroups, which similarly provides no way to unblock, it needs to
// be restructured.

////import (
////	"context"
////	"errors"
////	"fmt"
////	"sync"
////)
////
////// this is a drop-in replacement for x/sync/errgroup.  It additionally provides
////// a Close() method that, when called, causes all goroutines blocked on Go() or
////// Wait() to return immediately and return ErrClosed.
////
////////type setLimitArgs struct {
////////	limit              int
////////	closeAfterLimitSet chan struct{}
////////}
////////
////////type acquireArgs struct {
////////	// each of the submitter and Group goroutine will attempt to set isScheduled
////////	// using setScheduledOnce.  They will then read isScheduled to determine
////////	// whether it is scheduled.
////////	isScheduled        *bool
////////	setScheduledOnce   *sync.Once
////////	closeAfterAcquired chan struct{}
////////}
////////
////////func (a *acquireArgs) trySchedule() bool {
////////	a.setScheduledOnce.Do(func() {
////////		*a.isScheduled = true
////////	})
////////	if *a.isScheduled {
////////		close(a.closeAfterAcquired)
////////	}
////////	return *a.isScheduled
////////}
////////
////////type releaseArgs struct {
////////	closeAfterReleased chan struct{}
////////}
////
////var ErrClosed error = errors.New("Group closed")
////
////type Group struct {
////	// context associated with this Group.  Will be cancelled upon the first
////	// return non-context-cancelled return from Wait or once the first submitted
////	// task returns an error.
////	ctx context.Context
////
////	// function to cancel the associated context
////	cancelFunc context.CancelFunc
////
////	// when closed will cancel all waiters and stop accepting new tasks
////	// subsequent calls to Wait() return immediately with ErrClosed
////	closed chan struct{}
////
////	// these variables must only be accessed by the Group goroutine!
////	setErrOnce sync.Once
////	err        error
////
////	mu sync.Mutex
////
////	////// these channels represent requests to be sent to the Group goroutine.
////	////// each entry addiitonally provides a channel for a return value or to
////	////// synchronize on the completion of the request.
////	////setLimit chan setLimitArgs
////	////acquire  chan acquireArgs
////	////release  chan releaseArgs
////}
////
////func WithContext(ctx context.Context) (*Group, context.Context) {
////	newCtx, cancelFunc := context.WithCancel(ctx)
////
////	g := &Group{
////		ctx:        newCtx,
////		cancelFunc: cancelFunc,
////		done:       make(chan struct{}),
////		close:      make(chan struct{}),
////		setLimit:   make(chan setLimitArgs),
////		acquire:    make(chan acquireArgs),
////		release:    make(chan releaseArgs),
////	}
////
////	// spin up our Group goroutine
////	go func() {
////		var limit int
////		var inFlight int
////		var isWaiting bool
////		acquireRequests := make([]acquireArgs, 0)
////		for {
////			select {
////			case <-g.close:
////				return
////			case limitArgs := <-g.setLimit:
////				if limitArgs.limit >= 0 && inFlight > 0 {
////					panic(fmt.Errorf("errgroup: modify limit while %v goroutines in the group are still active", inFlight))
////				}
////				limit = limitArgs.limit
////				close(limitArgs.closeAfterLimitSet)
////			case acquireArgs := <-g.acquire:
////				if limit < 0 || inFlight < limit {
////					isScheduled := acquireArgs.trySchedule()
////					if isScheduled {
////						inFlight++
////					}
////				} else {
////					// we've reached the limit.  Queue this up
////					acquireRequests = append(acquireRequests, acquireArgs)
////				}
////			case releaseArgs := <-g.release:
////				// if any waiters then release 1
////				if len(acquireRequests) > 0 {
////					// first one might have been cancelled! do in a loop
////					var i int
////					for i = 0; i < len(acquireRequests); i++ {
////						acq := acquireRequests[i]
////						isScheduled := acq.trySchedule()
////						if isScheduled {
////							break
////						}
////					}
////					if i == len(acquireRequests) {
////						// did not schedule anything.  Queue now empty
////						acquireRequests = make([]acquireArgs, 0)
////						inFlight--
////					} else {
////						acquireRequests = acquireRequests[i:]
////					}
////				} else {
////					inFlight--
////				}
////
////				close(releaseArgs.closeAfterReleased)
////			}
////		}
////	}()
////
////	return g, newCtx
////}
////
////// TODO there is a race between the context being cancelled and the goroutine being started.
////// need a DoOnce or CAS to determine who wins
////// returns true if the task was started and false otherwise (because the context is done)
////func (g *Group) Go(f func() error) error {
////	aa := acquireArgs{
////		isScheduled:        new(bool),
////		setScheduledOnce:   &sync.Once{},
////		closeAfterAcquired: make(chan struct{})}
////	select {
////	case g.acquire <- aa:
////	case <-ctx.Done():
////		aa.setScheduledOnce.Do(func() {
////			*aa.isScheduled = false
////		})
////	}
////
////	select {
////	case <-aa.closeAfterAcquired:
////	case <-ctx.Done():
////		aa.setScheduledOnce.Do(func() {
////			*aa.isScheduled = false
////		})
////	}
////	// reading *aa.isScheduled resolved, via the sync.Once, whether we consider this task scheduled
////	// or its submission cancelled.  If it was scheduled then we need to release the semaphore
////	// when done
////	if *aa.isScheduled {
////		go func() {
////			defer func() {
////				ra := releaseArgs{closeAfterReleased: make(chan struct{})}
////				select {
////				case g.release <- ra:
////				case <-g.close:
////				}
////			}()
////			err := f()
////			if err != nil {
////				g.tryEndWithError(err)
////			}
////		}()
////	}
////
////	return *aa.isScheduled
////}
////
////func (g *Group) SetLimit(n int) {
////	la := setLimitArgs{limit: n, closeAfterLimitSet: make(chan struct{})}
////	// submit the set limit request
////	select {
////	case <-g.close:
////		return
////	case g.setLimit <- la:
////	}
////
////	// synchronize on a response.  This is to ensure that a subsequent call to Go() observes the
////	// effect of this limit setting
////	select {
////	case <-g.close:
////	case <-la.closeAfterLimitSet:
////	}
////}
////
////func (g *Group) TryGo(f func() error) bool {
////
////}
////
////func (g *Group) Wait() error {
////	select {
////	case <-ctx.Done():
////	case <-g.done:
////	case <-g.close:
////	}
////
////}
////
////func (g *Group) WaitChan() chan struct{} {
////	return g.closed
////}
////
////func (g *Group) Close() error {
////
////}
////
////// attempts to end this group with the provided error.  err must not be nil.
////// returns whatever error the group ends with (may be from some earlier call)
////func (g *Group) tryEndWithError(err error) error {
////	if err == nil {
////		panic("tryEndWithError called with nil err")
////	}
////
////	g.setErrOnce.Do(func() {
////		g.err = err
////		close(g.closed)
////		g.cancelFunc()
////	})
////	return g.err
////}
////
