package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseTarget(t *testing.T) {
	ok := map[string]string{
		"db:5432":                "tcp://db:5432",
		"tcp://10.0.0.1:80":      "tcp://10.0.0.1:80",
		":8080":                  "tcp://localhost:8080",
		"[::1]:9000":             "tcp://[::1]:9000",
		"http://api/healthz":     "http://api/healthz",
		"https://x.example/":     "https://x.example/",
		"file:///tmp/ready.flag": "file:///tmp/ready.flag",
	}
	for in, want := range ok {
		p, err := ParseTarget(in, false)
		if err != nil || p.String() != want {
			t.Errorf("%s: got %v %v, want %s", in, p, err, want)
		}
	}
	for _, bad := range []string{"db", "ftp://x:21", "file://"} {
		if _, err := ParseTarget(bad, false); err == nil {
			t.Errorf("%s: expected error", bad)
		}
	}
}

func TestTCPBecomesReady(t *testing.T) {
	// reserve a port, close it, and open it again a bit later
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	go func() {
		time.Sleep(300 * time.Millisecond)
		l2, err := net.Listen("tcp", addr)
		if err != nil {
			return
		}
		defer l2.Close()
		time.Sleep(2 * time.Second)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	p, _ := ParseTarget(addr, false)
	fails := 0
	if err := WaitOne(ctx, p, 50*time.Millisecond, time.Second, func(error) { fails++ }); err != nil {
		t.Fatal(err)
	}
	if fails == 0 {
		t.Fatal("expected at least one failed attempt before success")
	}
}

func TestHTTPStatusMatters(t *testing.T) {
	var healthy atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			http.Error(w, "starting", 503)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	p, _ := ParseTarget(srv.URL, false)
	if err := p.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("expected 503 error, got %v", err)
	}
	time.AfterFunc(200*time.Millisecond, func() { healthy.Store(true) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := WaitOne(ctx, p, 50*time.Millisecond, time.Second, nil); err != nil {
		t.Fatal(err)
	}
}

func TestTimeoutReportsLastError(t *testing.T) {
	p, _ := ParseTarget("file://"+filepath.Join(t.TempDir(), "never"), false)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err := WaitOne(ctx, p, 30*time.Millisecond, time.Second, nil)
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitAllAndAny(t *testing.T) {
	dir := t.TempDir()
	exists := filepath.Join(dir, "yes")
	os.WriteFile(exists, nil, 0o644)
	good, _ := ParseTarget("file://"+exists, false)
	bad, _ := ParseTarget("file://"+filepath.Join(dir, "no"), false)
	quiet := func(string, ...any) {}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := waitAll(ctx, []Probe{good, bad}, true, 20*time.Millisecond, time.Second, quiet); err != nil {
		t.Fatalf("any mode should succeed: %v", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	if err := waitAll(ctx2, []Probe{good, bad}, false, 20*time.Millisecond, time.Second, quiet); err == nil {
		t.Fatal("all mode should fail when one target never appears")
	}
}

func TestSplitCommand(t *testing.T) {
	ts, cmd := splitCommand([]string{"db:1", "-t", "5s", "--", "echo", "--", "x"})
	if len(ts) != 3 || len(cmd) != 3 || cmd[1] != "--" {
		t.Fatalf("got %v / %v", ts, cmd)
	}
}
