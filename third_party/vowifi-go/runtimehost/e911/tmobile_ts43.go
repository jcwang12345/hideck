package e911

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	tmobileEntitlementProvider       = "t-mobile_entitlement"
	tmobileEAPRelayContentType       = "application/vnd.gsma.eap-relay.v1.0+json"
	tmobileEntitlementResponseTypes  = tmobileEAPRelayContentType + ", application/json, text/vnd.wap.connectivity-xml"
	tmobileWebsheetContentType       = "application/x-www-form-urlencoded"
	tmobileEntitlementVersion        = "2.0"
	tmobileEntitlementApplicationID  = "ap2004"
	tmobileEntitlementSoftware       = "1.0"
	tmobileEntitlementTerminalVendor = "HiDe"
	tmobileEntitlementTerminalModel  = "HiDeck"
	tmobileTS43Endpoint              = "https://ts43.eas3.msg.t-mobile.com/ts43"
	tmobileAppleEntitlementEndpoint  = "https://eas3.msg.t-mobile.com"
)

func isTMobileEntitlementProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case tmobileEntitlementProvider, "tmobile_entitlement", "t-mobile-entitlement", "tmobile-entitlement":
		return true
	default:
		return false
	}
}

func startTMobileEntitlementFlow(ctx context.Context, req Request, endpoint, fallbackURL string) (Response, error) {
	endpoint = normalizeTMobileEntitlementEndpoint(endpoint)
	if endpoint == "" {
		if fallbackURL == "" {
			return Response{}, ErrWebsheetUnavailable
		}
		return finalizeTMobileWebsheetResponse(Response{
			URL: fallbackURL, ContentType: tmobileWebsheetContentType, Title: "E911 address",
		}, nil), nil
	}

	initialURL, identity, err := tmobileEntitlementURL(endpoint, req.Identity)
	if err != nil {
		return Response{}, err
	}
	state := newEntitlementChallengeState(req)
	response, err := doTMobileEntitlementRequest(
		ctx, req, http.MethodGet, initialURL,
		tmobileEntitlementHeaders(tmobileEntitlementResponseTypes, ""), nil,
	)
	if err != nil {
		return Response{}, err
	}
	cookieSeeds := collectEntitlementCookieSeeds(initialURL, response.Headers)
	cookies := mergeEntitlementCookies("", response.Headers)

	for attempts := 0; ; attempts++ {
		result, final, err := parseTMobileEntitlementResponse(response.Body)
		if err != nil {
			return Response{}, err
		}
		if final.URL != "" {
			return state.applyToResponse(finalizeTMobileWebsheetResponse(final, cookieSeeds)), nil
		}
		if !result.hasChallenge() {
			if fallbackURL != "" {
				return state.applyToResponse(finalizeTMobileWebsheetResponse(Response{
					URL: fallbackURL, ContentType: tmobileWebsheetContentType, Title: "E911 address",
				}, cookieSeeds)), nil
			}
			return Response{}, fmt.Errorf("%w: T-Mobile entitlement response did not include ServiceFlow_URL", ErrWebsheetUnavailable)
		}
		if attempts >= maxEntitlementChallengeResponses {
			return Response{}, fmt.Errorf("%w: too many T-Mobile entitlement challenges", ErrChallengeNotImplemented)
		}

		relay, err := tmobileChallengeAnswer(req, result, &state, identity)
		if err != nil {
			return Response{}, err
		}
		body, err := json.Marshal(map[string]string{"eap-relay-packet": relay})
		if err != nil {
			return Response{}, err
		}
		response, err = doTMobileEntitlementRequest(
			ctx, req, http.MethodPost, endpoint,
			tmobileEntitlementHeaders(tmobileEntitlementResponseTypes, cookies), body,
		)
		if err != nil {
			return Response{}, err
		}
		cookieSeeds = append(cookieSeeds, collectEntitlementCookieSeeds(endpoint, response.Headers)...)
		cookies = mergeEntitlementCookies(cookies, response.Headers)
	}
}

func finalizeTMobileWebsheetResponse(response Response, cookieSeeds []CookieSeed) Response {
	response.WebsheetProfile = WebsheetProfileIOSSafari
	response.CookieSeeds = append([]CookieSeed(nil), cookieSeeds...)
	return response
}

func collectEntitlementCookieSeeds(sourceURL string, headers []HeaderPair) []CookieSeed {
	seeds := make([]CookieSeed, 0, 2)
	for _, header := range headers {
		if !strings.EqualFold(strings.TrimSpace(header.Key), "Set-Cookie") {
			continue
		}
		seeds = append(seeds, CookieSeed{SourceURL: sourceURL, SetCookie: header.Value})
	}
	return seeds
}

func normalizeTMobileEntitlementEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if strings.EqualFold(strings.TrimRight(endpoint, "/"), tmobileAppleEntitlementEndpoint) {
		return tmobileTS43Endpoint
	}
	return endpoint
}

func tmobileEntitlementURL(endpoint string, identity Identity) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" {
		return "", "", fmt.Errorf("e911: invalid T-Mobile entitlement URL %q", endpoint)
	}
	permanentNAI := permanentNAIIdentity(identity.IMSI, identity.MCC, identity.MNC)
	if permanentNAI == "" {
		return "", "", errors.New("e911: T-Mobile entitlement identity is incomplete")
	}
	if strings.TrimSpace(identity.IMEI) == "" {
		return "", "", errors.New("e911: T-Mobile entitlement terminal identity is incomplete")
	}

	query := parsed.Query()
	query.Set("EAP_ID", permanentNAI)
	query.Set("terminal_id", strings.TrimSpace(identity.IMEI))
	query.Set("terminal_vendor", tmobileEntitlementTerminalVendor)
	query.Set("terminal_model", tmobileEntitlementTerminalModel)
	query.Set("terminal_sw_version", tmobileEntitlementSoftware)
	if gid1 := strings.TrimSpace(identity.GID1); gid1 != "" {
		query.Set("GID1", gid1)
	}
	query.Set("app", tmobileEntitlementApplicationID)
	query.Set("vers", "0")
	query.Set("entitlement_version", tmobileEntitlementVersion)
	parsed.RawQuery = query.Encode()
	return parsed.String(), permanentNAI, nil
}

func permanentNAIIdentity(imsi, mcc, mnc string) string {
	imsi = strings.TrimSpace(imsi)
	mcc = strings.TrimSpace(mcc)
	mnc = strings.TrimSpace(mnc)
	if imsi == "" || len(mcc) != 3 || mnc == "" || len(mnc) > 3 {
		return ""
	}
	for len(mnc) < 3 {
		mnc = "0" + mnc
	}
	return fmt.Sprintf("0%s@nai.epc.mnc%s.mcc%s.3gppnetwork.org", imsi, mnc, mcc)
}

func tmobileEntitlementHeaders(accept, cookies string) []HeaderPair {
	headers := []HeaderPair{
		{Key: "Accept", Value: accept},
		{Key: "Accept-Language", Value: "en-US,en;q=0.5"},
		{Key: "User-Agent", Value: "PRD-TS43 term-HiDe/HiDeck client-IMS-Entitlement/2.0 OS-Linux/1.0"},
	}
	if strings.TrimSpace(cookies) != "" {
		headers = append(headers, HeaderPair{Key: "Cookie", Value: cookies})
	}
	return headers
}

func doTMobileEntitlementRequest(ctx context.Context, req Request, method, endpoint string, headers []HeaderPair, body []byte) (*HTTPResponse, error) {
	ctx = contextOrBackground(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if method == http.MethodPost {
		headers = append(headers, HeaderPair{Key: "Content-Type", Value: tmobileEAPRelayContentType})
	}
	httpRequest := &HTTPRequest{
		Context: ctx, Method: method, URL: endpoint,
		Headers: append([]HeaderPair(nil), headers...), Body: append([]byte(nil), body...),
	}
	trace, _ := req.Trace.(TraceSink)
	if trace != nil {
		trace.Request(httpRequest)
	}
	client := req.Client
	if client == nil {
		client = NewDefaultHTTPClient()
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		if trace != nil {
			trace.Error(httpRequest, err)
		}
		return nil, err
	}
	if response == nil {
		return nil, errors.New("e911: T-Mobile entitlement HTTP client returned nil response")
	}
	if trace != nil {
		trace.Response(httpRequest, response)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("e911 T-Mobile entitlement HTTP status %d", response.StatusCode)
	}
	return response, nil
}

func tmobileChallengeAnswer(req Request, result entitlementResult, state *entitlementChallengeState, identity string) (string, error) {
	answerReq := req
	answerReq.Identity.SIPUsername = identity
	body := entitlementChallengeAnswerBody(answerReq, result)
	handled, err := answerEAPControl(answerReq, result, state, body)
	if err != nil {
		return "", err
	}
	if !handled {
		if err := answerAKAChallenge(answerReq, result, state, body); err != nil {
			return "", err
		}
	}
	relay, _ := body["eap-relay-packet"].(string)
	if strings.TrimSpace(relay) == "" {
		return "", ErrChallengeNotImplemented
	}
	return relay, nil
}

func parseTMobileEntitlementResponse(body []byte) (entitlementResult, Response, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return entitlementResult{}, Response{}, errors.New("e911: empty T-Mobile entitlement response")
	}
	if strings.HasPrefix(trimmed, "<") {
		response, err := parseTMobileEntitlementXML([]byte(trimmed))
		return entitlementResult{}, response, err
	}
	var document any
	if err := json.Unmarshal([]byte(trimmed), &document); err != nil {
		return entitlementResult{}, Response{}, fmt.Errorf("e911: decode T-Mobile entitlement JSON: %w", err)
	}
	result, err := parseEntitlementResponse([]byte(trimmed))
	if err != nil {
		return entitlementResult{}, Response{}, err
	}
	response := tmobileWebsheetFromJSON(document)
	if response.URL != "" {
		response.ContentType = tmobileWebsheetContentType
		response.Title = "E911 address"
	}
	return result, response, nil
}

func tmobileWebsheetFromJSON(document any) Response {
	application := findTMobileApplication(document)
	if application == nil {
		return Response{}
	}
	serviceURL := strings.TrimSpace(stringValue(findTMobileApplicationField(application, "serviceflowurl")))
	if !looksHTTPURL(serviceURL) {
		return Response{}
	}
	return Response{
		URL:      serviceURL,
		UserData: strings.TrimSpace(stringValue(findTMobileApplicationField(application, "serviceflowuserdata"))),
	}
}

func findTMobileApplication(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if normalizeTMobileFieldName(key) != tmobileEntitlementApplicationID {
				continue
			}
			if application, ok := child.(map[string]any); ok {
				return application
			}
			if application := findTMobileApplication(child); application != nil {
				return application
			}
		}
		if strings.EqualFold(strings.TrimSpace(stringValue(findTMobileApplicationFieldDirect(typed, "appid"))), tmobileEntitlementApplicationID) {
			return typed
		}
		for _, child := range typed {
			if application := findTMobileApplication(child); application != nil {
				return application
			}
		}
	case []any:
		for _, child := range typed {
			if application := findTMobileApplication(child); application != nil {
				return application
			}
		}
	}
	return nil
}

func findTMobileApplicationField(value any, want string) any {
	switch typed := value.(type) {
	case map[string]any:
		if found := findTMobileApplicationFieldDirect(typed, want); found != nil {
			return found
		}
		for _, child := range typed {
			if found := findTMobileApplicationField(child, want); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range typed {
			if found := findTMobileApplicationField(child, want); found != nil {
				return found
			}
		}
	}
	return nil
}

func findTMobileApplicationFieldDirect(application map[string]any, want string) any {
	for key, value := range application {
		if normalizeTMobileFieldName(key) == want {
			return value
		}
	}
	return nil
}

func normalizeTMobileFieldName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, "-", "")
	return value
}

func parseTMobileEntitlementXML(body []byte) (Response, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(body)))
	applicationDepth := 0
	var parameters map[string]string
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Response{}, fmt.Errorf("e911: decode T-Mobile entitlement XML: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			if strings.EqualFold(element.Name.Local, "characteristic") {
				if applicationDepth > 0 {
					applicationDepth++
				} else if strings.EqualFold(xmlAttribute(element.Attr, "type"), "APPLICATION") {
					applicationDepth = 1
					parameters = make(map[string]string)
				}
				continue
			}
			if applicationDepth > 0 && strings.EqualFold(element.Name.Local, "parm") {
				name, value := xmlParameter(element.Attr)
				parameters[normalizeTMobileFieldName(name)] = value
			}
		case xml.EndElement:
			if applicationDepth == 0 || !strings.EqualFold(element.Name.Local, "characteristic") {
				continue
			}
			applicationDepth--
			if applicationDepth != 0 {
				continue
			}
			if strings.EqualFold(parameters["appid"], tmobileEntitlementApplicationID) {
				serviceURL := strings.TrimSpace(parameters["serviceflowurl"])
				if looksHTTPURL(serviceURL) {
					return Response{
						URL: serviceURL, UserData: strings.TrimSpace(parameters["serviceflowuserdata"]),
						ContentType: tmobileWebsheetContentType, Title: "E911 address",
					}, nil
				}
			}
			parameters = nil
		}
	}
	return Response{}, fmt.Errorf("%w: T-Mobile entitlement XML did not include ap2004 ServiceFlow_URL", ErrWebsheetUnavailable)
}

func xmlAttribute(attributes []xml.Attr, want string) string {
	for _, attribute := range attributes {
		if strings.EqualFold(attribute.Name.Local, want) {
			return strings.TrimSpace(attribute.Value)
		}
	}
	return ""
}

func xmlParameter(attributes []xml.Attr) (name, value string) {
	for _, attribute := range attributes {
		switch strings.ToLower(attribute.Name.Local) {
		case "name":
			name = attribute.Value
		case "value":
			value = attribute.Value
		}
	}
	return strings.TrimSpace(name), strings.TrimSpace(value)
}

func mergeEntitlementCookies(current string, headers []HeaderPair) string {
	names := make([]string, 0, 4)
	values := make(map[string]string)
	add := func(cookie string) {
		cookie = strings.TrimSpace(cookie)
		separator := strings.IndexByte(cookie, '=')
		if separator <= 0 {
			return
		}
		name := strings.TrimSpace(cookie[:separator])
		if _, exists := values[name]; !exists {
			names = append(names, name)
		}
		values[name] = cookie
	}
	for _, cookie := range strings.Split(current, ";") {
		add(cookie)
	}
	for _, header := range headers {
		if !strings.EqualFold(strings.TrimSpace(header.Key), "Set-Cookie") {
			continue
		}
		add(strings.SplitN(header.Value, ";", 2)[0])
	}
	cookies := make([]string, 0, len(names))
	for _, name := range names {
		cookies = append(cookies, values[name])
	}
	return strings.Join(cookies, "; ")
}
