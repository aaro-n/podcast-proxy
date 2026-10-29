package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"html"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type presentationFeed struct {
	Title       string
	Author      string
	Description string
	ArtworkURL  string
	FeedURL     string
	Episodes    []presentationEpisode
}

type presentationEpisode struct {
	Title       string
	Description string
	PubDate     string
	Duration    string
	AudioURL    string
	AudioType   string
	ArtworkURL  string
	IsVideo     bool
}

type presentationCapture struct {
	field   string
	depth   int
	item    *presentationEpisode
	builder strings.Builder
}

var (
	presentationHTMLTagRE     = regexp.MustCompile(`(?s)<[^>]*>`)
	presentationScriptBlockRE = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	presentationStyleBlockRE  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`)
)

func acceptsHTML(header string) bool {
	htmlQuality := -1.0
	xmlQuality := -1.0
	for _, mediaRange := range strings.Split(strings.ToLower(header), ",") {
		parts := strings.Split(mediaRange, ";")
		mediaType := strings.TrimSpace(parts[0])
		quality := 1.0
		for _, parameter := range parts[1:] {
			parameter = strings.TrimSpace(parameter)
			if strings.HasPrefix(parameter, "q=") {
				if parsed, err := strconv.ParseFloat(strings.TrimPrefix(parameter, "q="), 64); err == nil {
					if parsed < 0 || parsed > 1 {
						quality = 0
					} else {
						quality = parsed
					}
				}
			}
		}
		switch mediaType {
		case "text/html", "application/xhtml+xml":
			if quality > htmlQuality {
				htmlQuality = quality
			}
		case "application/rss+xml", "application/atom+xml", "application/xml", "text/xml":
			if quality > xmlQuality {
				xmlQuality = quality
			}
		}
	}
	return htmlQuality > 0 && (xmlQuality < 0 || htmlQuality >= xmlQuality)
}

func wantsHTMLPresentation(r *http.Request) bool {
	mode := r.URL.Query().Get("display")
	if mode == "1" {
		return true
	}
	if mode == "0" {
		return false
	}
	return acceptsHTML(r.Header.Get("Accept"))
}

func forceXMLFeedURL(feedURL string) string {
	parsed, err := url.Parse(feedURL)
	if err != nil {
		return feedURL
	}
	query := parsed.Query()
	query.Set("display", "0")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func parsePresentationFeed(feedXML, subscriptionURL string) (presentationFeed, error) {
	model := presentationFeed{FeedURL: subscriptionURL}
	decoder := newXMLDecoder(feedXML)
	decoder.Strict = true
	var stack []xml.Name
	var episode *presentationEpisode
	var capture *presentationCapture
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return presentationFeed{}, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			local := strings.ToLower(value.Name.Local)
			parent := ""
			if len(stack) > 0 {
				parent = strings.ToLower(stack[len(stack)-1].Local)
			}
			if local == "item" || (local == "entry" && value.Name.Space == atomNamespace) {
				episode = &presentationEpisode{}
			}
			for _, attr := range value.Attr {
				if attr.Name.Space != "" {
					continue
				}
				attrName := strings.ToLower(attr.Name.Local)
				switch {
				case episode != nil && local == "enclosure" && attrName == "url":
					typeName := attribute(value.Attr, "type")
					if isPlayableMIME(typeName) {
						episode.AudioURL, episode.AudioType = attr.Value, typeName
						episode.IsVideo = strings.HasPrefix(strings.ToLower(typeName), "video/")
					}
				case episode != nil && local == "link" && value.Name.Space == atomNamespace && parent == "entry" && attrName == "href":
					rel := strings.ToLower(attribute(value.Attr, "rel"))
					typeName := attribute(value.Attr, "type")
					if rel == "enclosure" && isPlayableMIME(typeName) {
						episode.AudioURL, episode.AudioType = attr.Value, typeName
						episode.IsVideo = strings.HasPrefix(strings.ToLower(typeName), "video/")
					}
				case local == "image" && attrName == "href" && strings.Contains(strings.ToLower(value.Name.Space), "itunes"):
					if episode != nil {
						episode.ArtworkURL = attr.Value
					} else if model.ArtworkURL == "" {
						model.ArtworkURL = attr.Value
					}
				case local == "thumbnail" && attrName == "url" && strings.Contains(strings.ToLower(value.Name.Space), "mrss"):
					if episode != nil && episode.ArtworkURL == "" {
						episode.ArtworkURL = attr.Value
					}
				case local == "content" && attrName == "url" && strings.Contains(strings.ToLower(value.Name.Space), "mrss"):
					if episode != nil {
						typeName := strings.ToLower(attribute(value.Attr, "type"))
						switch {
						case isPlayableMIME(typeName) && episode.AudioURL == "":
							episode.AudioURL, episode.AudioType = attr.Value, typeName
							episode.IsVideo = strings.HasPrefix(typeName, "video/")
						case strings.HasPrefix(typeName, "image/") && episode.ArtworkURL == "":
							episode.ArtworkURL = attr.Value
						}
					}
				}
			}
			field := presentationTextField(value.Name, episode != nil, parent)
			if field != "" {
				capture = &presentationCapture{field: field, depth: len(stack) + 1, item: episode}
			}
			stack = append(stack, value.Name)
		case xml.CharData:
			if capture != nil {
				capture.builder.Write([]byte(value))
			}
		case xml.Comment:
			// Comments cannot contribute to displayed fields.
		case xml.Directive:
			// DTD/entity directives are not expanded by this page renderer.
		case xml.EndElement:
			depth := len(stack)
			if capture != nil && capture.depth == depth {
				assignPresentationText(&model, capture, strings.TrimSpace(capture.builder.String()))
				capture = nil
			}
			local := strings.ToLower(value.Name.Local)
			if (local == "item" || local == "entry") && episode != nil {
				if strings.TrimSpace(episode.Title) != "" {
					if episode.ArtworkURL == "" {
						episode.ArtworkURL = model.ArtworkURL
					}
					model.Episodes = append(model.Episodes, *episode)
				}
				episode = nil
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if model.Title == "" {
		return presentationFeed{}, fmt.Errorf("feed title is missing")
	}
	return model, nil
}

func presentationTextField(name xml.Name, inEpisode bool, parent string) string {
	local := strings.ToLower(name.Local)
	if inEpisode {
		switch local {
		case "title":
			return "episode-title"
		case "description", "summary":
			return "episode-description"
		case "encoded":
			if name.Space == "http://purl.org/rss/1.0/modules/content/" {
				return "episode-description"
			}
		case "pubdate", "published", "updated":
			return "episode-date"
		case "duration":
			return "episode-duration"
		}
		return ""
	}
	switch local {
	case "title":
		return "feed-title"
	case "description", "subtitle", "summary":
		return "feed-description"
	case "author":
		return "feed-author"
	case "name":
		if len(name.Space) > 0 {
			return "feed-author"
		}
	case "url":
		if parent == "image" {
			return "feed-artwork"
		}
	case "logo", "icon":
		if name.Space == atomNamespace {
			return "feed-artwork"
		}
	}
	return ""
}

func assignPresentationText(feed *presentationFeed, capture *presentationCapture, text string) {
	if capture.item != nil {
		switch capture.field {
		case "episode-title":
			capture.item.Title = text
		case "episode-description":
			if capture.item.Description == "" {
				capture.item.Description = presentationPlainText(text)
			}
		case "episode-date":
			if capture.item.PubDate == "" {
				capture.item.PubDate = text
			}
		case "episode-duration":
			capture.item.Duration = text
		}
		return
	}
	switch capture.field {
	case "feed-title":
		if feed.Title == "" {
			feed.Title = text
		}
	case "feed-description":
		if feed.Description == "" {
			feed.Description = presentationPlainText(text)
		}
	case "feed-author":
		if feed.Author == "" {
			feed.Author = text
		}
	case "feed-artwork":
		if feed.ArtworkURL == "" && isWebURL(text) {
			feed.ArtworkURL = text
		}
	}
}

func presentationPlainText(value string) string {
	value = html.UnescapeString(value)
	value = presentationScriptBlockRE.ReplaceAllString(value, " ")
	value = presentationStyleBlockRE.ReplaceAllString(value, " ")
	value = presentationHTMLTagRE.ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(value), " ")
}

func attribute(attrs []xml.Attr, name string) string {
	for _, attr := range attrs {
		if attr.Name.Space == "" && strings.EqualFold(attr.Name.Local, name) {
			return strings.TrimSpace(attr.Value)
		}
	}
	return ""
}

func isPlayableMIME(mimeType string) bool {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	return strings.HasPrefix(mimeType, "audio/") || strings.HasPrefix(mimeType, "video/")
}

func isWebURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

var podcastPageTemplate = template.Must(template.New("podcast").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="dark light">
<title>{{.Title}} · 播客</title>
<style>
:root{color-scheme:dark;--bg:#0c1220;--panel:#151e30;--line:#29364c;--text:#edf2fa;--muted:#aab8cb;--accent:#7db7ff;--accent2:#aa9bff}
*{box-sizing:border-box}body{margin:0;background:#0c1220;color:#edf2fa;font:16px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}a{color:#7db7ff;overflow-wrap:anywhere}button,input{font:inherit}.wrap{width:100%;max-width:1120px;margin:0 auto;padding:32px 20px 120px}.hero{display:flex;align-items:flex-start;margin:0 0 28px}.cover{width:180px;height:180px;object-fit:cover;border-radius:22px;background:#202d42;box-shadow:0 18px 50px #0006;margin-right:28px;width:clamp(120px,22vw,240px);aspect-ratio:1}.about{min-width:0;flex:1}.kicker{color:#7db7ff;font-size:.8rem;font-weight:700;letter-spacing:.12em;text-transform:uppercase}.title{font-size:2.2rem;line-height:1.12;margin:.25em 0;font-size:clamp(1.8rem,5vw,3.4rem)}.author,.muted{color:#aab8cb}.description{white-space:pre-wrap;overflow-wrap:anywhere;max-width:75ch}.subscribe{display:inline-block;background:#4e6fe0;color:#fff;text-decoration:none;border-radius:12px;padding:10px 16px;font-weight:700;margin-top:12px}.toolbar{display:flex;align-items:center;justify-content:space-between;background:#151e30;border:1px solid #29364c;border-radius:16px;padding:14px 18px;margin:24px 0}.toolbar label{display:flex;align-items:center}.toolbar input{max-width:320px;width:100%;padding:9px 12px;color:#edf2fa;background:#0c1423;border:1px solid #29364c;border-radius:10px}.episode{background:#151e30;border:1px solid #29364c;border-radius:18px;padding:22px;margin:14px 0}.episode-head{display:flex;align-items:flex-start}.episode-cover{width:64px;height:64px;flex:0 0 64px;object-fit:cover;border-radius:12px;background:#202d42;margin-right:16px}.episode-title{font-size:1.22rem;line-height:1.3;margin:0}.episode-meta{display:flex;flex-wrap:wrap;color:#aab8cb;font-size:.88rem;margin:8px 0 14px}.episode-meta span{margin-right:16px}.episode-desc{white-space:pre-wrap;overflow-wrap:anywhere;color:#d3dceb;margin:12px 0}.player{width:100%;margin-top:10px}.episode-link{display:inline-block;margin-top:8px}.empty{padding:26px;border:1px dashed #29364c;border-radius:16px;color:#aab8cb}
@media(max-width:650px){.wrap{padding:22px 14px 110px}.hero{display:block}.cover{width:150px;height:150px;margin:0 0 18px}.toolbar{display:block}.toolbar label{display:block;margin-top:8px}.episode{padding:16px}.episode-cover{width:52px;height:52px;flex-basis:52px}}
</style>
</head>
<body>
<main class="wrap">
<header class="hero">
{{if .ArtworkURL}}<img class="cover" src="{{.ArtworkURL}}" alt="{{.Title}} 封面">{{end}}
<div class="about"><div class="kicker">代理播客</div><h1 class="title">{{.Title}}</h1>{{if .Author}}<div class="author">{{.Author}}</div>{{end}}{{if .Description}}<p class="description">{{.Description}}</p>{{end}}<a class="subscribe" href="{{.FeedURL}}">复制/打开代理 RSS</a></div>
</header>
<div class="toolbar"><strong>共 {{len .Episodes}} 期节目</strong><label>筛选节目 <input id="filter" type="search" placeholder="输入标题或描述" aria-label="筛选节目"></label></div>
<section id="episodes" aria-label="播客节目">
{{range .Episodes}}<article class="episode" data-search="{{.Title}} {{.Description}}">
<div class="episode-head">{{if .ArtworkURL}}<img class="episode-cover" src="{{.ArtworkURL}}" alt="" loading="lazy">{{end}}<div><h2 class="episode-title">{{.Title}}</h2><div class="episode-meta">{{if .PubDate}}<span>{{.PubDate}}</span>{{end}}{{if .Duration}}<span>时长 {{.Duration}}</span>{{end}}</div></div></div>
{{if .Description}}<p class="episode-desc">{{.Description}}</p>{{end}}
{{if .AudioURL}}{{if .IsVideo}}<video class="player" controls preload="none"><source src="{{.AudioURL}}" type="{{.AudioType}}"></video>{{else}}<audio class="player" controls preload="none"><source src="{{.AudioURL}}" type="{{.AudioType}}"></audio>{{end}}<p class="episode-link"><a href="{{.AudioURL}}">如果播放器不可用，点击打开媒体文件</a></p>{{else}}<p class="muted">RSS 未提供 PinePods 可识别的音频 enclosure。</p>{{end}}
</article>{{else}}<p class="empty">Feed 暂无可显示的节目。</p>{{end}}
</section>
</main>
<script>
(function(){
 var filter=document.getElementById('filter');
 var episodes=document.getElementById('episodes');
 function listen(target,name,handler){if(target.addEventListener){target.addEventListener(name,handler,false);}else if(target.attachEvent){target.attachEvent('on'+name,handler);}}
 if(filter&&episodes){listen(filter,'input',function(){var query=filter.value.toLowerCase();var cards=episodes.getElementsByClassName?episodes.getElementsByClassName('episode'):episodes.getElementsByTagName('article');for(var i=0;i<cards.length;i++){var text=(cards[i].getAttribute('data-search')||'').toLowerCase();cards[i].style.display=text.indexOf(query)>=0?'':'none';}});}
})();
</script>
</body>
</html>`))

func renderPodcastPage(feedXML, subscriptionURL string) ([]byte, error) {
	model, err := parsePresentationFeed(feedXML, subscriptionURL)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := podcastPageTemplate.Execute(&output, model); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
