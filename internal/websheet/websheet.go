package websheet

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultTTL             = 10 * time.Minute
	defaultClientTimeout   = 45 * time.Second
	defaultBasePath        = "/api/websheets"
	browserCookiePrefix    = "hideck_websheet_"
	callbackURLToken       = "{{CALLBACK_URL}}"
	targetQueryParam       = "target_query"
	bootstrapBodyParam     = "bootstrap_body"
	HeaderProfileIOSSafari = "ios-safari"
	iosSafariUserAgent     = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1"
)

var (
	ErrNotFound     = errors.New("websheet session not found")
	ErrExpired      = errors.New("websheet session expired")
	ErrUnsafeURL    = errors.New("websheet URL is not allowed")
	ErrUnauthorized = errors.New("websheet session unauthorized")
)

//go:embed bridge.js
var websheetBridgeJS string

type Config struct {
	TTL               time.Duration
	BasePath          string
	AllowPrivateHosts bool
	Now               func() time.Time
}

type Broker struct {
	mu                sync.Mutex
	sessions          map[string]*Session
	ttl               time.Duration
	basePath          string
	allowPrivateHosts bool
	now               func() time.Time
}

type Request struct {
	URL           string
	UserData      string
	ContentType   string
	Title         string
	Transport     http.RoundTripper
	CookieSeeds   []CookieSeed
	HeaderProfile string
}

type CookieSeed struct {
	SourceURL string
	SetCookie string
}

type Info struct {
	ID       string `json:"id"`
	EmbedURL string `json:"embedUrl"`
	Title    string `json:"title,omitempty"`
	URL      string `json:"url"`
	Method   string `json:"method"`
}

type Callback struct {
	Source             string `json:"source,omitempty"`
	Controller         string `json:"controller,omitempty"`
	Method             string `json:"method,omitempty"`
	Event              string `json:"event"`
	ResultCode         string `json:"resultCode,omitempty"`
	Href               string `json:"href,omitempty"`
	ActivationCode     string `json:"activationCode,omitempty"`
	DefaultSMDPAddress string `json:"defaultSmdpAddress,omitempty"`
	SMDPFQDN           string `json:"smdpFqdn,omitempty"`
	ICCID              string `json:"iccid,omitempty"`
	IMEI               string `json:"imei,omitempty"`
	NextAction         string `json:"nextAction,omitempty"`
}

type Session struct {
	id                string
	accessToken       string
	target            *url.URL
	userData          string
	contentType       string
	title             string
	headerProfile     string
	expiresAt         time.Time
	basePath          string
	client            *http.Client
	now               func() time.Time
	allowPrivateHosts bool

	callbackCh chan Callback
	doneCh     chan struct{}
	doneOnce   sync.Once
}

func New(cfg Config) *Broker {
	ttl := cfg.TTL
	if ttl == 0 {
		ttl = defaultTTL
	}
	basePath := strings.TrimRight(cfg.BasePath, "/")
	if basePath == "" {
		basePath = defaultBasePath
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Broker{
		sessions:          make(map[string]*Session),
		ttl:               ttl,
		basePath:          basePath,
		allowPrivateHosts: cfg.AllowPrivateHosts,
		now:               now,
	}
}

func (b *Broker) Create(ctx context.Context, req Request) (*Session, error) {
	if b == nil {
		return nil, errors.New("websheet broker is nil")
	}
	target, err := parseAllowedURL(ctx, req.URL, b.allowPrivateHosts)
	if err != nil {
		return nil, err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create websheet cookie jar: %w", err)
	}
	if err := seedCookieJar(jar, req.CookieSeeds); err != nil {
		return nil, err
	}
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	accessToken, err := randomToken()
	if err != nil {
		return nil, err
	}
	session := &Session{
		id:                id,
		accessToken:       accessToken,
		target:            target,
		userData:          strings.TrimSpace(req.UserData),
		contentType:       strings.TrimSpace(req.ContentType),
		title:             strings.TrimSpace(req.Title),
		headerProfile:     strings.TrimSpace(req.HeaderProfile),
		expiresAt:         b.now().Add(b.ttl),
		basePath:          b.basePath,
		now:               b.now,
		allowPrivateHosts: b.allowPrivateHosts,
		callbackCh:        make(chan Callback, 1),
		doneCh:            make(chan struct{}),
	}
	transport := req.Transport
	if transport == nil {
		transport = &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: 10 * time.Second,
				Control: safeDialControl(b.allowPrivateHosts),
			}).DialContext,
		}
	}
	session.client = &http.Client{
		Jar:       jar,
		Timeout:   defaultClientTimeout,
		Transport: transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			_, err := parseAllowedURL(r.Context(), r.URL.String(), b.allowPrivateHosts)
			return err
		},
	}

	b.mu.Lock()
	b.sessions[id] = session
	b.mu.Unlock()
	return session, nil
}

func (b *Broker) Get(id string) (*Session, error) {
	if b == nil {
		return nil, ErrNotFound
	}
	b.mu.Lock()
	session := b.sessions[id]
	if session != nil && session.expired() {
		delete(b.sessions, id)
		session = nil
	}
	b.mu.Unlock()
	if session == nil {
		return nil, ErrNotFound
	}
	return session, nil
}

func (b *Broker) Delete(id string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	delete(b.sessions, id)
	b.mu.Unlock()
}

func (s *Session) Info() Info {
	values := url.Values{}
	values.Set("token", s.accessToken)
	return Info{
		ID:       s.id,
		EmbedURL: s.basePath + "/" + url.PathEscape(s.id) + "?" + values.Encode(),
		Title:    s.title,
		URL:      s.target.String(),
		Method:   s.method(),
	}
}

func (s *Session) Authorize(r *http.Request) error {
	if s == nil {
		return ErrNotFound
	}
	if s.expired() {
		return ErrExpired
	}
	token := ""
	if r != nil {
		if r.URL != nil {
			token = strings.TrimSpace(r.URL.Query().Get("token"))
		}
		if token == "" {
			token = strings.TrimSpace(r.Header.Get("X-Websheet-Token"))
		}
		if token == "" {
			if cookie, err := r.Cookie(s.browserCookieName()); err == nil {
				token = strings.TrimSpace(cookie.Value)
			}
		}
	}
	if token == "" || s.accessToken == "" {
		return ErrUnauthorized
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.accessToken)) != 1 {
		return ErrUnauthorized
	}
	return nil
}

func (s *Session) WaitCallback(ctx context.Context) (Callback, error) {
	select {
	case callback := <-s.callbackCh:
		return callback, nil
	case <-s.doneCh:
		return Callback{Event: "finishFlow"}, nil
	case <-ctx.Done():
		return Callback{}, ctx.Err()
	}
}

func (s *Session) WaitDone(ctx context.Context) error {
	select {
	case <-s.doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) Done() {
	s.doneOnce.Do(func() {
		close(s.doneCh)
	})
}

func (s *Session) Callback(callback Callback) {
	sendLatest(s.callbackCh, callback)
}

func (s *Session) ServeBootstrap(w http.ResponseWriter, r *http.Request) error {
	if s.expired() {
		return ErrExpired
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	s.setBrowserAuthCookie(w)
	target := *s.target
	if s.method() == http.MethodGet && s.userData != "" {
		appendRawQuery(&target, s.userData)
	}
	proxyURL := s.proxyURL(target.String(), token)
	if s.method() == http.MethodGet {
		http.Redirect(w, r, proxyURL, http.StatusFound)
		return nil
	}
	if s.userData != "" {
		proxyURL = appendLocalQuery(proxyURL, bootstrapBodyParam, "1")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := io.WriteString(w, s.postBootstrapHTML(proxyURL))
	return err
}

func (s *Session) browserCookieName() string {
	return browserCookiePrefix + s.id
}

func (s *Session) setBrowserAuthCookie(w http.ResponseWriter) {
	maxAge := int(s.expiresAt.Sub(s.now()).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.browserCookieName(),
		Value:    s.accessToken,
		Path:     s.basePath + "/" + url.PathEscape(s.id) + "/",
		Expires:  s.expiresAt,
		MaxAge:   maxAge,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Session) Proxy(w http.ResponseWriter, r *http.Request) error {
	if s.expired() {
		return ErrExpired
	}
	rawTarget := strings.TrimSpace(r.URL.Query().Get("target"))
	if rawTarget == "" {
		rawTarget = s.proxyPathTarget(r)
	}
	if rawTarget == "" {
		rawTarget = s.target.String()
	}
	target, err := parseAllowedURL(r.Context(), rawTarget, s.allowPrivateHosts)
	if err != nil {
		return err
	}
	if callback, ok := callbackFromURL(target); ok {
		s.Callback(callback)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, err := io.WriteString(w, callbackHTML())
		return err
	}

	var body io.Reader
	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
		if r.URL.Query().Get(bootstrapBodyParam) == "1" && s.userData != "" {
			body = strings.NewReader(s.userData)
		} else {
			body = r.Body
		}
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), body)
	if err != nil {
		return fmt.Errorf("create websheet request: %w", err)
	}
	bootstrap := r.URL.Query().Get(bootstrapBodyParam) == "1"
	if body != nil && !bootstrap && r.ContentLength >= 0 {
		req.ContentLength = r.ContentLength
	}
	copyProxyHeaders(req.Header, r.Header)
	applyHeaderProfile(req.Header, s.headerProfile, bootstrap)
	if !bootstrap {
		referer := s.upstreamReferer(r)
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		origin := targetOrigin(target)
		if refererURL, parseErr := url.Parse(referer); parseErr == nil && targetOrigin(refererURL) != "" {
			origin = targetOrigin(refererURL)
		}
		if body != nil && origin != "" {
			req.Header.Set("Origin", origin)
		}
	}
	if bootstrap && s.contentType != "" {
		req.Header.Set("Content-Type", s.contentType)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("proxy websheet request: %w", err)
	}
	defer resp.Body.Close()

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	contentType := resp.Header.Get("Content-Type")
	if isHTML(contentType) {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("read websheet response: %w", err)
		}
		token := strings.TrimSpace(r.URL.Query().Get("token"))
		base := target
		if resp.Request != nil && resp.Request.URL != nil {
			base = resp.Request.URL
		}
		html := s.rewriteHTML(string(data), base, token, shouldInjectBridge(r))
		_, err = io.WriteString(w, html)
		return err
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

func (s *Session) method() string {
	if s.contentType != "" {
		return http.MethodPost
	}
	return http.MethodGet
}

func (s *Session) expired() bool {
	return !s.now().Before(s.expiresAt)
}

func (s *Session) proxyURL(rawTarget string, token string) string {
	if target, err := url.Parse(rawTarget); err == nil && target.Scheme != "" && target.Host != "" {
		values := url.Values{}
		if target.RawQuery != "" {
			values.Set(targetQueryParam, target.RawQuery)
		}
		if token != "" {
			values.Set("token", token)
		}
		proxyPath := s.basePath + "/" + url.PathEscape(s.id) + "/proxy/" + target.Scheme + "/" + target.Host + target.EscapedPath()
		if target.EscapedPath() == "" {
			proxyPath += "/"
		}
		if encoded := values.Encode(); encoded != "" {
			proxyPath += "?" + encoded
		}
		return proxyPath
	}
	values := url.Values{}
	values.Set("target", rawTarget)
	if token != "" {
		values.Set("token", token)
	}
	return s.basePath + "/" + url.PathEscape(s.id) + "/proxy?" + values.Encode()
}

func (s *Session) proxyPathTarget(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	prefix := s.basePath + "/" + url.PathEscape(s.id) + "/proxy/"
	escapedPath := r.URL.EscapedPath()
	if !strings.HasPrefix(escapedPath, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(escapedPath, prefix)
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 {
		return ""
	}
	target := url.URL{
		Scheme: parts[0],
		Host:   parts[1],
	}
	if len(parts) == 3 {
		pathValue, err := url.PathUnescape("/" + parts[2])
		if err != nil {
			return ""
		}
		target.Path = pathValue
	} else {
		target.Path = "/"
	}
	target.RawQuery = r.URL.Query().Get(targetQueryParam)
	return target.String()
}

func (s *Session) upstreamReferer(r *http.Request) string {
	if r == nil {
		return ""
	}
	raw := strings.TrimSpace(r.Referer())
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	upstream := s.proxyPathTarget(&http.Request{URL: parsed})
	if upstream == "" {
		return ""
	}
	target, err := parseAllowedURL(r.Context(), upstream, s.allowPrivateHosts)
	if err != nil {
		return ""
	}
	return target.String()
}

func (s *Session) callbackURL(token string) string {
	values := url.Values{}
	if token != "" {
		values.Set("token", token)
	}
	path := s.basePath + "/" + url.PathEscape(s.id) + "/callback"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return path
}

func (s *Session) postBootstrapHTML(action string) string {
	values, err := url.ParseQuery(strings.TrimLeft(s.userData, "?&"))
	var body bytes.Buffer
	body.WriteString("<!doctype html><html><body>")
	body.WriteString(`<form id="websheet" method="post" action="`)
	body.WriteString(html.EscapeString(action))
	body.WriteString(`">`)
	if err == nil {
		for key, items := range values {
			for _, item := range items {
				body.WriteString(`<input type="hidden" name="`)
				body.WriteString(html.EscapeString(key))
				body.WriteString(`" value="`)
				body.WriteString(html.EscapeString(item))
				body.WriteString(`">`)
			}
		}
	} else if s.userData != "" {
		body.WriteString(`<input type="hidden" name="payload" value="`)
		body.WriteString(html.EscapeString(s.userData))
		body.WriteString(`">`)
	}
	body.WriteString(`</form><script>document.getElementById("websheet").submit();</script></body></html>`)
	return body.String()
}

func (s *Session) rewriteHTML(doc string, base *url.URL, token string, injectBridge bool) string {
	docBase := s.documentBaseURL(doc, base)
	rewritten := attrURLPattern.ReplaceAllStringFunc(doc, func(match string) string {
		parts := attrURLPattern.FindStringSubmatch(match)
		if len(parts) < 6 {
			return match
		}
		attr := parts[1]
		raw := parts[3]
		if raw == "" {
			raw = parts[4]
		}
		if raw == "" {
			raw = parts[5]
		}
		next, ok := s.rewriteURL(raw, docBase, token)
		if !ok {
			return match
		}
		return attr + `="` + html.EscapeString(next) + `"`
	})
	if !injectBridge {
		return rewritten
	}
	script := s.bridgeScript(token, docBase)
	lower := strings.ToLower(rewritten)
	if idx := strings.Index(lower, "<head"); idx >= 0 {
		if end := strings.Index(rewritten[idx:], ">"); end >= 0 {
			insertAt := idx + end + 1
			return rewritten[:insertAt] + script + rewritten[insertAt:]
		}
	}
	if idx := strings.LastIndex(lower, "</head>"); idx >= 0 {
		return rewritten[:idx] + script + rewritten[idx:]
	}
	if idx := strings.LastIndex(lower, "</body>"); idx >= 0 {
		return rewritten[:idx] + script + rewritten[idx:]
	}
	return script + rewritten
}

func shouldInjectBridge(r *http.Request) bool {
	if r == nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Dest"))) {
	case "", "document", "iframe", "frame":
		return true
	default:
		return false
	}
}

func (s *Session) documentBaseURL(doc string, fallback *url.URL) *url.URL {
	match := baseHrefPattern.FindStringSubmatch(doc)
	if len(match) < 5 {
		return fallback
	}
	raw := match[2]
	if raw == "" {
		raw = match[3]
	}
	if raw == "" {
		raw = match[4]
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return fallback
	}
	return fallback.ResolveReference(ref)
}

func (s *Session) rewriteURL(raw string, base *url.URL, token string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return raw, false
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "javascript:") || strings.HasPrefix(lower, "mailto:") || strings.HasPrefix(lower, "tel:") || strings.HasPrefix(lower, "data:") {
		return raw, false
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return raw, false
	}
	target := base.ResolveReference(ref)
	return s.proxyURL(target.String(), token), true
}

func (s *Session) bridgeScript(token string, carrierBase *url.URL) string {
	callbackURL := s.callbackURL(token)
	script := strings.ReplaceAll(websheetBridgeJS, callbackURLToken, jsString(callbackURL))
	script = strings.ReplaceAll(script, "{{ABSOLUTE_PATH_PROXY_PREFIX}}", jsString(s.absolutePathProxyPrefix(carrierBase, token)))
	script = strings.ReplaceAll(script, "{{WEBSHEET_TOKEN}}", jsString(token))
	return "<script>\n" + script + "\n</script>"
}

func (s *Session) absolutePathProxyPrefix(carrierBase *url.URL, token string) string {
	origin := targetOrigin(carrierBase)
	if origin == "" {
		return ""
	}
	return strings.TrimRight(s.proxyURL(origin+"/", ""), "/")
}

var (
	attrURLPattern  = regexp.MustCompile("(?i)\\b(href|src|action)=(\"([^\"]*)\"|'([^']*)'|([^\\s\"'=<>`]+))")
	baseHrefPattern = regexp.MustCompile("(?is)<base\\b[^>]*\\bhref=(\"([^\"]*)\"|'([^']*)'|([^\\s\"'=<>`]+))[^>]*>")
)

func appendLocalQuery(raw string, key string, value string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	values := parsed.Query()
	values.Set(key, value)
	parsed.RawQuery = values.Encode()
	return parsed.String()
}

func copyProxyHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		lowerKey := strings.ToLower(key)
		switch lowerKey {
		case "authorization", "cookie", "host", "referer", "origin", "content-length", "accept-encoding",
			"connection", "sec-fetch-dest", "sec-fetch-mode", "sec-fetch-site", "sec-fetch-user":
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func applyHeaderProfile(header http.Header, profile string, bootstrap bool) {
	if !strings.EqualFold(strings.TrimSpace(profile), HeaderProfileIOSSafari) {
		return
	}
	preserved := make(http.Header)
	if !bootstrap {
		for _, key := range []string{
			"Accept", "Cache-Control", "Content-Type", "If-Modified-Since", "If-None-Match",
			"Pragma", "Range", "X-CSRF-Token", "X-Requested-With", "X-XSRF-Token",
		} {
			for _, value := range header.Values(key) {
				preserved.Add(key, value)
			}
		}
	}
	// The browser is only rendering a same-origin relay. Its native Chromium
	// headers do not describe the upstream request. Forwarding them beside an
	// iOS Safari user agent produces an internally inconsistent fingerprint that
	// carrier WAFs reject, including for scripts and styles after the bootstrap.
	for key := range header {
		header.Del(key)
	}
	for key, values := range preserved {
		for _, value := range values {
			header.Add(key, value)
		}
	}
	header.Set("User-Agent", iosSafariUserAgent)
	header.Set("Accept-Language", "en-US,en;q=0.9")
	if bootstrap {
		header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	}
}

func seedCookieJar(jar http.CookieJar, seeds []CookieSeed) error {
	for _, seed := range seeds {
		sourceURL, err := url.Parse(strings.TrimSpace(seed.SourceURL))
		if err != nil || !strings.EqualFold(sourceURL.Scheme, "https") || sourceURL.Host == "" {
			return fmt.Errorf("invalid websheet cookie source URL")
		}
		setCookie := strings.TrimSpace(seed.SetCookie)
		if setCookie == "" {
			continue
		}
		header := make(http.Header)
		header.Add("Set-Cookie", setCookie)
		response := http.Response{Header: header}
		cookies := response.Cookies()
		if len(cookies) == 0 {
			continue
		}
		jar.SetCookies(sourceURL, cookies)
	}
	return nil
}

func targetOrigin(target *url.URL) string {
	if target == nil || target.Scheme == "" || target.Host == "" {
		return ""
	}
	return target.Scheme + "://" + target.Host
}

func copyResponseHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		switch strings.ToLower(key) {
		case "content-security-policy", "content-security-policy-report-only", "x-frame-options", "content-length", "set-cookie":
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func isHTML(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.Contains(strings.ToLower(contentType), "html")
	}
	return mediaType == "text/html" || mediaType == "application/xhtml+xml"
}

func parseAllowedURL(ctx context.Context, raw string, allowPrivate bool) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%w: URL is required", ErrUnsafeURL)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse websheet URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: scheme %q", ErrUnsafeURL, parsed.Scheme)
	}
	if !allowPrivate && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: scheme %q", ErrUnsafeURL, parsed.Scheme)
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, fmt.Errorf("%w: host is required", ErrUnsafeURL)
	}
	if allowPrivate {
		return parsed, nil
	}
	if isLocalHostname(host) {
		return nil, fmt.Errorf("%w: local host %q", ErrUnsafeURL, host)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if unsafeIP(ip) {
			return nil, fmt.Errorf("%w: private address %q", ErrUnsafeURL, host)
		}
		return parsed, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve websheet host: %w", err)
	}
	for _, addr := range addrs {
		ip, ok := netip.AddrFromSlice(addr.IP)
		if ok && unsafeIP(ip.Unmap()) {
			return nil, fmt.Errorf("%w: private address %q", ErrUnsafeURL, host)
		}
	}
	return parsed, nil
}

func isLocalHostname(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

func unsafeIP(ip netip.Addr) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// safeDialControl 返回一个 net.Dialer.Control 回调，在 TCP 连接真正建立
// 之前（即 DNS 解析之后、connect(2) 之前）对目标 IP 再次校验。
// parseAllowedURL 只在请求发起前校验一次解析结果，如果目标域名在那之后、
// 真正拨号之前被 DNS rebinding 成内网地址，标准库 Transport 会在连接阶段
// 重新解析并直接绕过之前的校验；Control 回调和实际拨号之间没有时间窗口，
// 能彻底堵住这个 TOCTOU 漏洞。
func safeDialControl(allowPrivate bool) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		if allowPrivate {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("%w: invalid dial address %q", ErrUnsafeURL, address)
		}
		if isLocalHostname(host) {
			return fmt.Errorf("%w: local host %q", ErrUnsafeURL, host)
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return fmt.Errorf("%w: dial address is not an IP %q", ErrUnsafeURL, host)
		}
		if unsafeIP(ip.Unmap()) {
			return fmt.Errorf("%w: private address %q", ErrUnsafeURL, host)
		}
		return nil
	}
}

func appendRawQuery(target *url.URL, raw string) {
	raw = strings.TrimLeft(raw, "?&")
	if raw == "" {
		return
	}
	if target.RawQuery == "" {
		target.RawQuery = raw
		return
	}
	target.RawQuery += "&" + raw
}

func callbackFromURL(target *url.URL) (Callback, bool) {
	if !strings.Contains(target.Path, "/_callback") {
		return Callback{}, false
	}
	query := target.Query()
	event := firstValue(query, "event", "callback", "action", "method")
	if event == "" {
		parts := strings.Split(strings.Trim(target.Path, "/"), "/")
		if len(parts) > 0 {
			event = parts[len(parts)-1]
		}
	}
	if normalize(event) == "callback" || normalize(event) == "esim" || event == "" {
		event = callbackEventFromQuery(query)
	}
	return Callback{
		Event:              event,
		ActivationCode:     firstValue(query, "activationCode", "activation_code"),
		DefaultSMDPAddress: firstValue(query, "defaultSmdpAddress", "default_smdp_address"),
		SMDPFQDN:           firstValue(query, "smdpFqdn", "smdp", "defaultSmdpAddress"),
		ICCID:              firstValue(query, "iccid", "ICCID"),
		IMEI:               firstValue(query, "imei", "IMEI"),
		NextAction:         firstValue(query, "nextAction", "next_action"),
	}, true
}

func callbackEventFromQuery(values url.Values) string {
	switch {
	case firstValue(values, "activationCode", "activation_code") != "":
		return "profileReadyWithActivationCode"
	case firstValue(values, "defaultSmdpAddress", "smdp", "smdpFqdn") != "":
		return "profileReadyWithDefaultSmdp"
	case firstValue(values, "nextAction", "next_action") != "":
		return "finishFlow"
	default:
		return "finishFlow"
	}
}

func normalize(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func firstValue(values url.Values, keys ...string) string {
	for _, key := range keys {
		if value := values.Get(key); value != "" {
			return value
		}
	}
	return ""
}

func callbackHTML() string {
	return `<!doctype html><html><body><script>try{window.parent.postMessage({type:"hideck-websheet-callback"},"*")}catch(_){}</script>Carrier flow returned to HiDeck.</body></html>`
}

func jsString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, "\r", `\r`)
	return `"` + value + `"`
}

func randomID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate websheet id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func randomToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate websheet token: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func sendLatest[T any](ch chan T, value T) {
	select {
	case ch <- value:
	default:
		select {
		case <-ch:
		default:
		}
		ch <- value
	}
}
