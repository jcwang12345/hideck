package e911

import (
	"errors"
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

type redactingHTTPClient struct {
	client runtimee911.HTTPClient
	proxy  *runtimehost.ProxyConfig
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
