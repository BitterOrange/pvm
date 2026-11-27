package gateway

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// A flow routed to a Mihomo exit must ask Mihomo for the VM's destination
// port, not for Mihomo's own listen port.
func TestDialProxyMihomoTarget(t *testing.T) {
	socks, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socks.Close()
	type target struct {
		domain string
		port   int
	}
	got := make(chan target, 1)
	go func() {
		c, err := socks.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		greet := make([]byte, 3)
		if _, err := io.ReadFull(c, greet); err != nil {
			return
		}
		c.Write([]byte{5, 0})
		hdr := make([]byte, 5)
		if _, err := io.ReadFull(c, hdr); err != nil {
			return
		}
		rest := make([]byte, int(hdr[4])+2)
		if _, err := io.ReadFull(c, rest); err != nil {
			return
		}
		got <- target{string(rest[:hdr[4]]), int(binary.BigEndian.Uint16(rest[hdr[4]:]))}
		c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		time.Sleep(200 * time.Millisecond)
	}()

	eng := newFlipTestPolicy(t)
	eng.SetVMExit("dev1", "my-exit")
	p := newFakeProxy(eng, "10.0.2.3", nil)
	p.SetExitHooks(func(string) int { return mihomoMark }, nil, nil)
	mihomoPort := socks.Addr().(*net.TCPAddr).Port
	p.SetMihomoHook(func(string) (int, bool) { return mihomoPort, true })

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	client, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	src, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	go p.dialProxy(src.(*net.TCPConn), "example.com", 443, "my-exit", "dev1")

	select {
	case tg := <-got:
		if tg.domain != "example.com" || tg.port != 443 {
			t.Fatalf("Mihomo was asked for %s:%d, want example.com:443", tg.domain, tg.port)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no SOCKS request reached Mihomo")
	}
}
