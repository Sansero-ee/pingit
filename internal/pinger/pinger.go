package pinger

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"github.com/Sansero-ee/pingit/internal/logwriter"
)

const (
	icmpProto   = 1
	icmpv6Proto = 58
)

type Target struct {
	Name string
	IP   net.IP
	Err  error
}

func Resolve(names []string) []Target {
	out := make([]Target, 0, len(names))
	for _, name := range names {
		t := Target{Name: name}
		if ip := net.ParseIP(name); ip != nil {
			t.IP = ip
		} else if ips, err := net.LookupIP(name); err != nil {
			t.Err = fmt.Errorf("lookup %q: %w", name, err)
		} else if len(ips) == 0 {
			t.Err = fmt.Errorf("lookup %q: no addresses found", name)
		} else {
			t.IP = pickIP(ips)
		}
		out = append(out, t)
	}
	return out
}

func pickIP(ips []net.IP) net.IP {
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip
		}
	}
	return ips[0]
}

type Snapshot struct {
	Target   string
	IP       string
	Err      string
	Sent     int
	Received int
	Lost     int
	LostPct  float64
	MinMs    float64
	AvgMs    float64
	MaxMs    float64
}

type Stats struct {
	mu       sync.Mutex
	sent     int
	received int
	lost     int
	min, max time.Duration
	sum      time.Duration
}

func (s *Stats) addSent() {
	s.mu.Lock()
	s.sent++
	s.mu.Unlock()
}

func (s *Stats) addLost() {
	s.mu.Lock()
	s.lost++
	s.mu.Unlock()
}

func (s *Stats) addReceived(rtt time.Duration) {
	s.mu.Lock()
	s.received++
	s.sum += rtt
	if s.received == 1 || rtt < s.min {
		s.min = rtt
	}
	if rtt > s.max {
		s.max = rtt
	}
	s.mu.Unlock()
}

func (s *Stats) snapshot(t Target) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn := Snapshot{Target: t.Name, Sent: s.sent, Received: s.received, Lost: s.lost}
	if t.Err != nil {
		sn.Err = t.Err.Error()
	}
	if t.IP != nil {
		sn.IP = t.IP.String()
	}
	if s.sent > 0 {
		sn.LostPct = float64(s.lost) / float64(s.sent) * 100
	}
	if s.received > 0 {
		sn.MinMs = msDuration(s.min)
		sn.AvgMs = msDuration(s.sum) / float64(s.received)
		sn.MaxMs = msDuration(s.max)
	}
	return sn
}

func msDuration(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

type Run struct {
	wg      sync.WaitGroup
	done    chan struct{}
	targets []Target
	stats   []*Stats
}

func Start(ctx context.Context, targets []Target, interval, duration time.Duration, log *logwriter.Writer) *Run {
	r := &Run{done: make(chan struct{}), targets: targets}
	for _, t := range targets {
		st := &Stats{}
		r.stats = append(r.stats, st)
		p := &pinger{target: t, interval: interval, duration: duration, log: log, stats: st}
		r.wg.Add(1)
		go p.run(ctx, &r.wg)
	}
	go func() {
		r.wg.Wait()
		close(r.done)
	}()
	return r
}

func (r *Run) Done() <-chan struct{} { return r.done }

func (r *Run) Wait() { r.wg.Wait() }

func (r *Run) Snapshots() []Snapshot {
	out := make([]Snapshot, len(r.stats))
	for i, st := range r.stats {
		out[i] = st.snapshot(r.targets[i])
	}
	return out
}

type pinger struct {
	target   Target
	interval time.Duration
	duration time.Duration
	log      *logwriter.Writer
	stats    *Stats
}

func newConn(ip net.IP) (conn *icmp.PacketConn, privileged bool, err error) {
	if ip.To4() != nil {
		if c, e := icmp.ListenPacket("ip4:icmp", "0.0.0.0"); e == nil {
			return c, true, nil
		}
		c, e := icmp.ListenPacket("udp4", "0.0.0.0")
		return c, false, e
	}
	if c, e := icmp.ListenPacket("ip6:ipv6-icmp", "::"); e == nil {
		return c, true, nil
	}
	c, e := icmp.ListenPacket("udp6", "::")
	return c, false, e
}

func (p *pinger) run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	name := p.target.Name
	if p.target.Err != nil {
		p.log.Write(failRecord(name, "", 0, "resolve: "+p.target.Err.Error()))
		return
	}
	ip := p.target.IP
	conn, privileged, err := newConn(ip)
	if err != nil {
		p.log.Write(failRecord(name, ip.String(), 0, "icmp socket: "+err.Error()))
		return
	}
	defer conn.Close()

	proto := icmpProto
	reqType := icmp.Type(ipv4.ICMPTypeEcho)
	replyType := icmp.Type(ipv4.ICMPTypeEchoReply)
	if ip.To4() == nil {
		proto = icmpv6Proto
		reqType = ipv6.ICMPTypeEchoRequest
		replyType = ipv6.ICMPTypeEchoReply
	}

	// Non-privileged (SOCK_DGRAM) ICMP endpoints reject *net.IPAddr and
	// only accept *net.UDPAddr destinations; raw-socket endpoints the
	// reverse.
	dst := net.Addr(&net.IPAddr{IP: ip})
	if !privileged {
		dst = &net.UDPAddr{IP: ip}
	}

	var token [8]byte
	if _, err := rand.Read(token[:]); err != nil {
		binary.BigEndian.PutUint64(token[:], uint64(time.Now().UnixNano()))
	}

	id := os.Getpid() & 0xffff
	pending := make(map[int]time.Time)
	seq := 0
	timeout := 3 * p.interval
	deadline := time.Now().Add(p.duration)
	nextSend := time.Now()
	buf := make([]byte, 1500)

	send := func() {
		seq++
		msg := icmp.Message{
			Type: reqType,
			Code: 0,
			Body: &icmp.Echo{ID: id, Seq: seq, Data: append([]byte(nil), token[:]...)},
		}
		wb, err := msg.Marshal(nil)
		if err != nil {
			p.log.Write(failRecord(name, ip.String(), seq, "marshal: "+err.Error()))
			return
		}
		if _, err := conn.WriteTo(wb, dst); err != nil {
			p.log.Write(failRecord(name, ip.String(), seq, "send: "+err.Error()))
			return
		}
		p.stats.addSent()
		pending[seq] = time.Now()
	}

	expire := func(now time.Time, maxAge time.Duration) {
		for s, t0 := range pending {
			if now.Sub(t0) >= maxAge {
				delete(pending, s)
				p.stats.addLost()
				p.log.Write(failRecord(name, ip.String(), s, "timeout"))
			}
		}
	}

	handle := func(b []byte) {
		m, err := icmp.ParseMessage(proto, b)
		if err != nil || m.Type != replyType {
			return
		}
		echo, ok := m.Body.(*icmp.Echo)
		if !ok || !bytes.HasPrefix(echo.Data, token[:]) {
			return
		}
		t0, ok := pending[echo.Seq]
		if !ok {
			return
		}
		delete(pending, echo.Seq)
		rtt := time.Since(t0)
		p.stats.addReceived(rtt)
		p.log.Write(okRecord(name, ip.String(), echo.Seq, rtt))
	}

	for {
		now := time.Now()
		if ctx.Err() != nil || !now.Before(deadline) {
			break
		}
		if !now.Before(nextSend) {
			expire(now, timeout)
			if !now.Before(deadline) {
				break
			}
			send()
			nextSend = nextSend.Add(p.interval)
			if nextSend.Before(now) {
				nextSend = now.Add(p.interval)
			}
			continue
		}
		conn.SetReadDeadline(now.Add(nextSend.Sub(now)))
		n, _, err := conn.ReadFrom(buf)
		if err == nil {
			handle(buf[:n])
			continue
		}
		if ctx.Err() != nil {
			break
		}
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			select {
			case <-ctx.Done():
			case <-time.After(10 * time.Millisecond):
			}
		}
	}

	drainEnd := time.Now().Add(2 * p.interval)
	for len(pending) > 0 && ctx.Err() == nil {
		if !time.Now().Before(drainEnd) {
			break
		}
		conn.SetReadDeadline(drainEnd)
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			break
		}
		handle(buf[:n])
	}
	expire(time.Now(), 0)
}

func okRecord(target, ip string, seq int, rtt time.Duration) logwriter.Record {
	return logwriter.Record{
		Timestamp:  time.Now(),
		Target:     target,
		ResolvedIP: ip,
		Seq:        seq,
		RTTMs:      float64(rtt) / float64(time.Millisecond),
		Success:    true,
	}
}

func failRecord(target, ip string, seq int, msg string) logwriter.Record {
	return logwriter.Record{
		Timestamp:  time.Now(),
		Target:     target,
		ResolvedIP: ip,
		Seq:        seq,
		Success:    false,
		Error:      msg,
	}
}
