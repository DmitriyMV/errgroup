// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
// Package errgroup provides synchronization, error propagation, and Context
// cancelation for groups of goroutines working on subtasks of a common task.
package errgroup

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"sync"
)

// A Group is a collection of goroutines working on subtasks that are part of
// the same overall task.
//
// A zero Group is valid and does not cancel on error.
type Group struct {
	wg            sync.WaitGroup
	cancelOnWait  bool
	cancelOnce    sync.Once
	cancelCtx     func()
	errOnce       sync.Once
	err           error
	terminateOnce sync.Once
	panic         interface{}
	goexit        bool
}

// WithContext returns a new Group and an associated Context derived from ctx.
//
// The derived Context is canceled if any goroutine in the group returns a
// non-nil error, panics, or invokes runtime.Goexit, if any other goroutine
// calls Stop on the returned Group, or the first time Wait returns — whichever
// occurs first.
//
// Deprecated: use New instead, and defer a call to Stop to clean up.
func WithContext(ctx context.Context) (*Group, context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	return &Group{cancelCtx: cancel, cancelOnWait: true}, ctx
}

// New returns a new Group and an associated Context derived from ctx.
//
// The derived Context is canceled if any goroutine in the group returns a
// non-nil error, panics, or invokes runtime.Goexit, or if any other goroutine
// calls Stop on the returned Group.
//
// Stopping the Group releases resources associated with its Context, so code
// should call Stop as soon as the Group is no longer needed.
func New(ctx context.Context) (*Group, context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	return &Group{cancelCtx: cancel}, ctx
}

// Wait blocks until all goroutines in the group have exited.
//
// If any goroutine panicked or invoked runtime.Goexit, Wait panics with a
// corresponding value or invokes runtime.Goexit.
//
// Otherwise, Wait returns the first non-nil error (if any) returned by any of
// the functions passed to Go.
func (g *Group) Wait() error {
	g.wg.Wait()
	if g.cancelOnWait {
		g.cancel()
	}
	if g.panic != nil {
		panic(g.panic)
	}
	if g.goexit {
		runtime.Goexit()
	}
	return g.err
}

// Stop cancels the Context associated with g, if any, then waits for all
// goroutines started by the Go method to exit.
func (g *Group) Stop() {
	g.cancel()
	g.wg.Wait()
}
func (g *Group) cancel() {
	g.cancelOnce.Do(func() {
		if g.cancelCtx != nil {
			g.cancelCtx()
		}
	})
}

// Go calls the given function in a new goroutine,
// adding that goroutine to the group.
//
// The first goroutine in the group that returns a non-nil error, panics, or
// invokes runtime.Goexit will cancel the group.
func (g *Group) Go(f func() error) {
	g.wg.Add(1)
	go func() {
		var goexiting bool
		// ⚠ Even though we recover (and save) any panic from f, runtime.Goexit
		// cannot be recovered: we must defer at least enough of the result
		// processing to handle the goexit path.
		defer func() {
			if goexiting {
				g.terminateOnce.Do(func() {
					g.goexit = true
					g.cancel()
				})
			}
			g.wg.Done()
		}()
		panicValue, err := doubleDeferSandwich(&goexiting, f)
		if panicValue != nil {
			g.terminateOnce.Do(func() {
				g.panic = panicValue
				g.cancel()
			})
		} else if err != nil {
			g.errOnce.Do(func() {
				g.err = err
				g.cancel()
			})
		}
	}()
}

// doubleDeferSandwich uses two 'defer' statements to determine whether
// f panicked, invoked runtime.Goexit, or returned normally.
//
// (See https://golang.org/issue/25448#issuecomment-420006237.)
func doubleDeferSandwich(goexiting *bool, f func() error) (panicValue interface{}, err error) {
	normalReturn := false
	recovered := false
	defer func() {
		if !normalReturn && !recovered {
			*goexiting = true
		}
	}()
	func() {
		defer func() {
			if !normalReturn {
				// Ideally, we would wait to take a stack trace until we've determined
				// whether this is a panic or a runtime.Goexit.
				//
				// Unfortunately, the only way we can distinguish the two is to see
				// whether the recover stopped the goroutine from terminating, and by
				// the time we know that, the part of the stack trace relevant to the
				// panic has been discarded.
				panicValue = errorOrStack(recover())
			}
		}()
		err = f()
		normalReturn = true
	}()
	if !normalReturn {
		recovered = true
	}
	return panicValue, err
}

// A panicStack is an arbitrary value recovered from a panic
// augmented with the stack trace at which the panic occurred.
type panicStack struct {
	value interface{}
	stack []byte
}

func (p panicStack) String() string {
	return fmt.Sprintf("%v\n\nvia errgroup.Go:\n%s", p.value, p.stack)
}

// errorOrStack returns v if it implements error, or a panicStack
// that wraps v otherwise.
func errorOrStack(v interface{}) interface{} {
	// If the panic value is an error (such as http.ErrAbortHandler or
	// bytes.ErrTooLarge), preserve its structure.
	if _, ok := v.(error); ok {
		return v
	}
	// Otherwise, capture a stack trace to aid in debugging.
	stack := make([]byte, 2<<10)
	n := runtime.Stack(stack, false)
	for n == len(stack) {
		stack = make([]byte, len(stack)*2)
		n = runtime.Stack(stack, false)
	}
	stack = stack[:n]
	// The first line of the stack trace is of the form "goroutine N [status]:"
	// but by the time the panic reaches Wait the goroutine will no longer exist
	// and its status will have changed. Trim out the misleading line.
	if line := bytes.IndexByte(stack[:], '\n'); line >= 0 {
		stack = stack[line+1:]
	}
	return panicStack{value: v, stack: stack}
}
