package logwriter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func readRecords(t *testing.T, path string) []Record {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var recs []Record
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var r Record
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			t.Fatalf("invalid JSON line %q: %v", scanner.Text(), err)
		}
		recs = append(recs, r)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return recs
}

func TestConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.jsonl")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 16
	const perWorker = 100
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				w.Write(Record{
					Timestamp:  time.Now(),
					Target:     fmt.Sprintf("target-%d", id),
					ResolvedIP: "10.0.0.1",
					Seq:        i,
					RTTMs:      1.5,
					Success:    true,
				})
			}
		}(g)
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	recs := readRecords(t, path)
	if len(recs) != workers*perWorker {
		t.Fatalf("got %d records, want %d", len(recs), workers*perWorker)
	}
	counts := map[string]int{}
	for _, r := range recs {
		if !r.Success {
			t.Fatalf("unexpected failed record: %+v", r)
		}
		counts[r.Target]++
	}
	for g := 0; g < workers; g++ {
		name := fmt.Sprintf("target-%d", g)
		if counts[name] != perWorker {
			t.Fatalf("target %s: got %d records, want %d", name, counts[name], perWorker)
		}
	}
}

type flakySink struct {
	mu       sync.Mutex
	failLeft int
	failures int
	buf      bytes.Buffer
}

func (s *flakySink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failLeft > 0 {
		s.failLeft--
		s.failures++
		return 0, errors.New("simulated disk failure")
	}
	return s.buf.Write(p)
}

func TestRetryAfterWriteErrors(t *testing.T) {
	sink := &flakySink{failLeft: 3}
	w := NewWithWriter(sink)
	w.Write(Record{Timestamp: time.Now(), Target: "a", Seq: 1, Success: true, RTTMs: 2})
	w.Write(Record{Timestamp: time.Now(), Target: "b", Seq: 2, Success: false, Error: "timeout"})
	closeErr := w.Close()
	if closeErr == nil {
		t.Fatal("expected close to report the write error")
	}
	var got []Record
	for _, line := range strings.Split(strings.TrimSpace(sink.buf.String()), "\n") {
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("invalid JSON line %q: %v", line, err)
		}
		got = append(got, r)
	}
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	if got[0].Target != "a" || got[1].Target != "b" {
		t.Fatalf("unexpected records: %+v", got)
	}
	if sink.failures == 0 {
		t.Fatal("expected write failures to be exercised")
	}
	if w.LastError() == nil {
		t.Fatal("expected write error to be recorded")
	}
	if w.Dropped() != 0 {
		t.Fatalf("dropped = %d, want 0", w.Dropped())
	}
}

func TestCloseFlushesPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.jsonl")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	const n = 10
	for i := 0; i < n; i++ {
		w.Write(Record{Timestamp: time.Now(), Target: "t", Seq: i, Success: true})
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	recs := readRecords(t, path)
	if len(recs) != n {
		t.Fatalf("got %d records, want %d", len(recs), n)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	w, err := Open(filepath.Join(t.TempDir(), "log.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	w.Write(Record{Timestamp: time.Now(), Target: "t", Success: true})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = w.Close()
		}()
	}
	wg.Wait()
}
