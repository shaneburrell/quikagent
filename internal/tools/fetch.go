package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"quikagent/internal/llm"
)

const (
	fetchDefaultTimeout = 30 * time.Second
	fetchMaxBytes       = 512 * 1024
	fetchMaxRedirects   = 5
)

type fetchTool struct {
	http *http.Client
}

func newFetch() *fetchTool {
	f := &fetchTool{}
	f.http = &http.Client{
		Timeout:   fetchDefaultTimeout,
		Transport: pinnedFetchTransport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= fetchMaxRedirects {
				return fmt.Errorf("too many redirects")
			}
			return validateFetchURL(req.Context(), req.URL.String())
		},
	}
	return f
}

// pinnedFetchTransport resolves the host once, rejects blocked IPs, and
// dials the chosen address so a later DNS rebinding cannot change the peer.
func pinnedFetchTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return &http.Transport{
		ForceAttemptHTTP2: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("fetch dns: %w", err)
			}
			var last error
			for _, ipa := range ips {
				if !allowPrivateFetchForTest && isBlockedIP(ipa.IP) {
					last = errInvalidArg("fetch blocked: resolves to private or loopback address")
					continue
				}
				c, err := dialer.DialContext(ctx, network, net.JoinHostPort(ipa.IP.String(), port))
				if err == nil {
					return c, nil
				}
				last = err
			}
			if last == nil {
				return nil, errInvalidArg("fetch blocked: no usable address")
			}
			return nil, last
		},
	}
}

func (f *fetchTool) ReadOnly() bool { return true }

func (f *fetchTool) Spec() llm.Tool {
	return llm.Tool{
		Name:        "fetch",
		Description: "Fetch a URL over HTTP GET and return text content. HTML is stripped to readable text. Size and timeout are capped. Private/loopback hosts are blocked.",
		Parameters:  []byte(`{"type":"object","properties":{"url":{"type":"string","description":"HTTP or HTTPS URL to fetch"}},"required":["url"]}`),
	}
}

func (f *fetchTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", errInvalidArg(err.Error())
	}
	if a.URL == "" {
		return "", errInvalidArg("url is required")
	}
	if err := validateFetchURL(ctx, a.URL); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "quikagent/0.1")
	req.Header.Set("Accept", "text/*, application/json, application/xml")

	res, err := f.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("fetch: HTTP %d", res.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, fetchMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("fetch read: %w", err)
	}
	truncated := false
	if len(body) > fetchMaxBytes {
		body = body[:fetchMaxBytes]
		truncated = true
	}

	ct := res.Header.Get("Content-Type")
	text := string(body)
	if strings.Contains(ct, "html") || looksHTML(text) {
		text = htmlToText(text)
	} else if !utf8.Valid(body) {
		return fmt.Sprintf("HTTP %d\n(content-type: %s; binary response omitted)", res.StatusCode, ct), nil
	}

	out := fmt.Sprintf("HTTP %d\nURL: %s\nContent-Type: %s\n\n%s", res.StatusCode, a.URL, ct, strings.TrimSpace(text))
	if truncated {
		out += "\n\n… [truncated at " + fmt.Sprintf("%d", fetchMaxBytes) + " bytes]"
	}
	return truncate(out), nil
}

func validateFetchURL(ctx context.Context, raw string) error {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return errInvalidArg("url must start with http:// or https://")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errInvalidArg("bad url: " + err.Error())
	}
	host := u.Hostname()
	if host == "" {
		return errInvalidArg("url host is required")
	}
	if allowPrivateFetchForTest {
		return nil
	}
	if isBlockedFetchHost(host) {
		return errInvalidArg("fetch blocked: private or loopback host")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("fetch dns: %w", err)
	}
	for _, ipa := range ips {
		if isBlockedIP(ipa.IP) {
			return errInvalidArg("fetch blocked: resolves to private or loopback address")
		}
	}
	return nil
}

// allowPrivateFetchForTest lets unit tests hit httptest servers on loopback.
var allowPrivateFetchForTest bool

func isBlockedFetchHost(host string) bool {
	h := strings.ToLower(host)
	switch h {
	case "localhost", "metadata.google.internal":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return isBlockedIP(ip)
	}
	return false
}

func isBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	// Cloud metadata link-local.
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
	}
	return false
}

func looksHTML(s string) bool {
	lower := strings.ToLower(s[:min(len(s), 512)])
	return strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype html")
}

var (
	reScript = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reTag    = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpace  = regexp.MustCompile(`[ \t]+\n`)
	reBlank  = regexp.MustCompile(`\n{3,}`)
)

func htmlToText(s string) string {
	s = reScript.ReplaceAllString(s, "")
	s = reStyle.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br />", "\n")
	s = strings.ReplaceAll(s, "</p>", "\n\n")
	s = strings.ReplaceAll(s, "</div>", "\n")
	s = strings.ReplaceAll(s, "</h1>", "\n\n")
	s = strings.ReplaceAll(s, "</h2>", "\n\n")
	s = strings.ReplaceAll(s, "</h3>", "\n\n")
	s = reTag.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = reSpace.ReplaceAllString(s, "\n")
	s = reBlank.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
