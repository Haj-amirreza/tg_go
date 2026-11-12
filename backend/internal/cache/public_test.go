package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSlowMissDoesNotBlockOtherKeys(t *testing.T) {
	c, _ := New(4, 100, "")
	ctx := context.Background()
	c.Get(ctx, "cached", time.Minute, func(context.Context) ([]byte, error) { return []byte("ready"), nil })
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		c.Get(ctx, "slow", time.Minute, func(context.Context) ([]byte, error) {
			close(started)
			<-release
			return []byte("slow"), nil
		})
	}()
	<-started
	defer func() { close(release); <-finished }()
	for _, key := range []string{"cached", "other"} {
		result := make(chan error, 1)
		go func() {
			_, err := c.Get(ctx, key, time.Minute, func(context.Context) ([]byte, error) { return []byte("other"), nil })
			result <- err
		}()
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("independent key blocked by slow loader")
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	waiting := make(chan error, 1)
	go func() {
		_, err := c.Get(ctx, "slow", time.Minute, func(context.Context) ([]byte, error) {
			return nil, errors.New("duplicate loader")
		})
		waiting <- err
	}()
	cancel()
	select {
	case err := <-waiting:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled waiter", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter blocked by loader")
	}
}

func TestClearDiscardsInflightLoad(t *testing.T) {
	c, _ := New(2, 100, "")
	ctx := context.Background()
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		c.Get(ctx, "key", time.Minute, func(context.Context) ([]byte, error) {
			close(started)
			<-release
			return []byte("old"), nil
		})
	}()
	<-started
	var releaseOnce sync.Once
	releaseLoad := func() { releaseOnce.Do(func() { close(release) }); <-finished }
	defer releaseLoad()
	cleared := make(chan error, 1)
	go func() { cleared <- c.Clear() }()
	select {
	case err := <-cleared:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("clear blocked by loader")
	}
	loadNew := func(context.Context) ([]byte, error) { return []byte("new"), nil }
	value, err := c.Get(ctx, "key", time.Minute, loadNew)
	if err != nil || string(value) != "new" {
		t.Fatal("clear reused inflight value", err)
	}
	releaseLoad()
	value, err = c.Get(ctx, "key", time.Minute, loadNew)
	if err != nil || string(value) != "new" {
		t.Fatal("old load repopulated cleared cache", err)
	}
}

type waitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *waitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestLiveWaiterRetriesFailedLoader(t *testing.T) {
	for _, loadErr := range []error{context.Canceled, context.DeadlineExceeded, errors.New("failed"), nil} {
		name := "panic"
		if loadErr != nil {
			name = loadErr.Error()
		}
		t.Run(name, func(t *testing.T) {
			c, _ := New(2, 100, "")
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			releaseLoad := func() { releaseOnce.Do(func() { close(release) }) }
			defer releaseLoad()
			leader := make(chan any, 1)
			go func() {
				defer func() { leader <- recover() }()
				c.Get(context.Background(), "key", time.Minute, func(context.Context) ([]byte, error) {
					close(started)
					<-release
					if loadErr == nil {
						panic("loader failed")
					}
					return nil, loadErr
				})
			}()
			<-started
			ctx := &waitingContext{Context: context.Background(), waiting: make(chan struct{})}
			result := make(chan error, 1)
			go func() {
				value, err := c.Get(ctx, "key", time.Minute, func(context.Context) ([]byte, error) { return []byte("retried"), nil })
				if err == nil && string(value) != "retried" {
					err = errors.New("wrong retry result")
				}
				result <- err
			}()
			select {
			case <-ctx.waiting:
			case <-time.After(time.Second):
				t.Fatal("waiter did not join loader")
			}
			releaseLoad()
			if recovered := <-leader; (loadErr == nil) != (recovered == "loader failed") {
				t.Fatal("loader panic was not preserved", recovered)
			}
			select {
			case err := <-result:
				if err != nil {
					t.Fatal("live waiter inherited failed loader", err)
				}
			case <-time.After(time.Second):
				t.Fatal("live waiter did not retry")
			}
		})
	}
}

func TestBoundedCacheAndStampede(t *testing.T) {
	c, _ := New(2, 20, "")
	var calls atomic.Int32
	load := func(context.Context) ([]byte, error) { calls.Add(1); return []byte("hello"), nil }
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := c.Get(context.Background(), "key", time.Minute, load)
			if e != nil || string(v) != "hello" {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 || c.Stats().Hits != 19 {
		t.Fatal("stampede")
	}
	for _, k := range []string{"b", "c", "d"} {
		c.Get(context.Background(), k, time.Minute, load)
	}
	if c.Stats().Entries > 2 || c.Stats().Bytes > 20 {
		t.Fatal("budget")
	}
	c.Clear()
	c.Get(context.Background(), "key", time.Minute, load)
	if calls.Load() != 5 {
		t.Fatal("clear")
	}
}
func TestPersistenceInvalidationAndTTL(t *testing.T) {
	dir := t.TempDir()
	a, _ := New(5, 1000, dir)
	b, _ := New(5, 1000, dir)
	n := 0
	load := func(context.Context) ([]byte, error) { n++; return []byte("value"), nil }
	ctx := context.Background()
	a.Get(ctx, "key", time.Minute, load)
	b.Get(ctx, "key", time.Minute, load)
	if n != 1 {
		t.Fatal("not persistent")
	}
	b.Clear()
	a.Get(ctx, "key", time.Millisecond, load)
	time.Sleep(3 * time.Millisecond)
	a.Get(ctx, "key", time.Minute, load)
	if n != 3 {
		t.Fatal("TTL or cross process invalidation")
	}
	r := Request(ctx)
	RequestGet(r, "x", func() ([]byte, error) { n++; return []byte("a"), nil })
	RequestGet(r, "x", func() ([]byte, error) { n++; return []byte("b"), nil })
	if n != 4 {
		t.Fatal("request cache")
	}
}

func TestRequestCoalescesConcurrentLoads(t *testing.T) {
	ctx := Request(context.Background())
	var calls atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			RequestGet(ctx, "same", func() ([]byte, error) { calls.Add(1); return []byte("value"), nil })
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("request repeated load")
	}
}
