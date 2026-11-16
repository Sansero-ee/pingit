package logwriter

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type Record struct {
	Timestamp  time.Time `json:"timestamp"`
	Target     string    `json:"target"`
	ResolvedIP string    `json:"resolved_ip"`
	Seq        int       `json:"seq"`
	RTTMs      float64   `json:"rtt_ms"`
	Success    bool      `json:"success"`
	Error      string    `json:"error,omitempty"`
}

const (
	queueSize       = 8192
	flushThreshold  = 128
	retryDelay      = 100 * time.Millisecond
	finalFlushLimit = 30 * time.Second
)

type Writer struct {
	ch      chan Record
	closed  chan struct{}
	closeMu sync.Mutex
	wg      sync.WaitGroup
	out     io.Writer
	dropped atomic.Int64
	mu      sync.Mutex
	lastErr error
}

func Open(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return NewWithWriter(f), nil
}

func NewWithWriter(out io.Writer) *Writer {
	w := &Writer{
		ch:     make(chan Record, queueSize),
		closed: make(chan struct{}),
		out:    out,
	}
	w.wg.Add(1)
	go w.loop()
	return w
}

func (w *Writer) Write(r Record) {
	select {
	case w.ch <- r:
	default:
		w.dropped.Add(1)
	}
}

func (w *Writer) Dropped() int64 { return w.dropped.Load() }

func (w *Writer) LastError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastErr
}

func (w *Writer) setLastErr(err error) {
	w.mu.Lock()
	w.lastErr = err
	w.mu.Unlock()
}

func (w *Writer) Close() error {
	w.closeMu.Lock()
	select {
	case <-w.closed:
	default:
		close(w.closed)
	}
	w.closeMu.Unlock()
	w.wg.Wait()
	return w.LastError()
}

func (w *Writer) loop() {
	defer w.wg.Done()
	var pending []Record
	var retryC <-chan time.Time
	for {
		if len(pending) > 0 && retryC == nil {
			retryC = time.After(retryDelay)
		}
		select {
		case r := <-w.ch:
			pending = append(pending, r)
			if len(pending) >= flushThreshold && w.flush(pending) {
				pending = pending[:0]
			}
		case <-retryC:
			retryC = nil
			if w.flush(pending) {
				pending = pending[:0]
			}
		case <-w.closed:
			w.drainAndClose(&pending)
			return
		}
	}
}

func (w *Writer) drainAndClose(pending *[]Record) {
	for {
		select {
		case r := <-w.ch:
			*pending = append(*pending, r)
			continue
		default:
		}
		break
	}
	limit := time.Now().Add(finalFlushLimit)
	for len(*pending) > 0 {
		if w.flush(*pending) {
			*pending = nil
			break
		}
		if !time.Now().Before(limit) {
			break
		}
		time.Sleep(retryDelay)
	}
	if f, ok := w.out.(*os.File); ok {
		f.Sync()
		f.Close()
	}
}

func (w *Writer) flush(recs []Record) bool {
	if len(recs) == 0 {
		return true
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for i := range recs {
		if err := enc.Encode(&recs[i]); err != nil {
			w.setLastErr(err)
			return false
		}
	}
	if _, err := w.out.Write(buf.Bytes()); err != nil {
		w.setLastErr(err)
		return false
	}
	return true
}
