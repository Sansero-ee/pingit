package pinger

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Sansero-ee/pingit/internal/logwriter"
)

func TestLoopbackUnprivileged(t *testing.T) {
	targets := Resolve([]string{"127.0.0.1"})
	if len(targets) != 1 || targets[0].Err != nil {
		t.Fatalf("resolve failed: %+v", targets)
	}
	dir := t.TempDir()
	w, err := logwriter.Open(dir + "/log.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := Start(ctx, targets, 100*time.Millisecond, 800*time.Millisecond, w)
	<-run.Done()
	path := dir + "/log.jsonl"
	if sn := run.Snapshots()[0]; sn.Sent == 0 || sn.Received == 0 {
		if data, err := os.ReadFile(path); err == nil {
			t.Logf("log records:\n%s", data)
		}
		t.Fatalf("sent=%d received=%d lost=%d err=%q", sn.Sent, sn.Received, sn.Lost, sn.Err)
	}
}
