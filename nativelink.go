package main

import (
	"context"
	"net"
	"strconv"
	"sync"
	"time"
)

// nativeLink is the TCP 2222 connection the console pushes changes on.
type nativeLink struct {
	// guarded by Wing.mu
	connected bool
	since     time.Time
	lastRx    time.Time
	events    int
	unknown   int // events whose hash the add-on hasn't learned
	lastPaths []string
	lastErr   string

	wmu  sync.Mutex // guards conn writes and the encoder
	conn net.Conn
	enc  *nrpEncoder
}

// send writes payload on channel ch of the native link.
func (w *Wing) nativeSend(ch int, payload []byte) error {
	n := &w.native
	n.wmu.Lock()
	defer n.wmu.Unlock()
	if n.conn == nil {
		return net.ErrClosed
	}
	n.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err := n.conn.Write(n.enc.frame(ch, payload))
	return err
}

// nativeLive: events are flowing and can be mapped to paths, so polling
// isn't needed.
func (w *Wing) nativeLive() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.native.connected && len(w.hashes) > 0 && time.Since(w.native.lastRx) < 12*time.Second
}

// nativeLoop keeps the TCP link up while the console answers.
func (w *Wing) nativeLoop(ctx context.Context) {
	addr := net.JoinHostPort(w.cfg.Host, strconv.Itoa(w.cfg.NativePort))
	for ctx.Err() == nil {
		err := w.nativeSession(ctx, addr)
		w.mu.Lock()
		w.native.connected = false
		if err != nil && ctx.Err() == nil {
			w.native.lastErr = err.Error()
		}
		w.dirty = true
		w.mu.Unlock()
		if !sleepCtx(ctx, 5*time.Second) {
			return
		}
	}
}

func (w *Wing) nativeSession(ctx context.Context, addr string) error {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	n := &w.native
	n.wmu.Lock()
	n.conn, n.enc = conn, newNRPEncoder()
	n.wmu.Unlock()
	defer func() {
		n.wmu.Lock()
		n.conn = nil
		n.wmu.Unlock()
		conn.Close()
	}()
	// Select the audio-engine channel and ask for the root definition:
	// harmless, and its reply proves the link works. The same request
	// every 4 s keeps the link inside the console's 10 s idle timeout.
	keepalive := []byte{tokRoot, tokDefReq}
	if err := w.nativeSend(chanAudio, keepalive); err != nil {
		return err
	}
	w.mu.Lock()
	n.connected, n.since, n.lastRx, n.lastErr = true, time.Now(), time.Now(), ""
	w.dirty = true
	w.mu.Unlock()
	w.meters.resend() // a new link needs the meter request again

	errc := make(chan error, 1)
	go func() {
		dec := newNRPDecoder()
		p := &nrpParser{onValue: w.applyEvent}
		buf := make([]byte, 16384)
		for {
			conn.SetReadDeadline(time.Now().Add(15 * time.Second))
			k, err := conn.Read(buf)
			if k > 0 {
				w.mu.Lock()
				n.lastRx = time.Now()
				w.mu.Unlock()
				var chunk []byte
				dec.feed(buf[:k], func(ch int, db byte) {
					if ch == chanAudio {
						chunk = append(chunk, db)
					}
				})
				p.feed(chunk...)
			}
			if err != nil {
				errc <- err
				return
			}
		}
	}()
	t := time.NewTicker(4 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			conn.Close()
			<-errc
			return nil
		case err := <-errc:
			return err
		case <-t.C:
			if err := w.nativeSend(chanAudio, keepalive); err != nil {
				conn.Close()
				<-errc
				return err
			}
		}
	}
}
