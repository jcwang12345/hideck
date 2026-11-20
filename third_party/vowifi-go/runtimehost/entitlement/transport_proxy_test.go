package entitlement

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSOCKS5HTTPClientUsesAuthenticatedProxyAndRemoteDNS(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	result := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			result <- acceptErr
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		reader := bufio.NewReader(conn)
		if err := serveEntitlementSOCKS5(reader, conn); err != nil {
			result <- err
			return
		}
		result <- nil
	}()

	client, err := NewSOCKS5HTTPClient(listener.Addr().String(), "alice", "secret")
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(&HTTPRequest{Method: "GET", URL: "http://carrier.invalid/entitlement"})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || string(response.Body) != "proxied" {
		t.Fatalf("response=%+v body=%q", response, response.Body)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestSOCKS5HTTPClientRejectsInvalidAddress(t *testing.T) {
	if _, err := NewSOCKS5HTTPClient("missing-port", "", ""); err == nil {
		t.Fatal("invalid SOCKS5 address was accepted")
	}
}

func serveEntitlementSOCKS5(reader *bufio.Reader, conn net.Conn) error {
	header, err := readExactly(reader, 2)
	if err != nil || header[0] != 5 {
		return fmt.Errorf("read greeting: %w", err)
	}
	methods, err := readExactly(reader, int(header[1]))
	if err != nil || !containsByte(methods, 2) {
		return fmt.Errorf("proxy authentication method missing")
	}
	if _, err = conn.Write([]byte{5, 2}); err != nil {
		return err
	}
	authHeader, err := readExactly(reader, 2)
	if err != nil || authHeader[0] != 1 {
		return fmt.Errorf("read authentication header: %w", err)
	}
	username, err := readExactly(reader, int(authHeader[1]))
	if err != nil {
		return err
	}
	passwordLength, err := reader.ReadByte()
	if err != nil {
		return err
	}
	password, err := readExactly(reader, int(passwordLength))
	if err != nil || string(username) != "alice" || string(password) != "secret" {
		return fmt.Errorf("unexpected proxy credentials")
	}
	if _, err = conn.Write([]byte{1, 0}); err != nil {
		return err
	}
	requestHeader, err := readExactly(reader, 5)
	if err != nil || requestHeader[0] != 5 || requestHeader[1] != 1 || requestHeader[3] != 3 {
		return fmt.Errorf("expected SOCKS5 CONNECT with domain name")
	}
	host, err := readExactly(reader, int(requestHeader[4]))
	if err != nil {
		return err
	}
	portBytes, err := readExactly(reader, 2)
	if err != nil || string(host) != "carrier.invalid" || binary.BigEndian.Uint16(portBytes) != 80 {
		return fmt.Errorf("unexpected SOCKS5 destination %q", host)
	}
	if _, err = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
		return err
	}
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return readErr
		}
		if line == "\r\n" {
			break
		}
	}
	_, err = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 7\r\nConnection: close\r\n\r\nproxied")
	return err
}

func readExactly(reader io.Reader, size int) ([]byte, error) {
	buffer := make([]byte, size)
	_, err := io.ReadFull(reader, buffer)
	return buffer, err
}

func containsByte(values []byte, target byte) bool {
	return strings.IndexByte(string(values), target) >= 0
}
