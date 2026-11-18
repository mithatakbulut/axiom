package verification

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const containerProxyAddress = "127.0.0.1:18080"
const containerRelayBinary = "/verity-agent"
const containerBrokerSocket = "/verity-egress/broker.sock"

// RunEgressRelay exposes only a loopback HTTP proxy in a network-none
// container. Its sole upstream is the host-side Unix socket broker.
func RunEgressRelay(ctx context.Context, socketPath, listenAddress, readyFile string) error {
	if socketPath == "" || listenAddress != containerProxyAddress {
		return errors.New("invalid egress relay configuration")
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return err
	}
	defer listener.Close()
	if readyFile != "" {
		if err := os.WriteFile(readyFile, []byte("ready\n"), 0600); err != nil {
			return err
		}
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		client, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer client.Close()
			broker, err := net.Dial("unix", socketPath)
			if err != nil {
				return
			}
			defer broker.Close()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(broker, client); _ = broker.Close(); done <- struct{}{} }()
			go func() { _, _ = io.Copy(client, broker); _ = client.Close(); done <- struct{}{} }()
			<-done
		}()
	}
}

type egressBroker struct {
	server   *http.Server
	listener net.Listener
	dir      string
	done     chan error
}

func startEgressBroker(ctx context.Context, allowlist []string) (*egressBroker, error) {
	if len(allowlist) == 0 {
		return nil, nil
	}
	dir, err := os.MkdirTemp("", "verity-egress-")
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(dir, 0700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	socket := filepath.Join(dir, "broker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err = os.Chmod(socket, 0600); err != nil {
		_ = listener.Close()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	allowed := make(map[string]bool, len(allowlist))
	for _, item := range allowlist {
		if !validNetworkDestination(item) {
			_ = listener.Close()
			_ = os.RemoveAll(dir)
			return nil, ErrInvalidManifest
		}
		allowed[item] = true
	}
	s := &http.Server{Handler: brokerHandler(allowed)}
	b := &egressBroker{server: s, listener: listener, dir: dir, done: make(chan error, 1)}
	go func() { b.done <- s.Serve(listener) }()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Shutdown(shutdown)
	}()
	return b, nil
}

func brokerHandler(allowed map[string]bool) http.Handler {
	return brokerHandlerWith(allowed, func(ctx context.Context, host string) ([]netip.Addr, error) {
		resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		ips := make([]netip.Addr, 0, len(resolved))
		for _, item := range resolved {
			if ip, ok := netip.AddrFromSlice(item.IP); ok {
				ips = append(ips, ip.Unmap())
			}
		}
		return ips, nil
	}, func(ctx context.Context, address string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, "tcp", address)
	})
}

func brokerHandlerWith(allowed map[string]bool, lookup func(context.Context, string) ([]netip.Addr, error), dial func(context.Context, string) (net.Conn, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodConnect || req.URL == nil || req.URL.Host == "" || req.Host != req.URL.Host || !allowed[req.URL.Host] {
			http.Error(w, "destination not allowed", http.StatusForbidden)
			return
		}
		host, port, err := net.SplitHostPort(req.URL.Host)
		if err != nil {
			http.Error(w, "invalid destination", http.StatusForbidden)
			return
		}
		ips, err := lookup(req.Context(), host)
		if err != nil || len(ips) == 0 {
			http.Error(w, "destination resolution failed", http.StatusBadGateway)
			return
		}
		var upstream net.Conn
		for _, ip := range ips {
			if !publicEgressAddress(ip.Unmap()) {
				continue
			}
			upstream, err = dial(req.Context(), net.JoinHostPort(ip.String(), port))
			if err == nil {
				break
			}
		}
		if upstream == nil {
			http.Error(w, "destination has no permitted public address", http.StatusForbidden)
			return
		}
		defer upstream.Close()
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "CONNECT unsupported", http.StatusInternalServerError)
			return
		}
		client, rw, err := hijacker.Hijack()
		if err != nil {
			return
		}
		_ = client.SetDeadline(time.Now().Add(10 * time.Second))
		_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		if err = rw.Flush(); err != nil {
			_ = client.Close()
			return
		}
		var recordHeader [5]byte
		if _, err = io.ReadFull(rw, recordHeader[:]); err != nil || recordHeader[0] != 22 {
			_ = client.Close()
			return
		}
		recordLength := int(binary.BigEndian.Uint16(recordHeader[3:]))
		if recordLength < 4 || recordLength > 16384 {
			_ = client.Close()
			return
		}
		recordBody := make([]byte, recordLength)
		if _, err = io.ReadFull(rw, recordBody); err != nil {
			_ = client.Close()
			return
		}
		sni, parseErr := clientHelloSNI(recordBody)
		if parseErr != nil || !strings.EqualFold(sni, host) {
			_ = client.Close()
			return
		}
		_ = client.SetDeadline(time.Time{})
		if _, err = upstream.Write(recordHeader[:]); err == nil {
			_, err = upstream.Write(recordBody)
		}
		if err != nil {
			_ = client.Close()
			return
		}
		finished := make(chan struct{}, 2)
		go func() { _, _ = io.Copy(upstream, rw); finished <- struct{}{} }()
		go func() { _, _ = io.Copy(client, upstream); finished <- struct{}{} }()
		<-finished
		_ = client.Close()
		_ = upstream.Close()
		<-finished
	})
}

func clientHelloSNI(record []byte) (string, error) {
	if len(record) < 4 || record[0] != 1 {
		return "", errors.New("not a TLS ClientHello")
	}
	handshakeLength := int(record[1])<<16 | int(record[2])<<8 | int(record[3])
	if handshakeLength > len(record)-4 {
		return "", errors.New("truncated TLS ClientHello")
	}
	b := record[4 : 4+handshakeLength]
	if len(b) < 34 {
		return "", errors.New("truncated TLS ClientHello fields")
	}
	offset := 34
	read8 := func() (int, bool) {
		if offset >= len(b) {
			return 0, false
		}
		v := int(b[offset])
		offset++
		return v, true
	}
	read16 := func() (int, bool) {
		if offset+2 > len(b) {
			return 0, false
		}
		v := int(binary.BigEndian.Uint16(b[offset:]))
		offset += 2
		return v, true
	}
	sessionLength, ok := read8()
	if !ok || offset+sessionLength > len(b) {
		return "", errors.New("invalid TLS session id")
	}
	offset += sessionLength
	cipherLength, ok := read16()
	if !ok || cipherLength == 0 || cipherLength%2 != 0 || offset+cipherLength > len(b) {
		return "", errors.New("invalid TLS cipher list")
	}
	offset += cipherLength
	compressionLength, ok := read8()
	if !ok || compressionLength == 0 || offset+compressionLength > len(b) {
		return "", errors.New("invalid TLS compression list")
	}
	offset += compressionLength
	if offset == len(b) {
		return "", errors.New("TLS ClientHello has no SNI")
	}
	extensionsLength, ok := read16()
	if !ok || offset+extensionsLength != len(b) {
		return "", errors.New("invalid TLS extension list")
	}
	extensionsEnd := offset + extensionsLength
	var serverName string
	for offset < extensionsEnd {
		typ, ok := read16()
		if !ok {
			return "", errors.New("truncated TLS extension")
		}
		length, ok := read16()
		if !ok || offset+length > extensionsEnd {
			return "", errors.New("invalid TLS extension length")
		}
		data := b[offset : offset+length]
		offset += length
		if typ != 0 {
			continue
		}
		if serverName != "" || len(data) < 5 {
			return "", errors.New("invalid or duplicate TLS SNI")
		}
		listLength := int(binary.BigEndian.Uint16(data))
		if listLength != len(data)-2 || data[2] != 0 {
			return "", errors.New("invalid TLS server name list")
		}
		nameLength := int(binary.BigEndian.Uint16(data[3:]))
		if nameLength == 0 || nameLength != len(data)-5 {
			return "", errors.New("invalid TLS host name")
		}
		name := data[5:]
		serverName = string(name)
	}
	if serverName == "" || !validNetworkDestination(serverName+":443") {
		return "", errors.New("TLS ClientHello has no valid host SNI")
	}
	return serverName, nil
}

func (b *egressBroker) close() {
	if b == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = b.server.Shutdown(ctx)
	_ = b.listener.Close()
	<-b.done
	_ = os.RemoveAll(b.dir)
}

func publicEgressAddress(ip netip.Addr) bool {
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	blocked := []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "64:ff9b::/96", "64:ff9b:1::/48", "2001:db8::/32", "2001::/23", "2002::/16", "3fff::/20"}
	for _, prefix := range blocked {
		p := netip.MustParsePrefix(prefix)
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
