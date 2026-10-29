package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// AuthManager 认证管理器
type AuthManager struct {
	config *ProxyConfig
}

// NewAuthManager 创建认证管理器
func NewAuthManager() *AuthManager {
	return &AuthManager{
		config: GetConfig(),
	}
}

// ExtractAPIKey 从请求中提取API Key
// 支持从 query 参数或 path 中提取
func (am *AuthManager) ExtractAPIKey(r *http.Request, pathPrefix string) string {
	if pathPrefix == "" {
		// Admin routes and feed fetches may use an Authorization header. The
		// scoped media routes deliberately do not accept the master key.
		if authorization := r.Header.Get("Authorization"); strings.HasPrefix(authorization, "Bearer ") {
			return strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
		}
		if r.URL.Path == "/feed" && am.VerifyScopedFeedURL(r.URL.Query().Get("url"), r.URL.Query().Get("feed_token")) {
			return am.config.APIKey
		}
		if apikey := r.URL.Query().Get("apikey"); apikey != "" {
			return am.decodeKey(apikey)
		}
	}

	// Verify signed, resource-scoped media tokens.
	if pathPrefix != "" {
		if p := strings.TrimPrefix(r.URL.Path, pathPrefix); p != "" {
			parts := strings.SplitN(p, "/", 2)
			if pathPrefix == "/podcast.xsl/" && len(parts) > 0 && strings.HasPrefix(parts[0], "f_") {
				if am.VerifyFeedURL(r.URL.Query().Get("url"), parts[0]) {
					return am.config.APIKey
				}
				return ""
			}
			if len(parts) > 0 && strings.HasPrefix(parts[0], "s_") {
				resource := ProxyResource(strings.Trim(pathPrefix, "/"))
				if resource == ResourceAudio || resource == ResourceImage || resource == ResourceStyle {
					if am.VerifyResourceToken(resource, r.URL.Query().Get("url"), parts[0]) {
						return am.config.APIKey
					}
				}
				return ""
			}
			if len(parts[0]) == sha256.Size*2 {
				return ""
			}
			if len(parts) > 0 && am.config.AllowLegacyMediaKey {
				return am.decodeKey(parts[0])
			}
		}
	}

	return ""
}

// VerifyAPIKey 验证API Key
func (am *AuthManager) VerifyAPIKey(apikey string) bool {
	return apikey == am.config.APIKey
}

// VerifyRequest 验证请求的API Key
func (am *AuthManager) VerifyRequest(r *http.Request, pathPrefix string) (string, bool) {
	apikey := am.ExtractAPIKey(r, pathPrefix)
	if apikey == "" {
		return "", false
	}
	return apikey, am.VerifyAPIKey(apikey)
}

// EncodeKey Base64编码API Key
func (am *AuthManager) EncodeKey(apikey string) string {
	return base64.StdEncoding.EncodeToString([]byte(apikey))
}

// SignResource produces a deterministic token bound to a resource's stable URL identity.
// Determinism matters because PinePods may keep a feed body cached for a long time.
func (am *AuthManager) SignResource(resource ProxyResource, rawURL string) string {
	payload := resourcePayload(resource, canonicalMediaURL(rawURL))
	mac := hmac.New(sha256.New, []byte(am.config.APIKey))
	_, _ = mac.Write([]byte(payload))
	return "s_" + hex.EncodeToString(mac.Sum(nil))
}

// VerifyResourceToken verifies the resource scope and stable URL identity.
func (am *AuthManager) VerifyResourceToken(resource ProxyResource, rawURL, token string) bool {
	if rawURL == "" {
		return false
	}
	if strings.HasPrefix(token, "s_") {
		if !validResourceURL(rawURL) || hasURLCredentials(rawURL) {
			return false
		}
		if len(token) != sha256.Size*2+2 {
			return false
		}
		got, err := hex.DecodeString(strings.TrimPrefix(token, "s_"))
		if err != nil {
			return false
		}
		mac := hmac.New(sha256.New, []byte(am.config.APIKey))
		_, _ = mac.Write([]byte(resourcePayload(resource, canonicalMediaURL(rawURL))))
		return hmac.Equal(got, mac.Sum(nil))
	}
	legacyExactURLToken := len(token) == sha256.Size*2
	if !legacyExactURLToken {
		return false
	}
	got, err := hex.DecodeString(token)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(am.config.APIKey))
	_, _ = mac.Write([]byte(resourcePayload(resource, canonicalMediaURL(rawURL))))
	if hmac.Equal(got, mac.Sum(nil)) {
		return true
	}
	// Accept tokens produced by the previous exact-URL format during rollout.
	if legacyExactURLToken {
		mac.Reset()
		_, _ = mac.Write([]byte(resourcePayload(resource, rawURL)))
		return hmac.Equal(got, mac.Sum(nil))
	}
	return false
}

// SignFeedURL creates a scoped credential for one upstream feed URL.
func (am *AuthManager) SignFeedURL(rawURL string) string {
	mac := hmac.New(sha256.New, []byte(am.config.APIKey))
	_, _ = mac.Write([]byte("xsl-feed\n" + rawURL))
	return "f_" + hex.EncodeToString(mac.Sum(nil))
}

func (am *AuthManager) VerifyFeedURL(rawURL, token string) bool {
	if rawURL == "" || !strings.HasPrefix(token, "f_") || len(token) != sha256.Size*2+2 {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(token, "f_"))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(am.config.APIKey))
	_, _ = mac.Write([]byte("xsl-feed\n" + rawURL))
	return hmac.Equal(got, mac.Sum(nil))
}

func (am *AuthManager) SignScopedFeedURL(rawURL string) string {
	mac := hmac.New(sha256.New, []byte(am.config.APIKey))
	_, _ = mac.Write([]byte("scoped-feed\n" + rawURL))
	return "s_" + hex.EncodeToString(mac.Sum(nil))
}

func (am *AuthManager) VerifyScopedFeedURL(rawURL, token string) bool {
	if rawURL == "" || !strings.HasPrefix(token, "s_") || len(token) != sha256.Size*2+2 {
		return false
	}
	if !validResourceURL(rawURL) || hasURLCredentials(rawURL) {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(token, "s_"))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(am.config.APIKey))
	_, _ = mac.Write([]byte("scoped-feed\n" + rawURL))
	return hmac.Equal(got, mac.Sum(nil))
}

func LookupFeedCredentials(feedURL string) (string, string) {
	token := NewAuthManager().SignScopedFeedURL(feedURL)
	key := resourceRouteKey(ResourceFeed, token)
	resourceRouteTable.Lock()
	defer resourceRouteTable.Unlock()
	entry, ok := resourceRouteTable.entries[key]
	if !ok || time.Since(entry.LastUsed) > resourceRouteTTL {
		delete(resourceRouteTable.entries, key)
		return "", ""
	}
	entry.LastUsed = time.Now()
	resourceRouteTable.entries[key] = entry
	return entry.Username, entry.Password
}

func resourcePayload(resource ProxyResource, rawURL string) string {
	return string(resource) + "\n" + rawURL
}

// canonicalMediaURL excludes the query values most commonly rotated by podcast
// CDNs. The path token then stays stable, while the proxy's route registry maps
// it to the newest source URL seen in the feed.
func canonicalMediaURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	parsed.User = nil
	parsed.Fragment = ""
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if isRotatingMediaQueryKey(lower) {
			query.Del(key)
		}
	}
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	canonical := url.Values{}
	for _, key := range keys {
		values := append([]string(nil), query[key]...)
		sort.Strings(values)
		for _, value := range values {
			canonical.Add(key, value)
		}
	}
	parsed.RawQuery = canonical.Encode()
	parsed.ForceQuery = false
	return parsed.String()
}

func isRotatingMediaQueryKey(lower string) bool {
	if strings.HasPrefix(lower, "x-amz-") || strings.HasPrefix(lower, "x-goog-") || strings.HasPrefix(lower, "hdnea") || strings.HasPrefix(lower, "hdnts") {
		return true
	}
	switch lower {
	case "token", "sig", "signature", "expires", "expiry", "exp", "auth", "authorization", "apikey", "api_key", "key-pair-id", "policy":
		return true
	default:
		return false
	}
}

// SignRedirectURL creates a same-scope token for an upstream redirect target.
func (am *AuthManager) SignRedirectURL(resource ProxyResource, rawURL string) string {
	return am.SignResource(resource, rawURL)
}

// decodeKey Base64解码API Key（内部方法）
func (am *AuthManager) decodeKey(encoded string) string {
	// 尝试Base64解码
	if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
		return string(decoded)
	}
	// 如果解码失败，直接返回原始值
	return encoded
}
