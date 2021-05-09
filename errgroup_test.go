// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package errgroup_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/DmitriyMV/errgroup"
	"net/http"
	"os"
	"strings"
	"testing"
)

var (
	Web   = fakeSearch("web")
	Image = fakeSearch("image")
	Video = fakeSearch("video")
)

type Result string
type Search func(ctx context.Context, query string) (Result, error)

func fakeSearch(kind string) Search {
	return func(_ context.Context, query string) (Result, error) {
		return Result(fmt.Sprintf("%s result for %q", kind, query)), nil
	}
}

// JustErrors illustrates the use of a Group in place of a sync.WaitGroup to
// simplify goroutine counting and error handling. This example is derived from
// the sync.WaitGroup example at https://golang.org/pkg/sync/#example_WaitGroup.
func ExampleGroup_justErrors() {
	var g errgroup.Group
	defer g.Stop()
	var urls = []string{
		"http://www.golang.org/",
		"http://www.google.com/",
		"http://www.somestupidname.com/",
	}
	for _, url := range urls {
		// Launch a goroutine to fetch the URL.
		url := url // https://golang.org/doc/faq#closures_and_goroutines
		g.Go(func() error {
			// Fetch the URL.
			resp, err := http.Get(url)
			if err == nil {
				resp.Body.Close()
			}
			return err
		})
	}
	// Wait for all HTTP fetches to complete.
	if err := g.Wait(); err == nil {
		fmt.Println("Successfully fetched all URLs.")
	}
}

// Parallel illustrates the use of a Group for synchronizing a simple parallel
// task: the "Google Search 2.0" function from
// https://talks.golang.org/2012/concurrency.slide#46, augmented with a Context
// and error-handling.
func ExampleGroup_parallel() {
	Google := func(ctx context.Context, query string) ([]Result, error) {
		g, ctx := errgroup.New(ctx)
		defer g.Stop()
		searches := []Search{Web, Image, Video}
		results := make([]Result, len(searches))
		for i, search := range searches {
			i, search := i, search // https://golang.org/doc/faq#closures_and_goroutines
			g.Go(func() error {
				result, err := search(ctx, query)
				if err == nil {
					results[i] = result
				}
				return err
			})
		}
		if err := g.Wait(); err != nil {
			return nil, err
		}
		return results, nil
	}
	results, err := Google(context.Background(), "golang")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	for _, result := range results {
		fmt.Println(result)
	}
	// Output:
	// web result for "golang"
	// image result for "golang"
	// video result for "golang"
}
func TestZeroGroup(t *testing.T) {
	err1 := errors.New("errgroup_test: 1")
	err2 := errors.New("errgroup_test: 2")
	cases := []struct {
		errs []error
	}{
		{errs: []error{}},
		{errs: []error{nil}},
		{errs: []error{err1}},
		{errs: []error{err1, nil}},
		{errs: []error{err1, nil, err2}},
	}
	for _, tc := range cases {
		var g errgroup.Group
		var firstErr error
		for i, err := range tc.errs {
			err := err
			g.Go(func() error { return err })
			if firstErr == nil && err != nil {
				firstErr = err
			}
			if gErr := g.Wait(); gErr != firstErr {
				t.Errorf("after %T.Go(func() error { return err }) for err in %v\n"+
					"g.Wait() = %v; want %v",
					g, tc.errs[:i+1], err, firstErr)
			}
		}
	}
}
func TestWithContext(t *testing.T) {
	errDoom := errors.New("group_test: doomed")
	cases := []struct {
		errs []error
		want error
	}{
		{want: nil},
		{errs: []error{nil}, want: nil},
		{errs: []error{errDoom}, want: errDoom},
		{errs: []error{errDoom, nil}, want: errDoom},
	}
	for _, tc := range cases {
		g, ctx := errgroup.WithContext(context.Background())
		for _, err := range tc.errs {
			err := err
			g.Go(func() error { return err })
		}
		if err := g.Wait(); err != tc.want {
			t.Errorf("after %T.Go(func() error { return err }) for err in %v\n"+
				"g.Wait() = %v; want %v",
				g, tc.errs, err, tc.want)
		}
		canceled := false
		select {
		case <-ctx.Done():
			canceled = true
		default:
		}
		if !canceled {
			t.Errorf("after %T.Go(func() error { return err }) for err in %v\n"+
				"ctx.Done() was not closed",
				g, tc.errs)
		}
	}
}
func TestNew(t *testing.T) {
	errDoom := errors.New("group_test: doomed")
	cases := []struct {
		errs []error
		want error
	}{
		{want: nil},
		{errs: []error{nil}, want: nil},
		{errs: []error{errDoom}, want: errDoom},
		{errs: []error{errDoom, nil}, want: errDoom},
	}
	for _, tc := range cases {
		g, ctx := errgroup.New(context.Background())
		defer g.Stop()
		for _, err := range tc.errs {
			err := err
			g.Go(func() error { return err })
		}
		if err := g.Wait(); err != tc.want {
			t.Errorf("after %T.Go(func() error { return err }) for err in %v\n"+
				"g.Wait() = %v; want %v",
				g, tc.errs, err, tc.want)
		}
		if (ctx.Err() != nil) != (tc.want != nil) {
			t.Errorf("after %T.Go(func() error { return err }) for err in %v\n"+
				"ctx.Err() = %v",
				g, tc.errs, ctx.Err())
		}
	}
}
func terminateInGroup(t *testing.T, terminate func() error) (panicValue interface{}) {
	t.Helper()
	defer func() {
		panicValue = recover()
	}()
	g, ctx := errgroup.New(context.Background())
	defer g.Stop()
	var waited = false
	g.Go(func() error {
		<-ctx.Done()
		waited = true
		return ctx.Err()
	})
	defer func() {
		if !waited {
			t.Errorf("did not wait for other goroutines to exit")
		}
	}()
	g.Go(terminate)
	err := g.Wait()
	t.Fatalf("g.Wait() unexpectedly returned (with error %v)", err)
	return panicValue
}
func TestPanic(t *testing.T) {
	t.Run("<nil>", func(t *testing.T) {
		got := terminateInGroup(t, func() error {
			panic(nil)
		})
		if !strings.HasPrefix(fmt.Sprint(got), "<nil>") {
			t.Errorf("panicked with %v; want .String() beginning with <nil>", got)
		}
	})
	t.Run("non-error", func(t *testing.T) {
		const s = "some string"
		got := terminateInGroup(t, func() error {
			panic(s)
		})
		if !strings.HasPrefix(fmt.Sprint(got), s) {
			t.Errorf("panicked with %v; want .String() beginning with %#v", got, s)
		}
	})
	t.Run("error", func(t *testing.T) {
		var errPanic = errors.New("errPanic")
		got := terminateInGroup(t, func() error {
			panic(errPanic)
		})
		if got != errPanic {
			t.Errorf("panicked with %v; want %v", got, errPanic)
		}
	})
}
func TestGoexit(t *testing.T) {
	// We have to test runtime.Goexit from a separate goroutine: testing.T itself
	// uses runtime.Goexit for SkipNow and FailNow, so it gets cranky if a test
	// goroutine calls runtime.Goexit otherwise.
	c := make(chan interface{}, 1)
	t.Run("goexit via Skip", func(inner *testing.T) {
		defer close(c)
		c <- terminateInGroup(t, func() error {
			inner.Skip("goexit!")
			return nil
		})
	})
	got := <-c
	if got != nil {
		t.Errorf("panicked with %v; want runtime.Goexit()", got)
	}
}
