package websheet

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestCreateRejectsPrivateAndNonHTTPSURLs(t *testing.T) {
	b := New(Config{})
	for _, raw := range []string{
		"http://example.com/",
		"https://127.0.0.1/",
		"https://10.0.0.1/",
		"https://192.168.1.1/",
		"file:///etc/passwd",
	} {
		if _, err := b.Create(context.Background(), Request{URL: raw}); !errors.Is(err, ErrUnsafeURL) {
			t.Fatalf("Create(%q) err=%v, want ErrUnsafeURL", raw, err)
		}
	}
}

func TestCreateAllowsPublicHTTPS(t *testing.T) {
	b := New(Config{})
	s, err := b.Create(context.Background(), Request{URL: "https://attdashboard.wireless.att.com/softphone/primary/reseller/r017"})
	if err != nil {
		t.Fatal(err)
	}
	info := s.Info()
	if info.EmbedURL == "" || info.Method != "GET" {
		t.Fatalf("info=%+v", info)
	}
}

func TestInfoEmbedURLCarriesSessionAccessToken(t *testing.T) {
	b := New(Config{})
	s, err := b.Create(context.Background(), Request{URL: "https://203.0.113.10/"})
	if err != nil {
		t.Fatal(err)
	}
	info := s.Info()
	if !strings.Contains(info.EmbedURL, "token=") {
		t.Fatalf("EmbedURL=%q, want session token query", info.EmbedURL)
	}

	validReq := httptest.NewRequest(http.MethodGet, info.EmbedURL, nil)
	if err := s.Authorize(validReq); err != nil {
		t.Fatalf("Authorize(valid token) error=%v", err)
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/api/websheets/"+info.ID, nil)
	if err := s.Authorize(missingReq); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Authorize(missing token) error=%v, want ErrUnauthorized", err)
	}

	bootstrapRec := httptest.NewRecorder()
	if err := s.ServeBootstrap(bootstrapRec, validReq); err != nil {
		t.Fatal(err)
	}
	cookies := bootstrapRec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("bootstrap cookies=%v, want one path-scoped authorization cookie", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != s.browserCookieName() || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("bootstrap cookie flags=%+v", cookie)
	}
	wantPath := s.basePath + "/" + url.PathEscape(s.id) + "/"
	if cookie.Path != wantPath {
		t.Fatalf("bootstrap cookie path=%q want %q", cookie.Path, wantPath)
	}
	cookieReq := httptest.NewRequest(http.MethodGet, wantPath+"proxy/https/example.com/logo.svg", nil)
	cookieReq.AddCookie(cookie)
	if err := s.Authorize(cookieReq); err != nil {
		t.Fatalf("Authorize(path-scoped cookie) error=%v", err)
	}
}

func TestPostBootstrapProxiesRawUserDataBody(t *testing.T) {
	const rawPostData = "method%3Dupdate-tc-loc%26devicetype%3Dphone%26authtoken%3DAXBCRgUM"
	var gotBody string
	var gotContentType string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><html><body>ok</body></html>")
	}))
	defer upstream.Close()

	b := New(Config{AllowPrivateHosts: true})
	s, err := b.Create(context.Background(), Request{
		URL:         upstream.URL + "/softphone/primary/reseller/r017",
		UserData:    rawPostData,
		ContentType: "application/x-www-form-urlencoded",
	})
	if err != nil {
		t.Fatal(err)
	}

	bootstrapReq := httptest.NewRequest(http.MethodGet, s.Info().EmbedURL, nil)
	bootstrapRec := httptest.NewRecorder()
	if err := s.ServeBootstrap(bootstrapRec, bootstrapReq); err != nil {
		t.Fatal(err)
	}
	action := extractFormAction(t, bootstrapRec.Body.String())

	proxyReq := httptest.NewRequest(http.MethodPost, action, nil)
	proxyReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	proxyRec := httptest.NewRecorder()
	if err := s.Proxy(proxyRec, proxyReq); err != nil {
		t.Fatal(err)
	}
	if gotBody != rawPostData {
		t.Fatalf("proxied body=%q want raw %q", gotBody, rawPostData)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("Content-Type=%q want application/x-www-form-urlencoded", gotContentType)
	}
}

func TestIOSSafariProfileNormalizesBootstrapRequest(t *testing.T) {
	var gotHeader http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><html><body>address form</body></html>")
	}))
	defer upstream.Close()

	b := New(Config{AllowPrivateHosts: true})
	s, err := b.Create(context.Background(), Request{
		URL:           upstream.URL + "/e911",
		UserData:      "signed=payload",
		ContentType:   "application/x-www-form-urlencoded",
		HeaderProfile: HeaderProfileIOSSafari,
	})
	if err != nil {
		t.Fatal(err)
	}

	bootstrapReq := httptest.NewRequest(http.MethodGet, s.Info().EmbedURL, nil)
	bootstrapRec := httptest.NewRecorder()
	if err := s.ServeBootstrap(bootstrapRec, bootstrapReq); err != nil {
		t.Fatal(err)
	}
	action := extractFormAction(t, bootstrapRec.Body.String())

	proxyReq := httptest.NewRequest(http.MethodPost, action, nil)
	proxyReq.Header.Set("User-Agent", "Mozilla/5.0 Chrome/131.0")
	proxyReq.Header.Set("Sec-CH-UA", `"Chromium";v="131"`)
	proxyReq.Header.Set("Sec-CH-UA-Platform", `"macOS"`)
	proxyReq.Header.Set("Priority", "u=0, i")
	proxyReq.Header.Set("Upgrade-Insecure-Requests", "1")
	proxyReq.Header.Set("DNT", "1")
	proxyReq.Header.Set("Sec-GPC", "1")
	proxyReq.Header.Set("X-Browser-Only", "chrome")
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.Header.Set("Origin", "https://hideck.example")
	proxyRec := httptest.NewRecorder()
	if err := s.Proxy(proxyRec, proxyReq); err != nil {
		t.Fatal(err)
	}

	if gotHeader.Get("User-Agent") != iosSafariUserAgent {
		t.Fatalf("User-Agent=%q", gotHeader.Get("User-Agent"))
	}
	if gotHeader.Get("Sec-CH-UA") != "" || gotHeader.Get("Sec-CH-UA-Platform") != "" {
		t.Fatalf("client hints leaked upstream: %v", gotHeader)
	}
	for _, key := range []string{"Priority", "Upgrade-Insecure-Requests", "DNT", "Sec-GPC", "X-Browser-Only"} {
		if value := gotHeader.Get(key); value != "" {
			t.Fatalf("bootstrap browser header %s leaked upstream: %q", key, value)
		}
	}
	if gotHeader.Get("Origin") != "" || gotHeader.Get("Referer") != "" {
		t.Fatalf("bootstrap forged origin headers: origin=%q referer=%q", gotHeader.Get("Origin"), gotHeader.Get("Referer"))
	}
	if gotHeader.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Fatalf("Content-Type=%q", gotHeader.Get("Content-Type"))
	}
}

func TestSecondHopPreservesFormLengthAndDoesNotInventContentType(t *testing.T) {
	type observedRequest struct {
		method        string
		body          string
		contentType   string
		contentLength int64
		transfer      []string
		referer       string
		origin        string
		priority      string
	}
	requests := make(chan observedRequest, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- observedRequest{
			method:        r.Method,
			body:          string(body),
			contentType:   r.Header.Get("Content-Type"),
			contentLength: r.ContentLength,
			transfer:      append([]string(nil), r.TransferEncoding...),
			referer:       r.Referer(),
			origin:        r.Header.Get("Origin"),
			priority:      r.Header.Get("Priority"),
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	b := New(Config{AllowPrivateHosts: true})
	s, err := b.Create(context.Background(), Request{
		URL:           upstream.URL + "/bootstrap",
		UserData:      "signed=payload",
		ContentType:   "application/x-www-form-urlencoded",
		HeaderProfile: HeaderProfileIOSSafari,
	})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.SplitN(s.Info().EmbedURL, "token=", 2)[1]

	getReq := httptest.NewRequest(http.MethodGet, s.proxyURL(upstream.URL+"/asset.js", token), nil)
	getReq.Header.Set("Referer", "https://hideck.example"+s.proxyURL(upstream.URL+"/address", token))
	getReq.Header.Set("Priority", "u=1")
	getRec := httptest.NewRecorder()
	if err := s.Proxy(getRec, getReq); err != nil {
		t.Fatal(err)
	}
	gotGET := <-requests
	if gotGET.contentType != "" {
		t.Fatalf("second-hop GET Content-Type=%q, want empty", gotGET.contentType)
	}
	if gotGET.referer != upstream.URL+"/address" {
		t.Fatalf("second-hop GET Referer=%q want %q", gotGET.referer, upstream.URL+"/address")
	}
	if gotGET.priority != "" {
		t.Fatalf("second-hop GET leaked browser Priority=%q", gotGET.priority)
	}

	body := "street=1+Main+St&city=Seattle"
	postReq := httptest.NewRequest(http.MethodPost, s.proxyURL(upstream.URL+"/submit", token), strings.NewReader(body))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("Referer", "https://hideck.example"+s.proxyURL(upstream.URL+"/address", token))
	postRec := httptest.NewRecorder()
	if err := s.Proxy(postRec, postReq); err != nil {
		t.Fatal(err)
	}
	gotPOST := <-requests
	if gotPOST.body != body {
		t.Fatalf("second-hop POST body=%q want %q", gotPOST.body, body)
	}
	if gotPOST.contentLength != int64(len(body)) {
		t.Fatalf("second-hop POST ContentLength=%d want %d", gotPOST.contentLength, len(body))
	}
	if len(gotPOST.transfer) != 0 {
		t.Fatalf("second-hop POST Transfer-Encoding=%v, want none", gotPOST.transfer)
	}
	if gotPOST.contentType != "application/x-www-form-urlencoded" {
		t.Fatalf("second-hop POST Content-Type=%q", gotPOST.contentType)
	}
	if gotPOST.referer != upstream.URL+"/address" {
		t.Fatalf("second-hop POST Referer=%q want %q", gotPOST.referer, upstream.URL+"/address")
	}
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	if gotPOST.origin != targetOrigin(upstreamURL) {
		t.Fatalf("second-hop POST Origin=%q want %q", gotPOST.origin, targetOrigin(upstreamURL))
	}
}

func TestSeedCookieJarPreservesDomainScope(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	err = seedCookieJar(jar, []CookieSeed{
		{SourceURL: "https://ts43.eas3.msg.t-mobile.com/ts43", SetCookie: "shared=ready; Domain=.t-mobile.com; Path=/; Secure; HttpOnly"},
		{SourceURL: "https://ts43.eas3.msg.t-mobile.com/ts43", SetCookie: "host_only=private; Path=/; Secure; HttpOnly"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cookies := jar.Cookies(mustURL(t, "https://ke911.t-mobile.com/e911-ui-entrypoint/e911address.do"))
	values := make(map[string]string, len(cookies))
	for _, cookie := range cookies {
		values[cookie.Name] = cookie.Value
	}
	if values["shared"] != "ready" {
		t.Fatalf("domain cookie missing: %v", values)
	}
	if _, ok := values["host_only"]; ok {
		t.Fatalf("host-only cookie crossed subdomains: %v", values)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestRequestTransportPersistsAcrossRedirectsAndCookies(t *testing.T) {
	var cookieSeen bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.SetCookie(w, &http.Cookie{Name: "carrier_session", Value: "ready", Path: "/"})
			http.Redirect(w, r, "/address", http.StatusFound)
		case "/address":
			cookie, err := r.Cookie("carrier_session")
			cookieSeen = err == nil && cookie.Value == "ready"
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, "<!doctype html><html><body>address form</body></html>")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	transport := &countingRoundTripper{base: http.DefaultTransport}
	b := New(Config{AllowPrivateHosts: true})
	s, err := b.Create(context.Background(), Request{
		URL:       upstream.URL + "/start",
		Transport: transport,
	})
	if err != nil {
		t.Fatal(err)
	}

	bootstrapReq := httptest.NewRequest(http.MethodGet, s.Info().EmbedURL, nil)
	bootstrapRec := httptest.NewRecorder()
	if err := s.ServeBootstrap(bootstrapRec, bootstrapReq); err != nil {
		t.Fatal(err)
	}
	proxyReq := httptest.NewRequest(http.MethodGet, bootstrapRec.Header().Get("Location"), nil)
	proxyRec := httptest.NewRecorder()
	if err := s.Proxy(proxyRec, proxyReq); err != nil {
		t.Fatal(err)
	}
	if proxyRec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", proxyRec.Code, proxyRec.Body.String())
	}
	if transport.calls != 2 {
		t.Fatalf("transport calls=%d, want initial request plus redirect", transport.calls)
	}
	if !cookieSeen {
		t.Fatal("carrier redirect did not retain the websheet cookie")
	}
}

type countingRoundTripper struct {
	base  http.RoundTripper
	calls int
}

func (t *countingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	return t.base.RoundTrip(req)
}

func TestRewriteHTMLKeepsProxyURLsRelativeToBrowserOrigin(t *testing.T) {
	b := New(Config{AllowPrivateHosts: true})
	s, err := b.Create(context.Background(), Request{URL: "https://attdashboard.wireless.att.com/softphone/primary/reseller/r017"})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(strings.SplitN(s.Info().EmbedURL, "token=", 2)[1], "")
	base, err := url.Parse("https://attdashboard.wireless.att.com/softphone/primary/reseller/r017")
	if err != nil {
		t.Fatal(err)
	}

	rewritten := s.rewriteHTML(
		`<html><head><base href="/softphone/"><script src="main-es2015.js"></script></head></html>`,
		base,
		token,
		true,
	)
	if strings.Contains(rewritten, "http://127.0.0.1:7575") {
		t.Fatalf("rewritten html leaked backend origin: %s", rewritten)
	}
	if !strings.Contains(rewritten, `/api/websheets/`) || !strings.Contains(rewritten, `/proxy/https/attdashboard.wireless.att.com/softphone/main-es2015.js`) {
		t.Fatalf("rewritten html missing relative proxy URL: %s", rewritten)
	}
}

func TestBridgePathPrefixKeepsTokenOutOfAppendablePath(t *testing.T) {
	b := New(Config{AllowPrivateHosts: true})
	s, err := b.Create(context.Background(), Request{URL: "https://attdashboard.wireless.att.com/softphone/primary/reseller/r017"})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.SplitN(s.Info().EmbedURL, "token=", 2)[1]
	base, err := url.Parse("https://attdashboard.wireless.att.com/softphone/primary/reseller/r017")
	if err != nil {
		t.Fatal(err)
	}

	script := s.bridgeScript(token, base)
	prefix := extractJSStringConst(t, script, "absolutePathProxyPrefix")
	if strings.Contains(prefix, "?token=") {
		t.Fatalf("appendable path prefix includes token query: %s", prefix)
	}
	if !strings.Contains(script, `const websheetToken = "`) {
		t.Fatalf("bridge script missing separate websheet token: %s", script)
	}
}

func TestBridgeDetectsATTAddressValidationOnlyForMutationResponses(t *testing.T) {
	b := New(Config{AllowPrivateHosts: true})
	s, err := b.Create(context.Background(), Request{URL: "https://attdashboard.wireless.att.com/softphone/primary/reseller/r017"})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.SplitN(s.Info().EmbedURL, "token=", 2)[1]
	base, err := url.Parse("https://attdashboard.wireless.att.com/softphone/primary/reseller/r017")
	if err != nil {
		t.Fatal(err)
	}

	script := s.bridgeScript(token, base)
	for _, marker := range []string{
		"inspectATTAddressResponse",
		"e911AddressValidated",
		`status === "validated"`,
		`method === "GET"`,
		"window.top.postMessage",
		"BroadcastChannel",
		"localStorage.setItem",
		"hideck-websheet-complete",
	} {
		if !strings.Contains(script, marker) {
			t.Fatalf("bridge script missing %q: %s", marker, script)
		}
	}
}

func extractJSStringConst(t *testing.T, script string, name string) string {
	t.Helper()
	pattern := regexp.MustCompile(`const\s+` + regexp.QuoteMeta(name) + `\s*=\s*"([^"]*)"`)
	match := pattern.FindStringSubmatch(script)
	if len(match) != 2 {
		t.Fatalf("script missing const %s: %s", name, script)
	}
	return match[1]
}

func extractFormAction(t *testing.T, html string) string {
	t.Helper()
	match := regexp.MustCompile(`action="([^"]+)"`).FindStringSubmatch(html)
	if len(match) != 2 {
		t.Fatalf("bootstrap html missing form action: %s", html)
	}
	return match[1]
}

func TestSessionExpires(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	b := New(Config{
		TTL: time.Minute,
		Now: func() time.Time { return now },
	})
	s, err := b.Create(context.Background(), Request{URL: "https://example.com/"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := b.Get(s.Info().ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get expired err=%v, want ErrNotFound", err)
	}
}

func TestSafeDialControlRejectsLoopbackAtDialTime(t *testing.T) {
	control := safeDialControl(false)
	if err := control("tcp4", "127.0.0.1:443", nil); err == nil {
		t.Fatal("拨号到回环地址应当被拒绝，即便更早的 URL 阶段校验已经通过")
	}
}

func TestSafeDialControlRejectsPrivateRangeAtDialTime(t *testing.T) {
	control := safeDialControl(false)
	if err := control("tcp4", "10.1.2.3:443", nil); err == nil {
		t.Fatal("拨号到内网地址应当被拒绝")
	}
}

func TestSafeDialControlAllowsPublicAddress(t *testing.T) {
	control := safeDialControl(false)
	if err := control("tcp4", "203.0.113.10:443", nil); err != nil {
		t.Fatalf("拨号到公网地址不应被拒绝: %v", err)
	}
}

func TestSafeDialControlAllowsPrivateWhenConfigured(t *testing.T) {
	control := safeDialControl(true)
	if err := control("tcp4", "127.0.0.1:443", nil); err != nil {
		t.Fatalf("allowPrivateHosts=true 时应放行内网地址: %v", err)
	}
}
