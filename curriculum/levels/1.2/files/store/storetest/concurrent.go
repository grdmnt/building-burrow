package storetest

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"

	"burrow/store"
)

// RunConcurrent checks that an engine stays correct under concurrent use.
// Run it with the race detector.
func RunConcurrent(t *testing.T, newStore Factory) {
	ctx := context.Background()

	t.Run("DisjointWriters", func(t *testing.T) {
		s := newStore(t)
		const workers, perWorker = 16, 200
		var wg sync.WaitGroup
		errs := make(chan error, workers)
		for w := range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range perWorker {
					k := fmt.Sprintf("w%02d-%04d", w, i)
					if _, err := s.Set(ctx, k, []byte(strconv.Itoa(i))); err != nil {
						errs <- err
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("concurrent Set failed: %v", err)
		}
		keys, err := s.Keys(ctx, "w")
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != workers*perWorker {
			t.Fatalf("after %d concurrent writes, Keys has %d entries", workers*perWorker, len(keys))
		}
		for w := range workers {
			for i := 0; i < perWorker; i += 37 {
				k := fmt.Sprintf("w%02d-%04d", w, i)
				v, err := s.Get(ctx, k)
				if err != nil || string(v.Value) != strconv.Itoa(i) {
					t.Fatalf("Get(%s) = (%q, %v), want %q", k, v.Value, err, strconv.Itoa(i))
				}
			}
		}
	})

	t.Run("MixedHammer", func(t *testing.T) {
		s := newStore(t)
		const workers, ops = 24, 600
		var wg sync.WaitGroup
		errs := make(chan error, workers)
		for w := range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range ops {
					k := fmt.Sprintf("hot-%d", (w*7+i)%32)
					var err error
					switch i % 5 {
					case 0, 1:
						_, err = s.Set(ctx, k, []byte(k))
					case 2:
						var v store.Entry
						v, err = s.Get(ctx, k)
						if err == nil && string(v.Value) != k {
							err = fmt.Errorf("Get(%s) returned %q: torn or mixed-up value", k, v.Value)
						}
					case 3:
						err = s.Delete(ctx, k)
					case 4:
						_, err = s.Keys(ctx, "hot-")
					}
					if err != nil && !errors.Is(err, store.ErrNotFound) {
						errs <- err
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
	})
}
