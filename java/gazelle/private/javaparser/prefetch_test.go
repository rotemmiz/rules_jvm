package javaparser

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/bazel-contrib/rules_jvm/java/gazelle/private/javaparser/proto/gazelle/java/javaparser/v0"
	"google.golang.org/grpc"
)

// fakeParser answers ParsePackage with a response derived from the request, and records the
// requests it receives. If gate is set, each call waits for it to be closed (or for its
// context to be cancelled) before answering.
type fakeParser struct {
	gate  chan struct{}
	delay func() time.Duration

	mu          sync.Mutex
	calls       map[string]int
	inFlight    int
	maxInFlight int
	started     chan string
}

func newFakeParser() *fakeParser {
	return &fakeParser{calls: make(map[string]int), started: make(chan string, 1000)}
}

func requestKey(rel string, files []string) string {
	return rel + ":" + strings.Join(files, ",")
}

func (f *fakeParser) ParsePackage(ctx context.Context, in *pb.ParsePackageRequest, _ ...grpc.CallOption) (*pb.Package, error) {
	key := requestKey(in.GetRel(), in.GetFiles())
	f.mu.Lock()
	f.calls[key]++
	f.inFlight++
	if f.inFlight > f.maxInFlight {
		f.maxInFlight = f.inFlight
	}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inFlight--
		f.mu.Unlock()
	}()
	f.started <- key

	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.delay != nil {
		time.Sleep(f.delay())
	}
	if strings.HasPrefix(in.GetRel(), "error") {
		return nil, fmt.Errorf("cannot parse %s", key)
	}
	return &pb.Package{Name: key, ImportedClasses: in.GetFiles()}, nil
}

func (f *fakeParser) callCount(rel string, files []string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[requestKey(rel, files)]
}

func (f *fakeParser) currentInFlight() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inFlight
}

func waitForStart(t *testing.T, f *fakeParser, key string) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case got := <-f.started:
			if got == key {
				return
			}
		case <-timeout:
			t.Fatalf("request %q was never sent", key)
		}
	}
}

func parseResult(t *testing.T, p *prefetcher, rel string, files []string) string {
	t.Helper()
	resp, err := p.parse(context.Background(), &ParsePackageRequest{Rel: rel, Files: files})
	if err != nil {
		return "error: " + err.Error()
	}
	return resp.GetName() + "|" + strings.Join(resp.GetImportedClasses(), ",")
}

type testPackage struct {
	rel   string
	files []string
}

func randomPackages(rng *rand.Rand, n int) []testPackage {
	pkgs := make([]testPackage, n)
	for i := range pkgs {
		rel := fmt.Sprintf("pkg%d", i)
		if i%17 == 0 {
			rel = fmt.Sprintf("error%d", i)
		}
		var files []string
		for j := rng.Intn(4) + 1; j > 0; j-- {
			files = append(files, fmt.Sprintf("F%d.java", j))
		}
		pkgs[i] = testPackage{rel: rel, files: files}
	}
	return pkgs
}

// TestPrefetchGivesSameResults parses the same packages with and without prefetching, with
// the prefetches racing the parses, and checks that every response is the same.
func TestPrefetchGivesSameResults(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	pkgs := randomPackages(rng, 300)

	direct := newPrefetcher(newFakeParser(), 4)
	defer direct.stop()
	want := make(map[string]string)
	for _, pkg := range pkgs {
		want[pkg.rel] = parseResult(t, direct, pkg.rel, pkg.files)
	}

	fake := newFakeParser()
	fake.delay = func() time.Duration { return time.Duration(rand.Intn(200)) * time.Microsecond }
	p := newPrefetcher(fake, 4)
	defer p.stop()
	for i, pkg := range pkgs {
		p.schedule(pkg.rel, pkg.files)
		// Parse some packages while others are still being prefetched, in a different order.
		if i%3 == 2 {
			prev := pkgs[i-1]
			if got := parseResult(t, p, prev.rel, prev.files); got != want[prev.rel] {
				t.Errorf("parse(%s) = %q with prefetching, %q without", prev.rel, got, want[prev.rel])
			}
		}
	}
	for i, pkg := range pkgs {
		if i%3 == 1 {
			continue
		}
		if got := parseResult(t, p, pkg.rel, pkg.files); got != want[pkg.rel] {
			t.Errorf("parse(%s) = %q with prefetching, %q without", pkg.rel, got, want[pkg.rel])
		}
	}
	for _, pkg := range pkgs {
		if n := fake.callCount(pkg.rel, pkg.files); n != 1 {
			t.Errorf("%s was sent %d times, want 1", pkg.rel, n)
		}
	}
}

func TestPrefetchUsesRunningParse(t *testing.T) {
	fake := newFakeParser()
	fake.gate = make(chan struct{})
	p := newPrefetcher(fake, 2)
	defer p.stop()

	files := []string{"A.java", "B.java"}
	p.schedule("a", files)
	waitForStart(t, fake, requestKey("a", files))

	result := make(chan string)
	go func() { result <- parseResult(t, p, "a", files) }()
	select {
	case got := <-result:
		t.Fatalf("parse returned %q before the running prefetch finished", got)
	case <-time.After(50 * time.Millisecond):
	}
	close(fake.gate)
	if got, want := <-result, "a:A.java,B.java|A.java,B.java"; got != want {
		t.Errorf("parse = %q, want %q", got, want)
	}
	if n := fake.callCount("a", files); n != 1 {
		t.Errorf("request was sent %d times, want 1", n)
	}
}

func TestPrefetchWithDifferentFilesIsNotUsed(t *testing.T) {
	fake := newFakeParser()
	p := newPrefetcher(fake, 2)
	defer p.stop()

	// For example, a file was excluded, or Kotlin is enabled in the package but wasn't when
	// the prefetch was scheduled.
	prefetched := []string{"A.java", "B.java"}
	p.schedule("a", prefetched)
	waitForStart(t, fake, requestKey("a", prefetched))

	requested := []string{"A.java"}
	if got, want := parseResult(t, p, "a", requested), "a:A.java|A.java"; got != want {
		t.Errorf("parse = %q, want %q", got, want)
	}
	if n := fake.callCount("a", requested); n != 1 {
		t.Errorf("requested files were sent %d times, want 1", n)
	}
}

func TestPrefetchNotStartedIsParsedByCaller(t *testing.T) {
	fake := newFakeParser()
	fake.gate = make(chan struct{})
	p := newPrefetcher(fake, 1)

	files := []string{"A.java"}
	p.schedule("blocker", files)
	waitForStart(t, fake, requestKey("blocker", files))
	// With a concurrency of 1, this one can't start while "blocker" is running.
	p.schedule("queued", files)

	result := make(chan string)
	go func() { result <- parseResult(t, p, "queued", files) }()
	waitForStart(t, fake, requestKey("queued", files))
	close(fake.gate)
	if got, want := <-result, "queued:A.java|A.java"; got != want {
		t.Errorf("parse = %q, want %q", got, want)
	}
	p.stop()
	if n := fake.callCount("queued", files); n != 1 {
		t.Errorf("queued request was sent %d times, want 1 (by the caller only)", n)
	}
}

func TestPrefetchUnknownPackageIsParsedByCaller(t *testing.T) {
	fake := newFakeParser()
	p := newPrefetcher(fake, 2)
	defer p.stop()

	files := []string{"A.java"}
	if got, want := parseResult(t, p, "never-prefetched", files), "never-prefetched:A.java|A.java"; got != want {
		t.Errorf("parse = %q, want %q", got, want)
	}
	// A package is parsed by the caller again if asked twice, rather than reusing anything.
	parseResult(t, p, "never-prefetched", files)
	if n := fake.callCount("never-prefetched", files); n != 2 {
		t.Errorf("request was sent %d times, want 2", n)
	}
}

func TestPrefetchPropagatesErrors(t *testing.T) {
	fake := newFakeParser()
	p := newPrefetcher(fake, 2)
	defer p.stop()

	files := []string{"A.java"}
	p.schedule("error", files)
	waitForStart(t, fake, requestKey("error", files))
	if got, want := parseResult(t, p, "error", files), "error: cannot parse error:A.java"; got != want {
		t.Errorf("parse = %q, want %q", got, want)
	}
}

func TestPrefetchIsScheduledOnce(t *testing.T) {
	fake := newFakeParser()
	p := newPrefetcher(fake, 2)

	files := []string{"A.java"}
	p.schedule("a", files)
	p.schedule("a", files)
	p.schedule("a", []string{"B.java"})
	p.schedule("empty", nil)
	p.stop()
	if n := fake.callCount("a", files); n > 1 {
		t.Errorf("request was sent %d times, want at most 1", n)
	}
	if n := fake.callCount("a", []string{"B.java"}); n != 0 {
		t.Errorf("second schedule of the same package was sent %d times, want 0", n)
	}
	if n := fake.callCount("empty", nil); n != 0 {
		t.Errorf("package without files was sent %d times, want 0", n)
	}
}

func TestPrefetchConcurrencyLimit(t *testing.T) {
	fake := newFakeParser()
	fake.delay = func() time.Duration { return time.Millisecond }
	p := newPrefetcher(fake, 3)
	for i := 0; i < 50; i++ {
		p.schedule(fmt.Sprintf("pkg%d", i), []string{"A.java"})
	}
	for i := 0; i < 50; i++ {
		parseResult(t, p, fmt.Sprintf("pkg%d", i), []string{"A.java"})
	}
	p.stop()
	// The caller's own parses (of prefetches that hadn't started) are not limited, but there
	// is only one caller here.
	if fake.maxInFlight > 3+1 {
		t.Errorf("%d requests were in flight at once, want at most 4", fake.maxInFlight)
	}
}

func TestParseWaitingForPrefetchHonorsContext(t *testing.T) {
	fake := newFakeParser()
	fake.gate = make(chan struct{})
	defer close(fake.gate)
	p := newPrefetcher(fake, 1)
	defer p.stop()

	files := []string{"A.java"}
	p.schedule("a", files)
	waitForStart(t, fake, requestKey("a", files))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.parse(ctx, &ParsePackageRequest{Rel: "a", Files: files}); !errors.Is(err, context.Canceled) {
		t.Errorf("parse with a cancelled context returned %v, want %v", err, context.Canceled)
	}
}

// TestStopLeavesNothingRunning checks that stop cancels queued and running prefetches, that
// their goroutines exit, and that nothing is sent after it returns.
func TestStopLeavesNothingRunning(t *testing.T) {
	before := runtime.NumGoroutine()

	fake := newFakeParser()
	fake.gate = make(chan struct{}) // Never opened: running prefetches only end when cancelled.
	p := newPrefetcher(fake, 4)
	for i := 0; i < 100; i++ {
		p.schedule(fmt.Sprintf("pkg%d", i), []string{"A.java"})
	}
	select {
	case <-fake.started:
	case <-time.After(10 * time.Second):
		t.Fatal("no prefetch was sent")
	}

	p.stop()
	if n := fake.currentInFlight(); n != 0 {
		t.Errorf("%d requests still in flight after stop", n)
	}
	var sent atomic.Int32
	fake.mu.Lock()
	for _, n := range fake.calls {
		sent.Add(int32(n))
	}
	fake.mu.Unlock()
	if n := sent.Load(); n > 4 {
		t.Errorf("%d requests were sent, want at most the 4 that could run at once", n)
	}

	p.schedule("after-stop", []string{"A.java"})
	if n := fake.callCount("after-stop", []string{"A.java"}); n != 0 {
		t.Errorf("a prefetch scheduled after stop was sent %d times", n)
	}

	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Errorf("%d goroutines before prefetching, %d after stop", before, after)
	}
}
