package e911

import (
	"errors"
	"net/http"
	"strings"

	"github.com/iniwex5/vowifi-go/runtimehost"
	runtimee911 "github.com/iniwex5/vowifi-go/runtimehost/e911"
)

func entitlementClientForProxy(proxy *runtimehost.ProxyConfig) (runtimee911.HTTPClient, error) {
	if proxy == nil || !proxy.Enabled {
		return runtimee911.NewDefaultHTTPClient(), nil
	}
	client, err := runtimee911.NewSOCKS5HTTPClient(proxy.Addr, proxy.Username, proxy.Password)
	if err != nil {
		return nil, redactProxyError(err, proxy)
	}
	return redactingHTTPClient{client: client, proxy: proxy}, nil
}

func websheetTransportForProxy(proxy *runtimehost.ProxyConfig) (http.RoundTripper, error) {
	if proxy == nil || !proxy.Enabled {
		return nil, nil
	}
	transport, err := runtimee911.NewSOCKS5HTTPTransport(proxy.Addr, proxy.Username, proxy.Password)
	if err != nil {
		return nil, redactProxyError(err, proxy)
	}
	return redactingRoundTripper{transport: transport, proxy: proxy}, nil
}

type redactingHTTPClient struct {
	client runtimee911.HTTPClient
	proxy  *runtimehost.ProxyConfig
}

type redactingRoundTripper struct {
	transport http.RoundTripper
	proxy     *runtimehost.ProxyConfig
}

func (t redactingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.transport == nil {
		return nil, errors.New("e911 websheet HTTP transport is unavailable")
	}
	response, err := t.transport.RoundTrip(req)
	return response, redactProxyError(err, t.proxy)
}

func (c redactingHTTPClient) Do(req *runtimee911.HTTPRequest) (*runtimee911.HTTPResponse, error) {
	if c.client == nil {
		return nil, errors.New("e911 entitlement HTTP client is unavailable")
	}
	response, err := c.client.Do(req)
	return response, redactProxyError(err, c.proxy)
}

func redactProxyError(err error, proxy *runtimehost.ProxyConfig) error {
	if err == nil || proxy == nil {
		return err
	}
	message := err.Error()
	for _, secret := range []string{proxy.Username, proxy.Password} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	return errors.New(message)
}
