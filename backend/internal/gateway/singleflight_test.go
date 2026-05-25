package gateway

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSingleFlight_SingleCallerExecutes(t *testing.T) {
	sf := NewSingleFlight()
	val, err, shared := sf.Do("k", func() (any, error) { return 42, nil })
	if err != nil {
		t.Fatal(err)
	}
	if val != 42 {
		t.Fatalf("got %v; want 42", val)
	}
	if shared {
		t.Fatal("solo caller should not be shared")
	}
}

func TestSingleFlight_FiveConcurrentSameKeyExecutesOnce(t *testing.T) {
	sf := NewSingleFlight()
	var calls atomic.Int32
	gate := make(chan struct{})
	fn := func() (any, error) {
		<-gate
		calls.Add(1)
		return "result", nil
	}
	var wg sync.WaitGroup
	results := make([]any, 5)
	sharedFlags := make([]bool, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, _, shared := sf.Do("same", fn)
			results[i] = v
			sharedFlags[i] = shared
		}(i)
	}
	// Let goroutines all park on Do
	time.Sleep(20 * time.Millisecond)
	close(gate)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("fn calls=%d; want exactly 1", got)
	}
	for _, r := range results {
		if r != "result" {
			t.Fatalf("follower got wrong result: %v", r)
		}
	}
	executors := 0
	for _, s := range sharedFlags {
		if !s {
			executors++
		}
	}
	if executors != 1 {
		t.Fatalf("got %d executors; want exactly 1", executors)
	}
}

func TestSingleFlight_DifferentKeysParallel(t *testing.T) {
	sf := NewSingleFlight()
	var calls atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		key := []string{"a", "b", "c"}[i]
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			_, _, _ = sf.Do(k, func() (any, error) {
				calls.Add(1)
				return k, nil
			})
		}(key)
	}
	wg.Wait()
	if got := calls.Load(); got != 3 {
		t.Fatalf("3 distinct keys, fn calls=%d; want 3", got)
	}
}

func TestSingleFlight_ErrorBroadcastsToFollowers(t *testing.T) {
	sf := NewSingleFlight()
	wantErr := errors.New("upstream down")
	gate := make(chan struct{})
	fn := func() (any, error) {
		<-gate
		return nil, wantErr
	}
	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e, _ := sf.Do("k", fn)
			errs[i] = e
		}(i)
	}
	time.Sleep(20 * time.Millisecond)
	close(gate)
	wg.Wait()
	for _, e := range errs {
		if !errors.Is(e, wantErr) {
			t.Fatalf("follower got %v; want %v", e, wantErr)
		}
	}
}

func TestSingleFlight_ForgetTriggersRerun(t *testing.T) {
	sf := NewSingleFlight()
	var calls atomic.Int32
	fn := func() (any, error) {
		calls.Add(1)
		return calls.Load(), nil
	}
	v1, _, _ := sf.Do("k", fn) // calls=1
	sf.Forget("k")
	v2, _, _ := sf.Do("k", fn) // calls=2
	if v1 == v2 {
		t.Fatalf("Forget should rerun fn; got same %v twice", v1)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d; want 2", calls.Load())
	}
}

func TestSingleFlight_InFlightLifecycle(t *testing.T) {
	sf := NewSingleFlight()
	gate := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_, _, _ = sf.Do("k", func() (any, error) {
			<-gate
			return 1, nil
		})
		close(done)
	}()
	// give goroutine time to park
	time.Sleep(20 * time.Millisecond)
	if !sf.InFlight("k") {
		t.Fatal("expected InFlight=true during execution")
	}
	close(gate)
	<-done
	if sf.InFlight("k") {
		t.Fatal("expected InFlight=false after completion")
	}
}

func TestSingleFlight_PanicRecoversAndWakesFollowers(t *testing.T) {
	sf := NewSingleFlight()
	gate := make(chan struct{})
	fn := func() (any, error) {
		<-gate
		panic("boom")
	}
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e, _ := sf.Do("k", fn)
			errs[i] = e
		}(i)
	}
	time.Sleep(20 * time.Millisecond)
	close(gate)
	wg.Wait() // if panic deadlocks, this blocks forever (test timeout)
	for _, e := range errs {
		if e == nil || !strings.Contains(e.Error(), "panicked") {
			t.Fatalf("expected panic-error; got %v", e)
		}
	}
}

func TestSingleFlight_SharedFlagDistinguishesExecutorVsFollower(t *testing.T) {
	sf := NewSingleFlight()
	gate := make(chan struct{})
	fn := func() (any, error) {
		<-gate
		return "x", nil
	}
	type res struct {
		shared bool
	}
	results := make(chan res, 4)
	for i := 0; i < 4; i++ {
		go func() {
			_, _, s := sf.Do("k", fn)
			results <- res{shared: s}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(gate)
	executors, followers := 0, 0
	for i := 0; i < 4; i++ {
		r := <-results
		if r.shared {
			followers++
		} else {
			executors++
		}
	}
	if executors != 1 || followers != 3 {
		t.Fatalf("executors=%d followers=%d; want 1+3", executors, followers)
	}
}
