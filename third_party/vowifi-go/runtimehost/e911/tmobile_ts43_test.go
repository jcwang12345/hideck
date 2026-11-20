package e911

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	enginesim "github.com/iniwex5/vowifi-go/engine/sim"
	"github.com/iniwex5/vowifi-go/engine/swu/eapaka"
	"github.com/iniwex5/vowifi-go/runtimehost/carrier"
)

func TestStartEmergencyAddressUpdateRunsTMobileTS43WebsheetFlow(t *testing.T) {
	const (
		imsi             = "310260123456789"
		imei             = "356306952701762"
		permanent        = "0310260123456789@nai.epc.mnc260.mcc310.3gppnetwork.org"
		websheet         = "https://wifi.t-mobile.test/e911"
		webData          = "imsi=masked&addr=0&signature=abc"
		sessionKey       = "entitlement-session=abc123"
		endpoint         = "https://ts43.example.test/ts43"
		initialSetCookie = sessionKey + "; Domain=.msg.t-mobile.com; Path=/; Secure; HttpOnly"
		finalSetCookie   = "portal-route=west; Domain=.t-mobile.com; Path=/; SameSite=None; Secure"
	)
	identityRequest := eapaka.Packet{
		Code: eapaka.CodeRequest, Identifier: 9, Type: eapaka.TypeAKA,
		Subtype: eapaka.SubtypeIdentity, Attributes: []eapaka.Attribute{eapaka.PermanentIDReqAttribute()},
	}
	identityRaw, err := identityRequest.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	identityRelay := base64.StdEncoding.EncodeToString(identityRaw)

	client := &tmobileTestHTTPClient{}
	client.handle = func(r *HTTPRequest) *HTTPResponse {
		switch call := len(client.requests); call {
		case 1:
			assertTMobileInitialRequest(t, r, permanent, imei)
			return &HTTPResponse{StatusCode: http.StatusOK,
				Headers: []HeaderPair{{Key: "Content-Type", Value: tmobileEAPRelayContentType}, {Key: "Set-Cookie", Value: initialSetCookie}},
				Body:    []byte(`{"eap-relay-packet":"` + identityRelay + `"}`)}
		case 2:
			if r.Method != http.MethodPost || testHeader(r.Headers, "Content-Type") != tmobileEAPRelayContentType {
				t.Errorf("follow-up request = %s content-type=%q", r.Method, testHeader(r.Headers, "Content-Type"))
			}
			if testHeader(r.Headers, "Cookie") != sessionKey {
				t.Errorf("follow-up cookie=%q", testHeader(r.Headers, "Cookie"))
			}
			assertTMobileIdentityAnswer(t, bytes.NewReader(r.Body), permanent)
			return &HTTPResponse{StatusCode: http.StatusOK, Headers: []HeaderPair{
				{Key: "Content-Type", Value: "text/vnd.wap.connectivity-xml"},
				{Key: "Set-Cookie", Value: finalSetCookie},
			}, Body: []byte(`<?xml version="1.0"?>
<wap-provisioningdoc version="1.1">
  <characteristic type="APPLICATION">
    <parm name="AppID" value="ap2004"/>
    <parm name="EntitlementStatus" value="0"/>
    <parm name="ServiceFlow_URL" value="` + websheet + `"/>
    <parm name="ServiceFlow_UserData" value="imsi=masked&amp;addr=0&amp;signature=abc"/>
  </characteristic>
</wap-provisioningdoc>`)}
		default:
			t.Errorf("unexpected request %d", call)
			return &HTTPResponse{StatusCode: http.StatusInternalServerError}
		}
	}

	response, err := StartEmergencyAddressUpdateCurrent(context.Background(), Request{
		Carrier: carrier.EffectiveCarrierConfig{E911: carrier.E911Config{
			Provider: "T-Mobile_entitlement", EntitlementURL: endpoint,
		}},
		Identity: Identity{IMSI: imsi, IMEI: imei, MCC: "310", MNC: "260", GID1: "A1"},
		Client:   client,
	})
	if err != nil {
		t.Fatalf("StartEmergencyAddressUpdateCurrent: %v", err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests=%d want 2", len(client.requests))
	}
	if response.URL != websheet || response.UserData != webData {
		t.Fatalf("response=%+v", response)
	}
	if response.ContentType != tmobileWebsheetContentType || response.Title != "E911 address" {
		t.Fatalf("websheet metadata=%+v", response)
	}
	if response.WebsheetProfile != WebsheetProfileIOSSafari {
		t.Fatalf("websheet profile=%q", response.WebsheetProfile)
	}
	wantSeeds := []CookieSeed{
		{SourceURL: client.requests[0].URL, SetCookie: initialSetCookie},
		{SourceURL: endpoint, SetCookie: finalSetCookie},
	}
	if len(response.CookieSeeds) != len(wantSeeds) {
		t.Fatalf("cookie seeds=%+v", response.CookieSeeds)
	}
	for index, want := range wantSeeds {
		if got := response.CookieSeeds[index]; got != want {
			t.Errorf("cookie seed %d=%+v want %+v", index, got, want)
		}
	}
}

func TestTMobileFallbackUsesIOSSafariWebsheetProfile(t *testing.T) {
	response, err := startTMobileEntitlementFlow(context.Background(), Request{}, "", "https://wifi.t-mobile.test/e911")
	if err != nil {
		t.Fatal(err)
	}
	if response.WebsheetProfile != WebsheetProfileIOSSafari {
		t.Fatalf("websheet profile=%q", response.WebsheetProfile)
	}
}

type tmobileTestHTTPClient struct {
	requests []*HTTPRequest
	handle   func(*HTTPRequest) *HTTPResponse
}

func (client *tmobileTestHTTPClient) Do(request *HTTPRequest) (*HTTPResponse, error) {
	client.requests = append(client.requests, request)
	return client.handle(request), nil
}

func testHeader(headers []HeaderPair, name string) string {
	for _, header := range headers {
		if strings.EqualFold(header.Key, name) {
			return header.Value
		}
	}
	return ""
}

func assertTMobileInitialRequest(t *testing.T, request *HTTPRequest, permanent, imei string) {
	t.Helper()
	if request.Method != http.MethodGet {
		t.Errorf("initial method=%s", request.Method)
	}
	parsed, err := url.Parse(request.URL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	for key, want := range map[string]string{
		"EAP_ID": permanent, "terminal_id": imei, "terminal_vendor": "HiDe",
		"terminal_model": "HiDeck", "terminal_sw_version": "1.0", "GID1": "A1",
		"app": "ap2004", "vers": "0", "entitlement_version": "2.0",
	} {
		if got := query.Get(key); got != want {
			t.Errorf("query %s=%q want %q", key, got, want)
		}
	}
	if !strings.Contains(testHeader(request.Headers, "Accept"), tmobileEAPRelayContentType) ||
		!strings.Contains(testHeader(request.Headers, "Accept"), "text/vnd.wap.connectivity-xml") {
		t.Errorf("initial Accept=%q", testHeader(request.Headers, "Accept"))
	}
	if !strings.Contains(testHeader(request.Headers, "User-Agent"), "PRD-TS43") {
		t.Errorf("initial User-Agent=%q", testHeader(request.Headers, "User-Agent"))
	}
}

func assertTMobileIdentityAnswer(t *testing.T, body io.Reader, wantIdentity string) {
	t.Helper()
	var payload map[string]string
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		t.Fatalf("decode challenge answer: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("challenge answer fields=%v", payload)
	}
	raw, err := base64.StdEncoding.DecodeString(payload["eap-relay-packet"])
	if err != nil {
		t.Fatalf("decode relay: %v", err)
	}
	packet, err := eapaka.ParsePacket(raw)
	if err != nil {
		t.Fatalf("parse relay: %v", err)
	}
	attribute, ok := eapaka.FindAttribute(packet.Attributes, eapaka.AttributeIdentity)
	if !ok {
		t.Fatalf("identity response missing AT_IDENTITY: %+v", packet)
	}
	identity, err := attribute.IdentityValue()
	if err != nil || identity != wantIdentity {
		t.Fatalf("identity=%q err=%v", identity, err)
	}
}

func TestTMobileChallengeAnswerUsesSIMAKA(t *testing.T) {
	rand16 := bytes.Repeat([]byte{0x21}, 16)
	autn16 := bytes.Repeat([]byte{0x42}, 16)
	identity := permanentNAIIdentity("310260123456789", "310", "260")
	akaResult := enginesim.AKAResult{RES: []byte{0x11, 0x22, 0x33, 0x44}, CK: make([]byte, 16), IK: make([]byte, 16)}
	keys, err := eapaka.DeriveKeys(identity, akaResult)
	if err != nil {
		t.Fatal(err)
	}
	challenge := eapaka.Packet{
		Code: eapaka.CodeRequest, Identifier: 4, Type: eapaka.TypeAKA, Subtype: eapaka.SubtypeChallenge,
		Attributes: []eapaka.Attribute{eapaka.RANDAttribute(rand16), eapaka.AUTNAttribute(autn16), eapaka.MACAttribute(nil)},
	}
	raw, err := challenge.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	mac, err := eapaka.CalculateMAC(keys.KAut, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	challenge.Attributes[len(challenge.Attributes)-1] = eapaka.MACAttribute(mac)
	raw, err = challenge.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseEntitlementResponse([]byte(`{"eap-relay-packet":"` + base64.StdEncoding.EncodeToString(raw) + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	aka := &testAKAProvider{}
	req := Request{
		Identity:    Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
		AKAProvider: aka,
	}
	state := newEntitlementChallengeState(req)
	relay, err := tmobileChallengeAnswer(req, parsed, &state, identity)
	if err != nil {
		t.Fatalf("tmobileChallengeAnswer: %v", err)
	}
	responseRaw, err := base64.StdEncoding.DecodeString(relay)
	if err != nil {
		t.Fatal(err)
	}
	response, err := eapaka.ParsePacket(responseRaw)
	if err != nil {
		t.Fatal(err)
	}
	if response.Code != eapaka.CodeResponse || response.Identifier != challenge.Identifier || aka.calls.Load() != 1 {
		t.Fatalf("response=%+v AKA calls=%d", response, aka.calls.Load())
	}
	if !bytes.Equal(aka.rand, rand16) || !bytes.Equal(aka.autn, autn16) {
		t.Fatal("SIM AKA provider did not receive the T-Mobile challenge")
	}
	if _, ok := eapaka.FindAttribute(response.Attributes, eapaka.AttributeRES); !ok {
		t.Fatal("AKA response is missing AT_RES")
	}
	if err := eapaka.VerifyMAC(keys.KAut, responseRaw, nil); err != nil {
		t.Fatalf("AKA response AT_MAC: %v", err)
	}
}

func TestTMobileEntitlementURLPadsTwoDigitMNC(t *testing.T) {
	raw, identity, err := tmobileEntitlementURL("https://example.test/entitlement", Identity{
		IMSI: "310261234567890", IMEI: "356306952701762", MCC: "310", MNC: "26",
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if identity != "0310261234567890@nai.epc.mnc026.mcc310.3gppnetwork.org" || parsed.Query().Get("EAP_ID") != identity {
		t.Fatalf("identity=%q url=%q", identity, raw)
	}
}

func TestTMobileJSONUsesOnlyAP2004ServiceFlowData(t *testing.T) {
	body := []byte(`{
		"Token":{"token":"AUTHENTICATION-TOKEN"},
		"ap2004":{"ServiceFlow_URL":"https://address.example/e911","ServiceFlow_UserData":"PORTAL-DATA"},
		"ap2005":{"ServiceFlow_URL":"https://wrong.example/sms","ServiceFlow_UserData":"WRONG-DATA"}
	}`)
	_, response, err := parseTMobileEntitlementResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if response.URL != "https://address.example/e911" || response.UserData != "PORTAL-DATA" {
		t.Fatalf("response=%+v", response)
	}
}

func TestTMobileXMLScopesAndOrdersApplicationFields(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
<wap-provisioningdoc>
  <characteristic type="APPLICATION">
    <parm name="AppID" value="ap2003"/>
    <parm name="ServiceFlow_URL" value="https://wrong.example/volte"/>
  </characteristic>
  <characteristic type="APPLICATION">
    <parm name="ServiceFlow_UserData" value="PORTAL-DATA"/>
    <parm name="ServiceFlow_URL" value="https://address.example/e911"/>
    <parm name="AppID" value="ap2004"/>
  </characteristic>
  <characteristic type="APPLICATION">
    <parm name="AppID" value="ap2005"/>
    <parm name="ServiceFlow_URL" value="https://wrong.example/sms"/>
  </characteristic>
</wap-provisioningdoc>`)
	response, err := parseTMobileEntitlementXML(body)
	if err != nil {
		t.Fatal(err)
	}
	if response.URL != "https://address.example/e911" || response.UserData != "PORTAL-DATA" {
		t.Fatalf("response=%+v", response)
	}
}

func TestTMobileEntitlementURLRequiresHTTPSAndIdentity(t *testing.T) {
	valid := Identity{IMSI: "310260123456789", IMEI: "356306952701762", MCC: "310", MNC: "260"}
	for name, testCase := range map[string]struct {
		endpoint string
		identity Identity
	}{
		"http":         {endpoint: "http://example.test/ts43", identity: valid},
		"missing imsi": {endpoint: "https://example.test/ts43", identity: Identity{IMEI: valid.IMEI, MCC: valid.MCC, MNC: valid.MNC}},
		"missing imei": {endpoint: "https://example.test/ts43", identity: Identity{IMSI: valid.IMSI, MCC: valid.MCC, MNC: valid.MNC}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := tmobileEntitlementURL(testCase.endpoint, testCase.identity); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestMergeEntitlementCookiesPreservesAndUpdatesSessions(t *testing.T) {
	cookies := mergeEntitlementCookies("session=old; route=a", []HeaderPair{
		{Key: "Set-Cookie", Value: "session=new; Path=/; Secure"},
		{Key: "Set-Cookie", Value: "aka=ready; Path=/"},
	})
	if cookies != "session=new; route=a; aka=ready" {
		t.Fatalf("cookies=%q", cookies)
	}
}

func TestTMobilePresetsUseTS43Endpoint(t *testing.T) {
	for _, mnc := range []string{"240", "260"} {
		cfg := carrier.ResolveEffectiveCarrierConfig("310", mnc)
		if cfg.E911.Provider != "T-Mobile_entitlement" || normalizeTMobileEntitlementEndpoint(cfg.E911.EntitlementURL) != tmobileTS43Endpoint {
			t.Fatalf("310/%s e911=%+v", mnc, cfg.E911)
		}
	}
}
