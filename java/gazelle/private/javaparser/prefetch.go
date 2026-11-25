package javaparser

import (
	"context"
	"runtime"
	"slices"
	"sync"

	pb "github.com/bazel-contrib/rules_jvm/java/gazelle/private/javaparser/proto/gazelle/java/javaparser/v0"
)

// prefetcher issues ParsePackage RPCs ahead of the GenerateRules calls that need them.
//
// Gazelle generates one directory at a time, and each GenerateRules call waits for its
// ParsePackage RPC, so the javaparser server, which parses requests concurrently, only ever
// had one request to work on. Gazelle configures a directory before it walks its
// subdirectories, and generates it after them, so starting the parse of a directory (and of
// its direct subdirectories) when it is configured gives the server work to do while Gazelle
// walks the rest of the tree.
//
// A prefetched response is only used for a request with the same Rel and Files as the one
// that was prefetched; any other request is sent to the server as before. A prefetch that has
// not started by the time its result is needed is not waited for: the caller sends the
// request itself. Each request is parsed independently by the server, so a prefetched
// response is the same as the one the caller would have received.
type prefetcher struct {
	rpc    pb.JavaParserClient
	ctx    context.Context
	cancel context.CancelFunc
	sem    chan struct{}
	wg     sync.WaitGroup

	mu      sync.Mutex
	stopped bool
	seen    map[string]struct{}
	pending map[string]*prefetchedParse
}

type prefetchedParse struct {
	files []string
	// claimed is set when either the background goroutine starts the RPC or a caller
	// decides not to use this prefetch. Guarded by prefetcher.mu.
	claimed bool
	// started is set if the background goroutine sent the RPC, in which case done is
	// closed when resp and err are set. Guarded by prefetcher.mu.
	started bool
	done    chan struct{}
	resp    *pb.Package
	err     error
}

func newPrefetcher(rpc pb.JavaParserClient, concurrency int) *prefetcher {
	if concurrency <= 0 {
		concurrency = runtime.GOMAXPROCS(0)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &prefetcher{
		rpc:     rpc,
		ctx:     ctx,
		cancel:  cancel,
		sem:     make(chan struct{}, concurrency),
		seen:    make(map[string]struct{}),
		pending: make(map[string]*prefetchedParse),
	}
}

// schedule starts parsing files in rel in the background, unless rel was already scheduled.
func (p *prefetcher) schedule(rel string, files []string) {
	if len(files) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	if _, ok := p.seen[rel]; ok {
		return
	}
	p.seen[rel] = struct{}{}
	e := &prefetchedParse{files: slices.Clone(files), done: make(chan struct{})}
	p.pending[rel] = e
	p.wg.Add(1)
	go p.run(rel, e)
}

func (p *prefetcher) run(rel string, e *prefetchedParse) {
	defer p.wg.Done()
	select {
	case p.sem <- struct{}{}:
	case <-p.ctx.Done():
		return
	}
	defer func() { <-p.sem }()

	p.mu.Lock()
	if e.claimed || p.stopped {
		p.mu.Unlock()
		return
	}
	e.claimed = true
	e.started = true
	p.mu.Unlock()

	e.resp, e.err = p.rpc.ParsePackage(p.ctx, &pb.ParsePackageRequest{Rel: rel, Files: e.files})
	close(e.done)
}

// parse returns the response to in, from a prefetch if one with the same request is running
// or finished, or else by sending the request.
func (p *prefetcher) parse(ctx context.Context, in *ParsePackageRequest) (*pb.Package, error) {
	p.mu.Lock()
	e := p.pending[in.Rel]
	delete(p.pending, in.Rel)
	if e != nil && (!e.started || !slices.Equal(e.files, in.Files)) {
		// Not started yet, or a different request: don't use it, and make sure it doesn't start.
		e.claimed = true
		e = nil
	}
	p.mu.Unlock()

	if e == nil {
		return p.rpc.ParsePackage(ctx, &pb.ParsePackageRequest{Rel: in.Rel, Files: in.Files})
	}
	select {
	case <-e.done:
		return e.resp, e.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// stop cancels the prefetches that are still queued or running, and waits for their
// goroutines to exit. Prefetch requests made after stop are ignored.
func (p *prefetcher) stop() {
	p.mu.Lock()
	p.stopped = true
	p.pending = make(map[string]*prefetchedParse)
	p.mu.Unlock()
	p.cancel()
	p.wg.Wait()
}
