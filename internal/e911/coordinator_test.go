package e911

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	runtimee911 "github.com/iniwex5/vowifi-go/runtimehost/e911"
	"github.com/yibaiba/hideck/internal/modem"
	"github.com/yibaiba/hideck/internal/websheet"
)

func TestCoordinatorDoesNotRunEntitlementProbes(t *testing.T) {
	body, err := os.ReadFile("coordinator.go")
	if err != nil {
		t.Fatalf("read coordinator.go: %v", err)
	}
	source := string(body)
	for _, forbidden := range []string{
		"runATTBootstrapProbe",
		"runTS43Probe",
		"HIDECK_E911_ATT_CACHED_TOKEN",
		"debugATTCachedToken",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("coordinator must not contain experimental entitlement hook %q", forbidden)
		}
	}
}

func TestHTTPClientAdapterUsesConfiguredEntitlementClient(t *testing.T) {
	client := &fakeEntitlementHTTPClient{
		body:    []byte(`[{"status":6004,"response-id":3}]`),
		headers: []runtimee911.HeaderPair{{Key: "X-Response", Value: "accepted"}},
	}
	adapter := httpClientAdapter{
		deviceID: "dev-1",
		client:   client,
	}

	resp, err := adapter.Do(&runtimee911.HTTPRequest{
		Context: context.Background(),
		Method:  "POST",
		URL:     "https://sentitlement2.mobile.att.net/",
		Headers: []runtimee911.HeaderPair{
			{Key: "x-protocol-version", Value: "2"},
		},
		Body: []byte("payload"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests=%d want 1", len(client.requests))
	}
	gotReq := client.requests[0]
	if gotReq.URL != "https://sentitlement2.mobile.att.net/" {
		t.Fatalf("url=%q", gotReq.URL)
	}
	if len(gotReq.Headers) != 1 || gotReq.Headers[0].Key != "x-protocol-version" || gotReq.Headers[0].Value != "2" {
		t.Fatalf("headers=%+v", gotReq.Headers)
	}
	if string(gotReq.Body) != "payload" {
		t.Fatalf("body=%q", gotReq.Body)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if string(resp.Body) != `[{"status":6004,"response-id":3}]` {
		t.Fatalf("response body=%q", resp.Body)
	}
	if len(resp.Headers) != 1 || resp.Headers[0].Key != "X-Response" {
		t.Fatalf("response headers=%+v", resp.Headers)
	}
}

func TestBuildRuntimeE911IdentityIgnoresDebugCachedTokenEnv(t *testing.T) {
	t.Setenv("HIDECK_E911_ATT_CACHED_TOKEN", " cached-token-value ")

	got := buildRuntimeE911Identity(modem.DeviceStatus{
		IMSI:  "310280233641503",
		ICCID: "89014103212345678901",
		IMEI:  "356306952701762",
		GID1:  "A1B2",
	}, "310", "280", "HiDeck")

	if got.CachedToken != "" {
		t.Fatalf("cached token=%q", got.CachedToken)
	}
	if got.SIPUsername != "310280233641503@private.att.net" {
		t.Fatalf("sip username=%q", got.SIPUsername)
	}
	if got.ICCID != "89014103212345678901" {
		t.Fatalf("ICCID=%q", got.ICCID)
	}
	if got.GID1 != "A1B2" {
		t.Fatalf("GID1=%q", got.GID1)
	}
}

func TestCoordinatorBindsCreatorToWebsheetSession(t *testing.T) {
	broker := websheet.New(websheet.Config{AllowPrivateHosts: true})
	coordinator := &Coordinator{OwnerUserID: "creator-user", Websheets: broker}

	info, err := coordinator.createOwnedWebsheet(t.Context(), websheet.Request{URL: "https://203.0.113.10/e911"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := broker.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AuthorizeOwner("creator-user", false); err != nil {
		t.Fatalf("creator authorization error=%v", err)
	}
	if err := session.AuthorizeOwner("different-user", false); !errors.Is(err, websheet.ErrUnauthorized) {
		t.Fatalf("different user authorization error=%v, want ErrUnauthorized", err)
	}
}

type fakeEntitlementHTTPClient struct {
	body     []byte
	headers  []runtimee911.HeaderPair
	requests []*runtimee911.HTTPRequest
}

func (f *fakeEntitlementHTTPClient) Do(req *runtimee911.HTTPRequest) (*runtimee911.HTTPResponse, error) {
	f.requests = append(f.requests, req)
	return &runtimee911.HTTPResponse{
		StatusCode: 200,
		Headers:    append([]runtimee911.HeaderPair(nil), f.headers...),
		Body:       append([]byte(nil), f.body...),
	}, nil
}
