// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package errgroup_test

import (
	"context"
	"crypto/md5"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"github.com/DmitriyMV/errgroup"
)

// Pipeline demonstrates the use of a Group to implement a multi-stage
// pipeline: a version of the MD5All function with bounded parallelism from
// https://blog.golang.org/pipelines.
func ExampleGroup_pipeline() {
	m, err := MD5All(context.Background(), ".")
	if err != nil {
		log.Fatal(err)
	}
	for k, sum := range m {
		fmt.Printf("%s:\t%x\n", k, sum)
	}
}

type result struct {
	path string
	sum  [md5.Size]byte
}
type token struct{}

// MD5All reads all the files in the file tree rooted at root and returns a map
// from file path to the MD5 sum of the file's contents. If the directory walk
// fails or any read operation fails, MD5All returns an error.
func MD5All(ctx context.Context, root string) (map[string][md5.Size]byte, error) {
	// ctx is canceled when g.Stop is called or any goroutine returns an error.
	// When this version of MD5All returns — even in case of error! — we know that
	// all of the goroutines have finished and the memory they were using can be
	// garbage-collected.
	g, ctx := errgroup.New(ctx)
	defer g.Stop()
	paths := make(chan string)
	g.Go(func() error {
		defer close(paths)
		return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			select {
			case paths <- path:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		})
	})
	// Read and digest files concurrently,
	// storing the results so far in a channel buffer.
	results := make(chan map[string][md5.Size]byte, 1)
	results <- make(map[string][md5.Size]byte)
	// Use a semaphore to bound the number of files in flight.
	const maxInFlight = 20
	semaphore := make(chan token, maxInFlight)
	for path := range paths {
		select {
		case <-ctx.Done():
			// We can return immediately without leaving any goroutines behind:
			// the 'defer g.Stop()' above will finish cleaning up.
			return nil, ctx.Err()
		case semaphore <- token{}:
		}
		path := path
		g.Go(func() error {
			defer func() { <-semaphore }()
			data, err := ioutil.ReadFile(path)
			if err != nil {
				return err
			}
			sum := md5.Sum(data)
			m := <-results
			m[path] = sum
			results <- m
			return nil
		})
	}
	// Check whether any of the goroutines failed. Since g is accumulating the
	// errors, we don't need to send them (or check for them) in the individual
	// results sent on the channel.
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return <-results, nil
}
