package verification

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"
	"time"
)

func TestPublicEgressAddressRejectsNonPublicAndReservedRanges(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.2.3.4", "100.64.1.2", "169.254.10.1", "192.0.2.10", "198.18.0.1", "203.0.113.5", "224.0.0.1", "::1", "fc00::1", "fe80::1", "64:ff9b::a00:1", "2001:db8::1", "2001:4860:4860::8888"} {
		got := publicEgressAddress(netip.MustParseAddr(raw))
		want := raw == "2001:4860:4860::8888"
		if got != want {
			t.Errorf("publicEgressAddress(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestEgressRelayRejectsInvalidEndpointBeforeBinding(t *testing.T) {
	if err := RunEgressRelay(context.Background(), "", "0.0.0.0:80", ""); err == nil {
		t.Fatal("invalid relay endpoint accepted")
	}
}

func TestClientHelloSNIExtractsOnlyValidDNSHost(t *testing.T) {
	record := testClientHello("api.example.com")
	if got, err := clientHelloSNI(record); err != nil || got != "api.example.com" {
		t.Fatalf("SNI=(%q,%v)", got, err)
	}
	if _, err := clientHelloSNI(testClientHello("localhost")); err == nil {
		t.Fatal("local SNI accepted")
	}
	if _, err := clientHelloSNI([]byte{1, 0, 0, 1}); err == nil {
		t.Fatal("truncated ClientHello accepted")
	}
}

func testClientHello(host string) []byte {
	name := []byte(host)
	sni := make([]byte, 5+len(name))
	binary.BigEndian.PutUint16(sni, uint16(3+len(name)))
	sni[2] = 0
	binary.BigEndian.PutUint16(sni[3:], uint16(len(name)))
	copy(sni[5:], name)
	extensions := make([]byte, 4+len(sni))
	binary.BigEndian.PutUint16(extensions, 0)
	binary.BigEndian.PutUint16(extensions[2:], uint16(len(sni)))
	copy(extensions[4:], sni)
	body := make([]byte, 34+1+2+2+1+1+2+len(extensions))
	offset := 34
	body[offset] = 0
	offset++
	binary.BigEndian.PutUint16(body[offset:], 2)
	offset += 2
	binary.BigEndian.PutUint16(body[offset:], 0x1301)
	offset += 2
	body[offset] = 1
	offset++
	body[offset] = 0
	offset++
	binary.BigEndian.PutUint16(body[offset:], uint16(len(extensions)))
	offset += 2
	copy(body[offset:], extensions)
	handshake := make([]byte, 4+len(body))
	handshake[0] = 1
	handshake[1] = byte(len(body) >> 16)
	handshake[2] = byte(len(body) >> 8)
	handshake[3] = byte(len(body))
	copy(handshake[4:], body)
	return handshake
}

func TestEgressBrokerRejectsNonAllowlistedCONNECT(t *testing.T) {
	request := httptest.NewRequest(http.MethodConnect, "http://evil.example.com:443", nil)
	request.Host = "evil.example.com:443"
	response := httptest.NewRecorder()
	brokerHandler(map[string]bool{"api.example.com:443": true}).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unexpected broker status: %d", response.Code)
	}
}

func TestEgressBrokerRejectsAllowlistedHostResolvingToPrivateIP(t *testing.T) {
	called := false
	request := httptest.NewRequest(http.MethodConnect, "http://api.example.com:443", nil)
	request.Host = "api.example.com:443"
	response := httptest.NewRecorder()
	brokerHandlerWith(map[string]bool{"api.example.com:443": true}, func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.1.2.3")}, nil
	}, func(context.Context, string) (net.Conn, error) {
		called = true
		return nil, nil
	}).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || called {
		t.Fatalf("private DNS result was not blocked: status=%d dialed=%v", response.Code, called)
	}
}

func TestEgressBrokerTunnelsOnlyMatchingTLSHost(t *testing.T) {
	for _, tc := range []struct {
		name, sni  string
		wantTunnel bool
	}{{"matching SNI", "api.example.com", true}, {"different SNI", "other.example.com", false}} {
		t.Run(tc.name, func(t *testing.T) {
			clientConn, brokerConn := net.Pipe()
			defer clientConn.Close()
			upstreamConn, brokerUpstream := net.Pipe()
			defer upstreamConn.Close()
			defer brokerUpstream.Close()
			writer := &hijackResponseWriter{conn: brokerConn, rw: bufio.NewReadWriter(bufio.NewReader(brokerConn), bufio.NewWriter(brokerConn))}
			dialed := ""
			handler := brokerHandlerWith(map[string]bool{"api.example.com:443": true}, func(context.Context, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
			}, func(_ context.Context, address string) (net.Conn, error) {
				dialed = address
				return brokerUpstream, nil
			})
			req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Host: "api.example.com:443"}, Host: "api.example.com:443", Header: make(http.Header)}
			done := make(chan struct{})
			go func() { handler.ServeHTTP(writer, req); close(done) }()
			response, err := http.ReadResponse(bufio.NewReader(clientConn), req)
			if err != nil || response.StatusCode != http.StatusOK {
				t.Fatalf("CONNECT response=(%v,%v)", response, err)
			}
			clientHello := testClientHelloRecord(tc.sni)
			_, writeErr := clientConn.Write(clientHello)
			if tc.wantTunnel {
				if writeErr != nil {
					t.Fatalf("write ClientHello: %v", writeErr)
				}
				got := make([]byte, len(clientHello))
				if _, err = io.ReadFull(upstreamConn, got); err != nil || !bytes.Equal(got, clientHello) {
					t.Fatalf("upstream TLS bytes=(%x,%v)", got, err)
				}
				if dialed != "8.8.8.8:443" {
					t.Fatalf("broker dialed %q", dialed)
				}
			} else if writeErr != nil {
				t.Fatalf("write mismatched ClientHello: %v", writeErr)
			}
			_ = clientConn.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("broker handler did not finish")
			}
			if !tc.wantTunnel {
				_ = upstreamConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				got := make([]byte, len(clientHello))
				n, readErr := upstreamConn.Read(got)
				if n != 0 || readErr == nil {
					t.Fatalf("mismatched TLS SNI reached upstream: n=%d err=%v", n, readErr)
				}
			}
			_ = upstreamConn.Close()
		})
	}
}

type hijackResponseWriter struct {
	conn   net.Conn
	rw     *bufio.ReadWriter
	header http.Header
}

func (w *hijackResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *hijackResponseWriter) Write(data []byte) (int, error) { return w.conn.Write(data) }
func (w *hijackResponseWriter) WriteHeader(int)                {}
func (w *hijackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, w.rw, nil
}

func testClientHelloRecord(host string) []byte {
	body := testClientHello(host)
	record := make([]byte, 5+len(body))
	record[0], record[1], record[2] = 22, 3, 1
	binary.BigEndian.PutUint16(record[3:], uint16(len(body)))
	copy(record[5:], body)
	return record
}
