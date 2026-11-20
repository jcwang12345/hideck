package e911

import (
	"errors"
	"strings"
	"testing"

	"github.com/iniwex5/vowifi-go/runtimehost"
	runtimee911 "github.com/iniwex5/vowifi-go/runtimehost/e911"
)

func TestEntitlementClientForProxyBuildsDirectAndSOCKS5Clients(t *testing.T) {
	direct, err := entitlementClientForProxy(nil)
	if err != nil || direct == nil {
		t.Fatalf("direct client=%v err=%v", direct, err)
	}
	proxied, err := entitlementClientForProxy(&runtimehost.ProxyConfig{
		Addr: "127.0.0.1:1080", Username: "alice", Password: "secret", Enabled: true,
	})
	if err != nil || proxied == nil {
		t.Fatalf("proxied client=%v err=%v", proxied, err)
	}
}

func TestEntitlementProxyErrorsRedactCredentials(t *testing.T) {
	proxy := &runtimehost.ProxyConfig{Username: "private-user", Password: "private-pass"}
	client := redactingHTTPClient{
		client: failingEntitlementClient{err: errors.New("private-user:private-pass authentication failed")},
		proxy:  proxy,
	}
	_, err := client.Do(&runtimee911.HTTPRequest{})
	if err == nil {
		t.Fatal("expected proxy error")
	}
	if strings.Contains(err.Error(), proxy.Username) || strings.Contains(err.Error(), proxy.Password) {
		t.Fatalf("credentials leaked in %q", err)
	}
}

type failingEntitlementClient struct{ err error }

func (c failingEntitlementClient) Do(*runtimee911.HTTPRequest) (*runtimee911.HTTPResponse, error) {
	return nil, c.err
}
