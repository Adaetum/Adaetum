package main

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/tlscert"
)

// serveTLSUpgradingHTTP serves HTTPS on server.Addr and answers a plain-HTTP request on
// the same port with a redirect to https, instead of Go's blunt "client sent an HTTP request
// to an HTTPS server". A person who types http://host:28443 lands on the page, and the HSTS
// header (see withHSTS) then makes the browser go straight to https next time.
//
// The first byte of a connection tells them apart: a TLS handshake always starts with 0x16,
// while an HTTP request starts with a method letter. Only the first byte is read here;
// everything else, including certificate handling and HTTP/2, is the normal TLS server's.
//
// ownName says whether a host name (or IP) is one this agent's certificate covers. A plain
// request is redirected only to such a name: the Host header is whatever the client sent,
// and a redirect to an arbitrary host would be an open redirect (and a 308 may be cached).
func serveTLSUpgradingHTTP(server *http.Server, ownName func(host string) bool) error {
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	return server.ServeTLS(newSniffListener(listener, plainRedirect(ownName)), "", "")
}

// plainRedirect sends a plain-HTTP request to the same host and path over https. The host
// is the one the client already used (it includes the port), so nothing is guessed.
func plainRedirect(ownName func(host string) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = strings.Trim(r.Host, "[]")
		}
		if host == "" || !ownName(host) {
			http.Error(w, "use https:// and this server's own name or address", http.StatusBadRequest)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		// 308 keeps the method and body, so a script that posted to http:// is not turned
		// into a GET without notice.
		http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusPermanentRedirect)
	})
}

// sniffListener hands TLS connections to its caller and answers the rest itself.
type sniffListener struct {
	net.Listener
	redirect http.Handler
	conns    chan net.Conn
	done     chan struct{}
	once     sync.Once
}

func newSniffListener(inner net.Listener, redirect http.Handler) *sniffListener {
	l := &sniffListener{Listener: inner, redirect: redirect, conns: make(chan net.Conn), done: make(chan struct{})}
	go l.acceptLoop()
	return l
}

func (l *sniffListener) acceptLoop() {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			l.Close()
			return
		}
		go l.sort(conn)
	}
}

// sort looks at the first byte, in its own goroutine so one slow client cannot hold up
// the others.
func (l *sniffListener) sort(conn net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	first, err := reader.Peek(1)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		conn.Close()
		return
	}
	replay := &replayConn{Conn: conn, reader: reader}
	if first[0] == 0x16 {
		select {
		case l.conns <- replay:
		case <-l.done:
			conn.Close()
		}
		return
	}
	// One request, one answer, then close: this connection exists only to send people
	// to the https address.
	_ = http.Serve(&singleConnListener{conn: replay}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		l.redirect.ServeHTTP(w, r)
	}))
}

func (l *sniffListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *sniffListener) Close() error {
	var err error
	l.once.Do(func() {
		close(l.done)
		err = l.Listener.Close()
	})
	return err
}

// replayConn reads the byte that was peeked before anything else.
type replayConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *replayConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// singleConnListener lets http.Serve handle exactly one connection and then stop.
type singleConnListener struct {
	conn net.Conn
	once sync.Once
	done chan struct{}
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() { conn = l.conn })
	if conn != nil {
		return &closeNotifyConn{Conn: conn, release: func() { l.close() }}, nil
	}
	<-l.closed()
	return nil, errors.New("single connection served")
}

func (l *singleConnListener) closed() chan struct{} {
	if l.done == nil {
		l.done = make(chan struct{})
	}
	return l.done
}

func (l *singleConnListener) close() {
	select {
	case <-l.closed():
	default:
		close(l.done)
	}
}

func (l *singleConnListener) Close() error   { l.close(); return nil }
func (l *singleConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

// closeNotifyConn tells the one-connection listener when its connection is done, so
// http.Serve returns and the goroutine does not linger.
type closeNotifyConn struct {
	net.Conn
	release func()
}

func (c *closeNotifyConn) Close() error { defer c.release(); return c.Conn.Close() }

// ownCertificateName reports whether a name or IP belongs to this machine: one its
// certificate covers, or a name its own addresses resolve to in reverse DNS (a tailnet's
// MagicDNS name, for instance, which the certificate does not list). The reverse names are
// looked up at most once a minute, and only when a plain-HTTP request asks, so a flood of
// them cannot become a flood of DNS queries.
func ownCertificateName(certificates *tlscert.Manager) func(string) bool {
	var (
		mutex     sync.Mutex
		refreshed time.Time
		reverse   = map[string]bool{}
	)
	return func(host string) bool {
		if _, leaf, err := certificates.Current(); err == nil && leaf.VerifyHostname(host) == nil {
			return true
		}
		mutex.Lock()
		defer mutex.Unlock()
		if time.Since(refreshed) > time.Minute {
			refreshed = time.Now()
			reverse = ownReverseNames()
		}
		return reverse[strings.ToLower(strings.TrimSuffix(host, "."))]
	}
}

// ownReverseNames is the names this machine's non-loopback addresses resolve to.
func ownReverseNames() map[string]bool {
	names := map[string]bool{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return names
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, addr := range addrs {
		network, ok := addr.(*net.IPNet)
		if !ok || network.IP.IsLoopback() || network.IP.IsLinkLocalUnicast() {
			continue
		}
		found, _ := net.DefaultResolver.LookupAddr(ctx, network.IP.String())
		for _, name := range found {
			names[strings.ToLower(strings.TrimSuffix(name, "."))] = true
		}
	}
	return names
}
