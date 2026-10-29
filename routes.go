package main

import (
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxResourceRoutes = 100_000
const resourceRouteTTL = 90 * 24 * time.Hour

type resourceRoute struct {
	URL      string
	Username string
	Password string
	LastUsed time.Time
}

func RegisterFeedCredentials(feedURL, username, password string) {
	if !validResourceURL(feedURL) || hasURLCredentials(feedURL) || (username == "" && password == "") {
		return
	}
	token := NewAuthManager().SignScopedFeedURL(feedURL)
	key := resourceRouteKey(ResourceFeed, token)
	resourceRouteTable.Lock()
	pruneResourceRoutesLocked(time.Now())
	resourceRouteTable.entries[key] = resourceRoute{URL: feedURL, Username: username, Password: password, LastUsed: time.Now()}
	resourceRouteTable.Unlock()
}

var resourceRouteTable = struct {
	sync.Mutex
	entries map[string]resourceRoute
}{entries: make(map[string]resourceRoute)}

func resourceRouteKey(resource ProxyResource, token string) string {
	return string(resource) + ":" + token
}

// RegisterResourceRoute remembers the latest source URL seen for a stable,
// resource-scoped token. RSS CDNs commonly rotate signed query parameters;
// PinePods may retain an older enclosure URL when its path/GUID is unchanged.
func RegisterResourceRoute(resource ProxyResource, token, rawURL string, credentials ...string) {
	if token == "" || !validResourceURL(rawURL) || hasURLCredentials(rawURL) {
		return
	}
	username, password := "", ""
	if len(credentials) > 0 {
		username = credentials[0]
	}
	if len(credentials) > 1 {
		password = credentials[1]
	}
	key := resourceRouteKey(resource, token)
	resourceRouteTable.Lock()
	pruneResourceRoutesLocked(time.Now())
	resourceRouteTable.entries[key] = resourceRoute{URL: rawURL, Username: username, Password: password, LastUsed: time.Now()}
	if len(resourceRouteTable.entries) > maxResourceRoutes {
		var oldestKey string
		var oldest time.Time
		for candidate, route := range resourceRouteTable.entries {
			if oldestKey == "" || route.LastUsed.Before(oldest) {
				oldestKey, oldest = candidate, route.LastUsed
			}
		}
		if oldestKey != "" && oldestKey != key {
			delete(resourceRouteTable.entries, oldestKey)
		}
	}
	resourceRouteTable.Unlock()
}

func RegisterResourceCredentials(resource ProxyResource, token, username, password string) {
	if token == "" || (username == "" && password == "") {
		return
	}
	key := resourceRouteKey(resource, token)
	resourceRouteTable.Lock()
	if entry, ok := resourceRouteTable.entries[key]; ok {
		entry.Username = username
		entry.Password = password
		entry.LastUsed = time.Now()
		resourceRouteTable.entries[key] = entry
	}
	resourceRouteTable.Unlock()
}

// ResolveResourceRoute returns the newest registered source URL for a scoped
// token, or the URL embedded in a legacy link when no route exists.
func ResolveResourceRoute(resource ProxyResource, token, fallbackURL string) string {
	url, _, _ := ResolveResourceDetails(resource, token, fallbackURL)
	return url
}

func ResolveResourceDetails(resource ProxyResource, token, fallbackURL string) (string, string, string) {
	if !strings.HasPrefix(token, "s_") {
		return fallbackURL, "", ""
	}
	key := resourceRouteKey(resource, token)
	resourceRouteTable.Lock()
	defer resourceRouteTable.Unlock()
	entry, ok := resourceRouteTable.entries[key]
	if !ok || time.Since(entry.LastUsed) > resourceRouteTTL {
		delete(resourceRouteTable.entries, key)
		return fallbackURL, "", ""
	}
	entry.LastUsed = time.Now()
	resourceRouteTable.entries[key] = entry
	return entry.URL, entry.Username, entry.Password
}

func UpdateResourceRouteURL(resource ProxyResource, token, rawURL string) {
	if token == "" || !validResourceURL(rawURL) || hasURLCredentials(rawURL) {
		return
	}
	key := resourceRouteKey(resource, token)
	resourceRouteTable.Lock()
	if entry, ok := resourceRouteTable.entries[key]; ok {
		entry.URL = rawURL
		entry.LastUsed = time.Now()
		resourceRouteTable.entries[key] = entry
	}
	resourceRouteTable.Unlock()
}

func LookupResourceCredentials(resource ProxyResource, token string) (string, string) {
	if !strings.HasPrefix(token, "s_") {
		return "", ""
	}
	key := resourceRouteKey(resource, token)
	resourceRouteTable.Lock()
	defer resourceRouteTable.Unlock()
	entry, ok := resourceRouteTable.entries[key]
	if !ok || time.Since(entry.LastUsed) > resourceRouteTTL {
		delete(resourceRouteTable.entries, key)
		return "", ""
	}
	return entry.Username, entry.Password
}

func pruneResourceRoutesLocked(now time.Time) {
	for key, entry := range resourceRouteTable.entries {
		if now.Sub(entry.LastUsed) > resourceRouteTTL {
			delete(resourceRouteTable.entries, key)
		}
	}
}

func mediaTokenFromRequest(rURL *url.URL, prefix string) string {
	path := strings.TrimPrefix(rURL.Path, prefix)
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
