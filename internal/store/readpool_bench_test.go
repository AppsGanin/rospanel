package store

import (
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// BenchmarkReadsUnderWrites is the shape of a busy panel: one writer landing
// connection batches back to back (the access tap and the stats poll do exactly this)
// while subscription fetches and the users page read. What it reports is how long a
// read waits — the thing a single shared connection makes every reader pay for every
// write in front of it.
//
//	go test -run '^$' -bench ReadsUnderWrites -benchtime 5s ./internal/store/
func BenchmarkReadsUnderWrites(b *testing.B) {
	const users = 5000
	st, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	tokens := make([]string, users)
	for i := range users {
		tokens[i] = fmt.Sprintf("tok%05d", i)
		if _, err := st.CreateUser(fmt.Sprintf("u%d", i), fmt.Sprintf("uuid-%d", i), "pw", tokens[i], 0, 0, 3); err != nil {
			b.Fatal(err)
		}
	}

	stop := make(chan struct{})
	var writes atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		now := time.Now().Unix()
		batch := make([]ConnectionHit, 2000)
		for round := 0; ; round++ {
			select {
			case <-stop:
				return
			default:
			}
			for i := range batch {
				batch[i] = ConnectionHit{
					UserID: int64(1 + (round*len(batch)+i)%users),
					IP:     fmt.Sprintf("10.%d.%d.%d", round%250, i/250, i%250),
					SeenAt: now, Hits: 1,
				}
			}
			if err := st.AddConnections(batch); err != nil {
				b.Error(err)
				return
			}
			writes.Add(1)
		}
	}()

	var mu sync.Mutex
	var lat []time.Duration
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		var mine []time.Duration
		for pb.Next() {
			start := time.Now()
			if i%20 == 0 {
				if _, err := st.ListUserSummaries(); err != nil {
					b.Error(err)
				}
			} else if _, err := st.GetUserBySubToken(tokens[(i*7919)%users]); err != nil {
				b.Error(err)
			}
			mine = append(mine, time.Since(start))
			i++
		}
		mu.Lock()
		lat = append(lat, mine...)
		mu.Unlock()
	})
	b.StopTimer()
	close(stop)
	wg.Wait()

	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	pct := func(p float64) time.Duration { return lat[int(float64(len(lat)-1)*p)] }
	b.ReportMetric(float64(pct(0.50).Microseconds()), "p50-µs")
	b.ReportMetric(float64(pct(0.99).Microseconds()), "p99-µs")
	b.ReportMetric(float64(writes.Load()), "write-batches")
}

// BenchmarkWritesUnderPacedReads holds the reads to a steady rate, the way requests
// arrive, and reports what the writer gets done in the meantime alongside how long
// the reads took. The flood above answers "how long does a read wait"; this one
// answers "what does letting reads run cost the writer".
//
//	go test -run '^$' -bench WritesUnderPacedReads -benchtime 1x ./internal/store/
func BenchmarkWritesUnderPacedReads(b *testing.B) {
	const (
		users    = 5000
		readers  = 4
		interval = 20 * time.Millisecond // 4 readers × 50/s = 200 reads a second
		window   = 5 * time.Second
	)
	st, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	tokens := make([]string, users)
	for i := range users {
		tokens[i] = fmt.Sprintf("tok%05d", i)
		if _, err := st.CreateUser(fmt.Sprintf("u%d", i), fmt.Sprintf("uuid-%d", i), "pw", tokens[i], 0, 0, 3); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for range b.N {
		stop := make(chan struct{})
		var writes atomic.Int64
		var mu sync.Mutex
		var lat []time.Duration
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			now := time.Now().Unix()
			batch := make([]ConnectionHit, 2000)
			for round := 0; ; round++ {
				select {
				case <-stop:
					return
				default:
				}
				for i := range batch {
					batch[i] = ConnectionHit{
						UserID: int64(1 + (round*len(batch)+i)%users),
						IP:     fmt.Sprintf("10.%d.%d.%d", round%250, i/250, i%250),
						SeenAt: now, Hits: 1,
					}
				}
				if err := st.AddConnections(batch); err != nil {
					b.Error(err)
					return
				}
				writes.Add(1)
			}
		}()
		for r := range readers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tick := time.NewTicker(interval)
				defer tick.Stop()
				for i := r; ; i += readers {
					select {
					case <-stop:
						return
					case <-tick.C:
					}
					start := time.Now()
					if _, err := st.GetUserBySubToken(tokens[(i*7919)%users]); err != nil {
						b.Error(err)
					}
					mu.Lock()
					lat = append(lat, time.Since(start))
					mu.Unlock()
				}
			}()
		}
		time.Sleep(window)
		close(stop)
		wg.Wait()
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		pct := func(p float64) time.Duration { return lat[int(float64(len(lat)-1)*p)] }
		b.ReportMetric(float64(writes.Load())/window.Seconds(), "write-batches/s")
		b.ReportMetric(float64(len(lat))/window.Seconds(), "reads/s")
		b.ReportMetric(float64(pct(0.50).Microseconds()), "read-p50-µs")
		b.ReportMetric(float64(pct(0.99).Microseconds()), "read-p99-µs")
	}
}
