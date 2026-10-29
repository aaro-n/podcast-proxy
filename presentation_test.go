package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func resetPresentationFeedState(t *testing.T) {
	t.Helper()
	feedCacheOnce = sync.Once{}
	feedCache = nil
	feedLocksMu.Lock()
	feedLocks = make(map[string]*feedRequestLock)
	feedLocksMu.Unlock()
	feedSlotsOnce = sync.Once{}
	feedSlots = nil
	mediaSlotsOnce = sync.Once{}
	mediaSlots = nil
	resourceRouteTable.Lock()
	resourceRouteTable.entries = make(map[string]resourceRoute)
	resourceRouteTable.Unlock()
	t.Cleanup(func() {
		feedCacheOnce = sync.Once{}
		feedCache = nil
		feedSlotsOnce = sync.Once{}
		feedSlots = nil
		mediaSlotsOnce = sync.Once{}
		mediaSlots = nil
		resourceRouteTable.Lock()
		resourceRouteTable.entries = make(map[string]resourceRoute)
		resourceRouteTable.Unlock()
	})
}

func TestAcceptsHTML(t *testing.T) {
	tests := []struct {
		accept string
		want   bool
	}{
		{"text/html", true},
		{"application/rss+xml;q=0.5, text/html;q=0.8", true},
		{"application/rss+xml, text/html;q=0.8", false},
		{"text/html;q=0.4, application/rss+xml;q=0.9", false},
		{"text/html;q=0", false},
		{"text/html;q=0.000, application/rss+xml", false},
		{"application/xml, */*;q=0.1", false},
		{"application/xhtml+xml", true},
		{"application/rss+xml", false},
		{"application/rss+xml;q=1, */*;q=0.1", false},
	}
	for _, test := range tests {
		if got := acceptsHTML(test.accept); got != test.want {
			t.Errorf("acceptsHTML(%q) = %v; want %v", test.accept, got, test.want)
		}
	}
}

func TestLivePodcastSourcesAgainstPinePods(t *testing.T) {
	if os.Getenv("PODCAST_PROXY_LIVE_FEED_TESTS") != "1" {
		t.Skip("set PODCAST_PROXY_LIVE_FEED_TESTS=1 to check the public podcast feeds")
	}
	feeds := []struct {
		name string
		url  string
	}{
		{"不明白播客 (Acast)", "https://feeds.acast.com/public/shows/68004395b4ef799a7a410371"},
		{"东谈西论 (Omny)", "https://www.omnycontent.com/d/playlist/d9486183-3dd4-4ad6-aebe-a4c1008455d5/d14a562d-2c6f-465d-80d2-ae44009af53e/77c8885e-9b24-4fb4-a01c-ae44009bc0f1/podcast.rss"},
		{"文化土豆 (Acast)", "https://feeds.acast.com/public/shows/5e0b6ece81be870a61107c66"},
		{"翻转电台 (Anchor)", "https://anchor.fm/s/141e652c/podcast/rss"},
		{"科技乱炖 (DAO/Wavpub)", "https://feeds.daopub.com/ld.xml"},
	}
	client := &http.Client{Timeout: 45 * time.Second}
	for _, feed := range feeds {
		feed := feed
		t.Run(feed.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, feed.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml, */*")
			request.Header.Set("User-Agent", "PinePods-Podcast-Proxy-Compatibility-Test/1.0")
			response, err := client.Do(request)
			if err != nil {
				t.Skipf("feed unavailable from this network: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
				t.Fatalf("upstream returned HTTP %d", response.StatusCode)
			}
			const maxFeedSize = 8 << 20
			body, err := io.ReadAll(io.LimitReader(response.Body, maxFeedSize+1))
			if err != nil {
				t.Fatalf("read feed body: %v", err)
			}
			if len(body) > maxFeedSize {
				t.Fatalf("feed exceeds proxy limit of %d bytes", maxFeedSize)
			}
			normalized, err := normalizeFeedXML(string(body))
			if err != nil {
				t.Fatalf("feed charset is unsupported: %v", err)
			}
			if !(&FeedValidator{}).IsFeedContent(normalized) {
				t.Fatal("response is not well-formed RSS/Atom XML")
			}
			model, err := parsePresentationFeed(normalized, feed.url)
			if err != nil {
				t.Fatalf("feed parse failed: %v", err)
			}
			if model.Title == "" || len(model.Episodes) == 0 {
				t.Fatalf("feed title or entries missing: title=%q episodes=%d", model.Title, len(model.Episodes))
			}
			missingTitle, missingMedia := 0, 0
			mediaTypes := make(map[string]int)
			for _, episode := range model.Episodes {
				if strings.TrimSpace(episode.Title) == "" {
					missingTitle++
				}
				if episode.AudioURL == "" || !isPlayableMIME(episode.AudioType) {
					missingMedia++
				}
				mediaTypes[episode.AudioType]++
			}
			if missingTitle > 0 || missingMedia > 0 {
				t.Fatalf("items incompatible with PinePods: missing titles=%d, missing audio/video enclosure=%d", missingTitle, missingMedia)
			}
			t.Logf("HTTP %d, %s, %d episodes, MIME types=%v", response.StatusCode, model.Title, len(model.Episodes), mediaTypes)
		})
	}
}

func TestWantsHTMLPresentation(t *testing.T) {
	request := httptest.NewRequest("GET", "/feed", nil)
	request.Header.Set("Accept", "text/html")
	if !wantsHTMLPresentation(request) {
		t.Fatal("browser Accept header should select HTML")
	}
	request.URL.RawQuery = "display=0"
	if wantsHTMLPresentation(request) {
		t.Fatal("display=0 should force an XML feed")
	}
	request.URL.RawQuery = "display=1"
	request.Header.Set("Accept", "application/rss+xml")
	if !wantsHTMLPresentation(request) {
		t.Fatal("display=1 should select the HTML presentation")
	}
}

func TestParsePresentationRSSFeed(t *testing.T) {
	feed := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd" xmlns:content="http://purl.org/rss/1.0/modules/content/">
  <channel>
    <title>测试播客 &amp; News</title>
    <itunes:author>作者</itunes:author>
    <description>节目介绍</description>
    <image><url>https://example.com/cover.jpg</url></image>
    <item>
      <title>第一期</title>
      <description>普通描述</description>
      <content:encoded><![CDATA[详细介绍]]></content:encoded>
      <pubDate>Tue, 27 Oct 2026 10:00:00 GMT</pubDate>
      <itunes:duration>12:34</itunes:duration>
      <itunes:image href="https://example.com/episode.jpg"/>
      <enclosure url="https://example.com/audio.mp3" type="audio/mpeg"/>
    </item>
    <item><title>第二期</title><enclosure url="https://example.com/video.mp4" type="video/mp4"/></item>
  </channel>
</rss>`

	model, err := parsePresentationFeed(feed, "https://example.com/feed.xml")
	if err != nil {
		t.Fatalf("parsePresentationFeed returned an error: %v", err)
	}
	if model.Title != "测试播客 & News" || model.Author != "作者" || model.Description != "节目介绍" {
		t.Fatalf("unexpected channel metadata: %#v", model)
	}
	if model.ArtworkURL != "https://example.com/cover.jpg" {
		t.Fatalf("channel artwork = %q", model.ArtworkURL)
	}
	if len(model.Episodes) != 2 {
		t.Fatalf("got %d episodes; want 2", len(model.Episodes))
	}
	first := model.Episodes[0]
	if first.Title != "第一期" || first.Description != "普通描述" || first.PubDate != "Tue, 27 Oct 2026 10:00:00 GMT" || first.Duration != "12:34" {
		t.Fatalf("unexpected first episode: %#v", first)
	}
	if first.AudioURL != "https://example.com/audio.mp3" || first.AudioType != "audio/mpeg" || first.IsVideo {
		t.Fatalf("unexpected audio enclosure: %#v", first)
	}
	if first.ArtworkURL != "https://example.com/episode.jpg" {
		t.Fatalf("episode artwork = %q", first.ArtworkURL)
	}
	if model.Episodes[1].AudioType != "video/mp4" || !model.Episodes[1].IsVideo {
		t.Fatalf("video enclosure was not recognized: %#v", model.Episodes[1])
	}
}

func TestParsePresentationAtomFeed(t *testing.T) {
	feed := `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom 播客</title><subtitle>Atom 简介</subtitle><icon>https://example.com/icon.png</icon>
  <author><name>Atom 作者</name></author>
  <entry><title>Atom 第一集</title><summary>单集简介</summary><published>2026-10-27T10:00:00Z</published><link rel="enclosure" href="https://example.com/atom.mp3" type="audio/mpeg"/></entry>
</feed>`
	model, err := parsePresentationFeed(feed, "https://example.com/feed.atom")
	if err != nil {
		t.Fatalf("parsePresentationFeed returned an error: %v", err)
	}
	if model.Title != "Atom 播客" || model.Author != "Atom 作者" || model.Description != "Atom 简介" || model.ArtworkURL != "https://example.com/icon.png" {
		t.Fatalf("unexpected Atom metadata: %#v", model)
	}
	if len(model.Episodes) != 1 || model.Episodes[0].AudioURL != "https://example.com/atom.mp3" || model.Episodes[0].Title != "Atom 第一集" {
		t.Fatalf("unexpected Atom episode: %#v", model.Episodes)
	}
}

func TestFeedTransformerDoesNotEmitXSLT(t *testing.T) {
	config = &ProxyConfig{APIKey: "test-key"}
	feed := `<?xml version="1.0"?><rss version="2.0"><channel><title>Sample</title><item><title>Episode</title><enclosure url="episode.mp3" length="10"/></item></channel></rss>`
	builder := &ProxyURLBuilder{scheme: "https", host: "proxy.example", sourceURL: "https://origin.example/feed.xml"}
	transformed, err := NewFeedTransformer().TransformStrict(feed, builder, "test-key", true)
	if err != nil {
		t.Fatalf("TransformStrict returned an error: %v", err)
	}
	if strings.Contains(strings.ToLower(transformed), "xml-stylesheet") || strings.Contains(transformed, "<proxy:feed") {
		t.Fatalf("transformed feed contains legacy XSLT wrapper: %s", transformed)
	}
	if err := (&FeedValidator{}).ValidateTransformedFeed(transformed); err != nil {
		t.Fatalf("transformed output is not valid XML: %v", err)
	}
	if !strings.Contains(transformed, `type="audio/mpeg"`) {
		t.Fatalf("inferred audio MIME type missing from transformed feed: %s", transformed)
	}
	if strings.Contains(transformed, "/podcast.xsl/") {
		t.Fatalf("normal feed must not reference the legacy stylesheet route: %s", transformed)
	}
}

func TestRenderPodcastPageEscapesContentAndHasLegacyFallback(t *testing.T) {
	config = &ProxyConfig{APIKey: "test-key"}
	feed := `<?xml version="1.0"?><rss version="2.0"><channel><title>&lt;script&gt;bad()&lt;/script&gt;</title><description>Feed description</description><item><title>Episode &amp; more</title><description>Episode description</description><enclosure url="https://example.com/audio.mp3" type="audio/mpeg"/></item></channel></rss>`
	builder := &ProxyURLBuilder{scheme: "https", host: "proxy.example", sourceURL: "https://origin.example/feed.xml"}
	transformed, err := NewFeedTransformer().TransformStrict(feed, builder, "test-key", false)
	if err != nil {
		t.Fatalf("TransformStrict returned an error: %v", err)
	}
	page, err := renderPodcastPage(transformed, builder.BuildFeedURL("test-key"))
	if err != nil {
		t.Fatalf("renderPodcastPage returned an error: %v", err)
	}
	html := string(page)
	if strings.Contains(html, "<h1 class=\"title\"><script>") || !strings.Contains(html, "&lt;script&gt;bad()&lt;/script&gt;") {
		t.Fatalf("feed title was not HTML-escaped: %s", html)
	}
	if strings.Contains(html, "data-search=\"&lt;script&gt;") {
		t.Fatal("feed title should be plain text in search metadata")
	}
	for _, want := range []string{"Episode &amp; more", "<audio class=\"player\" controls", "如果播放器不可用，点击打开媒体文件", "https://proxy.example/audio/"} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered page is missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(html), "xml-stylesheet") {
		t.Fatal("native HTML response should not reference XSLT")
	}
}

func TestForceXMLFeedURL(t *testing.T) {
	got := forceXMLFeedURL("https://proxy.example/feed?url=https%3A%2F%2Forigin.example%2Frss&feed_token=s_token")
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("forceXMLFeedURL returned invalid URL %q: %v", got, err)
	}
	if parsed.Query().Get("display") != "0" || parsed.Query().Get("feed_token") != "s_token" {
		t.Fatalf("forced XML URL lost expected query values: %s", got)
	}
}

func TestFeedHandlerContentNegotiation(t *testing.T) {
	resetPresentationFeedState(t)
	config = &ProxyConfig{APIKey: "test-key", PublicHost: "proxy.example", FeedMaxBytes: 1 << 20, FeedMaxConcurrency: 4, MediaMaxConcurrency: 4, EgressGuard: false, EgressAllowedPorts: map[string]bool{"443": true}}
	feedXML := `<?xml version="1.0"?><rss version="2.0"><channel><title>测试播客</title><description>简介</description><item><title>第一期</title><description>说明</description><enclosure url="https://media.example/audio.mp3" type="audio/mpeg"/></item></channel></rss>`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Header().Set("Cache-Control", "public, max-age=60")
		_, _ = w.Write([]byte(feedXML))
	}))
	defer upstream.Close()
	feedURL := upstream.URL + "/feed.xml"
	server := NewServer(config)
	server.RegisterRoutes()
	proxyBuilder := &ProxyURLBuilder{scheme: "https", host: config.PublicHost}

	request := httptest.NewRequest(http.MethodGet, proxyBuilder.BuildFeedURLFor("test-key", feedURL), nil)
	request.Header.Set("Accept", "text/html")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("browser request got status=%d content-type=%q body=%s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	if strings.Contains(strings.ToLower(response.Body.String()), "xml-stylesheet") || !strings.Contains(response.Body.String(), "第一期") {
		t.Fatalf("browser HTML response is missing expected page or contains XSLT: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "/podcast.xsl/") {
		t.Fatal("HTML page should not reference legacy XSLT")
	}

	request = httptest.NewRequest(http.MethodGet, proxyBuilder.BuildFeedURLFor("test-key", feedURL), nil)
	request.Header.Set("Accept", "application/rss+xml")
	response = httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "application/rss+xml") {
		t.Fatalf("PinePods request got status=%d content-type=%q body=%s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	if strings.Contains(strings.ToLower(response.Body.String()), "xml-stylesheet") || strings.Contains(response.Body.String(), "<html") {
		t.Fatalf("PinePods XML response contains presentation markup: %s", response.Body.String())
	}
	if vary := response.Header().Get("Vary"); !strings.Contains(vary, "Accept") {
		t.Errorf("Vary header = %q; expected Accept", vary)
	}
}

func TestAudioHandlerPreservesRangeForFeedEnclosure(t *testing.T) {
	resetPresentationFeedState(t)
	config = &ProxyConfig{APIKey: "test-key", PublicHost: "proxy.example", MediaMaxConcurrency: 4, EgressGuard: false}
	var sourceRange string
	mediaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceRange = r.Header.Get("Range")
		if sourceRange != "bytes=0-1" {
			t.Errorf("upstream Range = %q", sourceRange)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", "bytes 0-1/4")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("ab"))
	}))
	defer mediaServer.Close()

	feed := fmt.Sprintf(`<rss version="2.0"><channel><title>Range test</title><item><title>Episode</title><enclosure url="%s/audio.mp3" length="4" type="audio/mpeg"/></item></channel></rss>`, mediaServer.URL)
	builder := &ProxyURLBuilder{scheme: "http", host: "proxy.example", sourceURL: "https://origin.example/feed.xml"}
	transformed, err := NewFeedTransformer().TransformStrict(feed, builder, "test-key", false)
	if err != nil {
		t.Fatalf("transform feed: %v", err)
	}
	var parsed struct {
		Channel struct {
			Item struct {
				Enclosure struct {
					URL string `xml:"url,attr"`
				} `xml:"enclosure"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal([]byte(transformed), &parsed); err != nil {
		t.Fatalf("read transformed enclosure: %v", err)
	}
	proxyURL, err := url.Parse(parsed.Channel.Item.Enclosure.URL)
	if err != nil || proxyURL.Path == "" {
		t.Fatalf("invalid proxy enclosure URL %q: %v", parsed.Channel.Item.Enclosure.URL, err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://proxy.example"+proxyURL.RequestURI(), nil)
	request.Header.Set("Range", "bytes=0-1")
	response := httptest.NewRecorder()
	NewAudioHandler(request).Handle(response, request)
	if response.Code != http.StatusPartialContent {
		t.Fatalf("audio handler status = %d, body=%q", response.Code, response.Body.String())
	}
	contentRange := response.Header().Get("Content-Range")
	if !strings.HasPrefix(contentRange, "bytes 0-1/") {
		t.Fatalf("Content-Range = %q", contentRange)
	}
	if response.Body.String() != "ab" || sourceRange != "bytes=0-1" {
		t.Fatalf("range body=%q, upstream Range=%q", response.Body.String(), sourceRange)
	}
}

func TestAudioDirectQueryRedirects(t *testing.T) {
	resetPresentationFeedState(t)
	config = &ProxyConfig{APIKey: "direct-key", PublicHost: "proxy.example", MediaMaxConcurrency: 4, EgressGuard: false}
	target := "https://media.example/episode.mp3?signature=abc"
	token := NewAuthManager().SignResource(ResourceAudio, target)
	request := httptest.NewRequest(http.MethodGet, "/audio/"+token+"/episode.mp3?url="+url.QueryEscape(target)+"&direct=1", nil)
	response := httptest.NewRecorder()
	NewAudioHandler(request).Handle(response, request)
	if response.Code != http.StatusTemporaryRedirect || response.Header().Get("Location") != target {
		t.Fatalf("audio direct response status=%d location=%q", response.Code, response.Header().Get("Location"))
	}

	token = NewAuthManager().SignResource(ResourceImage, target)
	request = httptest.NewRequest(http.MethodGet, "/image/"+token+"/cover.jpg?url="+url.QueryEscape(target)+"&direct=1", nil)
	response = httptest.NewRecorder()
	NewImageHandler(request).Handle(response, request)
	if response.Code != http.StatusTemporaryRedirect || response.Header().Get("Location") != target {
		t.Fatalf("image direct response status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
}

func TestFeedCacheFreshnessAndAge(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	headers := make(http.Header)
	headers.Set("Cache-Control", "public, max-age=120, stale-if-error=600")
	headers.Set("Age", "30")
	ttl, lifetime, age, noStore, private, noCache := feedCacheTTLFromHeaders(headers, now)
	if ttl != 90*time.Second || lifetime != 120*time.Second || age != 30*time.Second || noStore || private || noCache {
		t.Fatalf("unexpected cache freshness: ttl=%v lifetime=%v age=%v noStore=%v private=%v noCache=%v", ttl, lifetime, age, noStore, private, noCache)
	}

	requestHeaders := make(http.Header)
	requestHeaders.Set("Accept-Language", "zh-CN")
	varyValues, ok := feedCacheVariantHeaders("Accept-Language, Accept-Encoding", requestHeaders)
	if !ok || len(varyValues) != 2 || varyValues["accept-language"] != "zh-CN" || varyValues["accept-encoding"] != "" {
		t.Fatalf("unexpected Vary request values: %#v, ok=%v", varyValues, ok)
	}
	entry := FeedCacheEntry{VaryRequestHeaders: varyValues}
	if !entry.matchesRequestVary(requestHeaders) {
		t.Fatal("matching request Vary headers should match cache entry")
	}
	requestHeaders.Set("Accept-Language", "en-US")
	if entry.matchesRequestVary(requestHeaders) {
		t.Fatal("different request Vary header should miss cache")
	}
	if _, ok := feedCacheVariantHeaders("*", requestHeaders); ok {
		t.Fatal("Vary: * cannot be used as a shared cache variant")
	}
}

func TestFeedCacheExpiresAgeAndNoCacheWithoutValidators(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	headers := make(http.Header)
	headers.Set("Date", now.Add(-10*time.Second).Format(http.TimeFormat))
	headers.Set("Expires", now.Add(50*time.Second).Format(http.TimeFormat))
	ttl, lifetime, age, noStore, private, noCache := feedCacheTTLFromHeaders(headers, now)
	if ttl != 50*time.Second || lifetime != time.Minute || age != 10*time.Second || noStore || private || noCache {
		t.Fatalf("Expires/Date age incorrect: ttl=%v lifetime=%v age=%v noStore=%v private=%v noCache=%v", ttl, lifetime, age, noStore, private, noCache)
	}

	noCacheHeaders := make(http.Header)
	noCacheHeaders.Set("Cache-Control", "no-cache")
	_, _, _, noStore, private, noCache = feedCacheTTLFromHeaders(noCacheHeaders, now)
	if noStore || private || !noCache {
		t.Fatalf("no-cache must require revalidation: noStore=%v private=%v noCache=%v", noStore, private, noCache)
	}
	if got := feedStaleIfError("public, stale-if-error=9999999"); got > 24*time.Hour {
		t.Fatalf("stale-if-error should be bounded, got %v", got)
	}
	if ttl, _, _, _, _, noCache := feedCacheTTLFromHeaders(make(http.Header), now); ttl != 0 || !noCache {
		t.Fatalf("absence of HTTP freshness metadata must not invent freshness: ttl=%v noCache=%v", ttl, noCache)
	}
}

func TestFeedCacheConditionalValidatorsAndRange(t *testing.T) {
	entry := FeedCacheEntry{
		Body:         []byte("<rss/>"),
		ETag:         `W/"proxy-etag"`,
		LastModified: "Wed, 28 Oct 2026 11:59:00 GMT",
		CacheControl: "public, max-age=60, must-revalidate",
		ContentType:  "application/rss+xml; charset=utf-8",
		Vary:         "Accept, Accept-Encoding",
		Cacheable:    true,
	}

	request := httptest.NewRequest(http.MethodGet, "/feed", nil)
	request.Header.Set("If-None-Match", `"proxy-etag"`)
	response := httptest.NewRecorder()
	serveCachedFeed(response, request, request.Header.Values("If-None-Match"), entry)
	if response.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match weak comparison status = %d", response.Code)
	}
	if response.Header().Get("Content-Length") != "" {
		t.Fatal("304 response should not advertise a body Content-Length")
	}

	request = httptest.NewRequest(http.MethodGet, "/feed", nil)
	request.Header.Set("If-Modified-Since", "Wed, 28 Oct 2026 11:59:00 GMT")
	response = httptest.NewRecorder()
	serveCachedFeed(response, request, nil, entry)
	if response.Code != http.StatusNotModified {
		t.Fatalf("If-Modified-Since status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/feed", nil)
	request.Header.Set("Range", "bytes=0-1")
	request.Header.Set("If-None-Match", entry.ETag)
	response = httptest.NewRecorder()
	serveCachedFeed(response, request, request.Header.Values("If-None-Match"), entry)
	if response.Code != http.StatusOK || response.Body.String() != string(entry.Body) {
		t.Fatalf("feed Range should return complete XML, status=%d body=%q", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/feed", nil)
	request.Header.Set("Range", "bytes=0-1")
	request.Header.Set("If-Range", entry.ETag)
	response = httptest.NewRecorder()
	serveCachedFeed(response, request, nil, entry)
	if response.Code != http.StatusOK || response.Body.String() != string(entry.Body) {
		t.Fatalf("feed If-Range should return complete XML, status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestFeedCacheVaryUsesOriginRequestHeaders(t *testing.T) {
	resetPresentationFeedState(t)
	config = &ProxyConfig{APIKey: "vary-key", PublicHost: "proxy.example", FeedMaxBytes: 1 << 20, FeedMaxConcurrency: 4, EgressGuard: false}
	feedXML := `<?xml version="1.0"?><rss version="2.0"><channel><title>Vary test</title><item><title>Episode</title><enclosure url="https://media.example/a.mp3" type="audio/mpeg"/></item></channel></rss>`
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("Vary", "Accept-Language")
		_, _ = w.Write([]byte(feedXML))
	}))
	defer upstream.Close()
	feedURL := upstream.URL + "/feed.xml"
	requestURL := (&ProxyURLBuilder{scheme: "http", host: "proxy.example"}).BuildFeedURLFor("vary-key", feedURL)
	server := NewServer(config)
	server.RegisterRoutes()

	request := httptest.NewRequest(http.MethodGet, requestURL, nil)
	request.Header.Set("Accept-Language", "zh-CN")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || calls != 1 {
		t.Fatalf("first variant status=%d calls=%d", response.Code, calls)
	}
	if got := response.Header().Get("Vary"); !strings.Contains(strings.ToLower(got), "accept-language") || !strings.Contains(strings.ToLower(got), "accept-encoding") {
		t.Fatalf("downstream Vary = %q", got)
	}

	request = httptest.NewRequest(http.MethodGet, requestURL, nil)
	request.Header.Set("Accept-Language", "en-US")
	response = httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || calls != 2 {
		t.Fatalf("second Vary variant should go to origin: status=%d calls=%d", response.Code, calls)
	}
	request = httptest.NewRequest(http.MethodGet, requestURL, nil)
	request.Header.Set("Accept-Language", "zh-CN")
	response = httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || calls != 2 {
		t.Fatalf("first Vary variant should hit its isolated cache entry: status=%d calls=%d", response.Code, calls)
	}
}

func TestFeed304RefreshRetainsOmittedValidators(t *testing.T) {
	entry := FeedCacheEntry{
		Body:               []byte("<rss/>"),
		ETag:               `"origin-etag"`,
		SourceETag:         `"origin-etag"`,
		SourceLastModified: "Wed, 28 Oct 2026 11:00:00 GMT",
		SourceCacheControl: "public, max-age=60",
		SourceDate:         "Wed, 28 Oct 2026 12:00:00 GMT",
		SourceAgePresent:   true,
		LastModified:       "Wed, 28 Oct 2026 11:00:00 GMT",
		Vary:               "Accept, Accept-Encoding",
		Cacheable:          true,
	}
	entry.StoredAt = time.Now().Add(-10 * time.Second)
	refreshed := refreshCachedFeedHeaders(entry, make(http.Header), http.Header{
		"User-Agent":      {"PinePods/1.0 PodcastProxy/2.1"},
		"Accept":          {"*/*"},
		"Accept-Encoding": {"gzip, identity;q=0.5"},
	})
	if refreshed.SourceETag != entry.SourceETag || refreshed.SourceLastModified != entry.SourceLastModified {
		t.Fatalf("304 without validator headers must retain validators: etag=%q modified=%q", refreshed.SourceETag, refreshed.SourceLastModified)
	}
	updated := refreshCachedFeedHeaders(entry, http.Header{"Etag": {`"origin-etag-v2"`}}, http.Header{})
	if updated.ETag != `"origin-etag-v2"` {
		t.Fatalf("RSS ETag should follow an updated origin ETag on 304, got %q", updated.ETag)
	}
	if refreshed.SourceAge < entry.SourceAge || refreshed.ExpiresAt.Before(time.Now()) {
		t.Fatalf("304 refresh should account for residency and refresh TTL: age=%v expires=%v", refreshed.SourceAge, refreshed.ExpiresAt)
	}
}

func TestFeedUsesNewBodyWhenOriginValidatorChanges(t *testing.T) {
	resetPresentationFeedState(t)
	config = &ProxyConfig{APIKey: "changed-feed-key", PublicHost: "proxy.example", FeedMaxBytes: 1 << 20, FeedMaxConcurrency: 4, EgressGuard: false}
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Cache-Control", "public, max-age=60")
		if calls == 1 {
			w.Header().Set("ETag", `"v1"`)
			_, _ = w.Write([]byte(`<rss version="2.0"><channel><title>Version one</title></channel></rss>`))
			return
		}
		if got := r.Header.Get("If-None-Match"); got != `"v1"` {
			t.Errorf("revalidation If-None-Match = %q, want origin v1", got)
		}
		w.Header().Set("ETag", `"v2"`)
		_, _ = w.Write([]byte(`<rss version="2.0"><channel><title>Version two</title></channel></rss>`))
	}))
	defer upstream.Close()
	feedURL := upstream.URL + "/feed.xml"
	requestURL := (&ProxyURLBuilder{scheme: "http", host: "proxy.example"}).BuildFeedURLFor("changed-feed-key", feedURL)
	server := NewServer(config)
	server.RegisterRoutes()
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, requestURL, nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Version one") {
		t.Fatalf("first response status=%d body=%q", response.Code, response.Body.String())
	}
	cache := getFeedCache()
	cache.mu.Lock()
	var key string
	var entry FeedCacheEntry
	for candidate, cached := range cache.entries {
		key, entry = candidate, cached
		break
	}
	cache.mu.Unlock()
	if key == "" {
		t.Fatal("expected first response to be cached")
	}
	entry.ExpiresAt = time.Now().Add(-time.Second)
	cache.Set(key, entry)
	response = httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, requestURL, nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Version two") || response.Header().Get("ETag") != `"v2"` {
		t.Fatalf("updated origin response status=%d etag=%q body=%q", response.Code, response.Header().Get("ETag"), response.Body.String())
	}
	if calls != 2 {
		t.Fatalf("origin calls=%d, want 2", calls)
	}
}

func TestFeedPrivateCachePolicy(t *testing.T) {
	date := time.Now().UTC().Format(http.TimeFormat)
	for _, test := range []struct {
		cacheControl string
		wantPrivate  bool
		wantNoStore  bool
	}{
		{"private, max-age=60", true, false},
		{"no-store", false, true},
	} {
		headers := make(http.Header)
		headers.Set("Cache-Control", test.cacheControl)
		headers.Set("Date", date)
		_, _, _, noStore, private, _ := feedCacheTTLFromHeaders(headers, time.Now())
		if noStore != test.wantNoStore || private != test.wantPrivate {
			t.Errorf("Cache-Control %q => noStore=%v private=%v", test.cacheControl, noStore, private)
		}
	}
}

func TestFeedCacheAgeUsesResidentTime(t *testing.T) {
	entry := FeedCacheEntry{SourceAge: 30 * time.Second, StoredAt: time.Now().Add(-12 * time.Second)}
	if got := currentFeedAge(entry, time.Now()); got < 42*time.Second || got > 44*time.Second {
		t.Fatalf("currentFeedAge = %v, expected roughly 42s", got)
	}
}

func TestFeedCacheConditionalRevalidationAndNoCache(t *testing.T) {
	resetPresentationFeedState(t)
	config = &ProxyConfig{APIKey: "cache-key", PublicHost: "proxy.example", FeedMaxBytes: 1 << 20, FeedMaxConcurrency: 4, EgressGuard: false}
	feedXML := `<?xml version="1.0"?><rss version="2.0"><channel><title>Validator test</title><item><title>Episode</title><enclosure url="https://media.example/a.mp3" type="audio/mpeg"/></item></channel></rss>`
	var originETag, originModified string
	var originCalls int
	var lastIfNoneMatch, lastIfModifiedSince string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls++
		lastIfNoneMatch = r.Header.Get("If-None-Match")
		lastIfModifiedSince = r.Header.Get("If-Modified-Since")
		w.Header().Set("ETag", originETag)
		w.Header().Set("Last-Modified", originModified)
		w.Header().Set("Cache-Control", "public, max-age=60, stale-if-error=120")
		if r.Header.Get("If-None-Match") == originETag || (r.Header.Get("If-Modified-Since") == originModified && originModified != "") {
			w.Header().Set("Age", "5")
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
		_, _ = w.Write([]byte(feedXML))
	}))
	defer upstream.Close()

	originETag = `"origin-v1"`
	originModified = "Wed, 28 Oct 2026 11:00:00 GMT"
	proxyBuilder := &ProxyURLBuilder{scheme: "http", host: "proxy.example"}
	feedURL := upstream.URL + "/feed.xml"
	feedRequestURL := proxyBuilder.BuildFeedURLFor("cache-key", feedURL)
	server := NewServer(config)
	server.RegisterRoutes()

	request := httptest.NewRequest(http.MethodGet, feedRequestURL, nil)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("ETag") != originETag {
		t.Fatalf("first representation status=%d ETag=%q", response.Code, response.Header().Get("ETag"))
	}
	publicETag := response.Header().Get("ETag")
	if got := response.Header().Get("Cache-Control"); got != "public, max-age=60, stale-if-error=120" {
		t.Fatalf("downstream Cache-Control = %q", got)
	}
	if response.Header().Get("Age") != "0" {
		t.Fatalf("first response Age = %q; want 0", response.Header().Get("Age"))
	}
	if response.Header().Get("Date") == "" {
		t.Fatal("cacheable representation should include a Date header")
	}
	if got := response.Header().Get("Vary"); !strings.Contains(got, "Accept") || !strings.Contains(got, "Accept-Encoding") {
		t.Fatalf("first response missing proxy representation Vary headers: %q", got)
	}

	request = httptest.NewRequest(http.MethodGet, feedRequestURL, nil)
	request.Header.Set("If-None-Match", `"different-origin-etag"`)
	response = httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || originCalls != 2 || lastIfNoneMatch != `"different-origin-etag"` || response.Header().Get("ETag") != originETag {
		t.Fatalf("fresh cache conditional status=%d origin calls=%d", response.Code, originCalls)
	}
	if response.Header().Get("Cache-Control") != "public, max-age=60, stale-if-error=120" {
		t.Fatalf("304 response Cache-Control = %q", response.Header().Get("Cache-Control"))
	}

	cache := getFeedCache()
	cache.mu.Lock()
	var key string
	var cached FeedCacheEntry
	for candidate, entry := range cache.entries {
		key, cached = candidate, entry
		break
	}
	cache.mu.Unlock()
	if key == "" {
		t.Fatal("fresh feed not in cache")
	}
	cached.ExpiresAt = time.Now().Add(-time.Second)
	getFeedCache().Set(key, cached)
	request = httptest.NewRequest(http.MethodGet, feedRequestURL, nil)
	request.Header.Set("If-None-Match", publicETag)
	response = httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotModified || lastIfNoneMatch != publicETag || lastIfModifiedSince != "" {
		t.Fatalf("source revalidation status=%d origin etag=%q modified=%q", response.Code, lastIfNoneMatch, lastIfModifiedSince)
	}
	if response.Header().Get("Age") == "" {
		t.Fatal("304 revalidation response should include Age")
	}
	if response.Header().Get("ETag") != originETag {
		t.Fatalf("origin ETag should be passed through, got %q", response.Header().Get("ETag"))
	}
	if originCalls != 3 {
		t.Fatalf("expected every RSS If-None-Match request to be decided by origin, got %d origin calls", originCalls)
	}

	resetPresentationFeedState(t)
	originCalls = 0
	upstream.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls++
		w.Header().Set("ETag", originETag)
		w.Header().Set("Cache-Control", "no-cache")
		if r.Header.Get("If-None-Match") == originETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write([]byte(feedXML))
	})
	request = httptest.NewRequest(http.MethodGet, feedRequestURL, nil)
	response = httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	request = httptest.NewRequest(http.MethodGet, feedRequestURL, nil)
	response = httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if originCalls != 2 || response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-cache, must-revalidate" {
		t.Fatalf("no-cache revalidation calls=%d status=%d cache-control=%q", originCalls, response.Code, response.Header().Get("Cache-Control"))
	}
	if response.Header().Get("ETag") == "" {
		t.Fatal("no-cache feed should retain representation ETag for conditional clients")
	}
}
