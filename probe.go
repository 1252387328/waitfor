package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Probe checks one dependency once.
type Probe interface {
	String() string
	Check(ctx context.Context) error
}

type tcpProbe struct{ addr string }

func (p tcpProbe) String() string { return "tcp://" + p.addr }

func (p tcpProbe) Check(ctx context.Context) error {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", p.addr)
	if err != nil {
		return err
	}
	return c.Close()
}

type httpProbe struct {
	url      string
	insecure bool
}

func (p httpProbe) String() string { return p.url }

func (p httpProbe) Check(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "waitfor/"+version)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if p.insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // explicit opt-in
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

type fileProbe struct{ path string }

func (p fileProbe) String() string { return "file://" + p.path }

func (p fileProbe) Check(context.Context) error {
	_, err := os.Stat(p.path)
	return err
}

// ParseTarget turns "db:5432", "tcp://db:5432", "http://api/health" or "file:///tmp/ready" into a Probe.
func ParseTarget(s string, insecure bool) (Probe, error) {
	switch {
	case strings.HasPrefix(s, "http://"), strings.HasPrefix(s, "https://"):
		if _, err := url.ParseRequestURI(s); err != nil {
			return nil, fmt.Errorf("bad url %q: %w", s, err)
		}
		return httpProbe{url: s, insecure: insecure}, nil
	case strings.HasPrefix(s, "file://"):
		p := strings.TrimPrefix(s, "file://")
		if p == "" {
			return nil, fmt.Errorf("empty file path in %q", s)
		}
		return fileProbe{path: p}, nil
	case strings.HasPrefix(s, "tcp://"):
		s = strings.TrimPrefix(s, "tcp://")
	case strings.Contains(s, "://"):
		return nil, fmt.Errorf("unsupported scheme in %q (use tcp://, http://, https:// or file://)", s)
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return nil, fmt.Errorf("bad address %q: want host:port", s)
	}
	if host == "" {
		host = "localhost"
	}
	return tcpProbe{addr: net.JoinHostPort(host, port)}, nil
}

// WaitOne polls p until it succeeds or ctx ends. It returns the last error on timeout.
func WaitOne(ctx context.Context, p Probe, interval, attemptTimeout time.Duration, onFail func(error)) error {
	for {
		actx, cancel := context.WithTimeout(ctx, attemptTimeout)
		err := p.Check(actx)
		cancel()
		if err == nil {
			return nil
		}
		if onFail != nil {
			onFail(err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w (last error: %v)", p, ctx.Err(), err)
		case <-time.After(interval):
		}
	}
}
