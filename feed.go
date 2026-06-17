package main

import (
	"encoding/xml"
	"regexp"
	"strings"
)

// FeedTransformer RSS源转换器
type FeedTransformer struct {
	regexes map[string]*regexp.Regexp
}

// NewFeedTransformer 创建RSS转换器
func NewFeedTransformer() *FeedTransformer {
	return &FeedTransformer{
		regexes: map[string]*regexp.Regexp{
			// enclosure标签：<enclosure url="..." />
			"enclosure": regexp.MustCompile(`(<enclosure\s+[^>]*?url=")([^"]+)`),
			// iTunes图片：<itunes:image href="..." />
			"itunes_image": regexp.MustCompile(`(<itunes:image\s+[^>]*?href=")([^"]+)`),
			// 标准图片标签：<image><url>...</url></image>
			"image_url": regexp.MustCompile(`(<image>[\s\S]*?<url>)([^<]+)(<\/url>)`),
			// 媒体缩略图：<media:thumbnail url="..." />
			"media_thumbnail": regexp.MustCompile(`(<media:thumbnail\s+[^>]*?url=")([^"]+)`),
			// 媒体内容：<media:content url="..." />
			"media_content": regexp.MustCompile(`<media:content\s+[^>]*?url="[^"]+"[^>]*>`),
			// XML样式表：<?xml-stylesheet href="..." ?>
			"stylesheet": regexp.MustCompile(`(<\?xml-stylesheet[^>]*href=")([^"]+)(")>`),
		},
	}
}

// Transform 转换RSS源
func (ft *FeedTransformer) Transform(content string, builder *ProxyURLBuilder, apikey string) string {
	// 1. 替换enclosure（音频）
	content = ft.replaceWithRegex(content, "enclosure", builder, apikey, ResourceAudio)

	// 2. 替换iTunes图片
	content = ft.replaceWithRegex(content, "itunes_image", builder, apikey, ResourceImage)

	// 3. 替换image标签中的URL
	content = ft.replaceImageURL(content, builder, apikey)

	// 4. 替换media:thumbnail
	content = ft.replaceWithRegex(content, "media_thumbnail", builder, apikey, ResourceImage)

	// 5. 替换media:content
	content = ft.replaceMediaContent(content, builder, apikey)

	// 6. 替换xml-stylesheet
	content = ft.replaceStylesheet(content, builder, apikey)

	// 7. 强行应用统一的本地美化 XSL 样式模板
	content = ft.applyCustomStylesheet(content)

	return content
}

// applyCustomStylesheet 强行应用我们的播客美化模板样式
func (ft *FeedTransformer) applyCustomStylesheet(content string) string {
	// 1. 移除或替换现有的所有 xml-stylesheet 指令
	reStylesheet := regexp.MustCompile(`(?i)<\?xml-stylesheet\s+[^>]*\?>`)
	
	if reStylesheet.MatchString(content) {
		// 如果已存在，直接替换为我们自己的
		content = reStylesheet.ReplaceAllString(content, `<?xml-stylesheet type="text/xsl" href="/podcast.xsl"?>`)
		return content
	}

	// 2. 如果不存在，我们需要在 <?xml ... ?> 声明后面插入
	reXMLDecl := regexp.MustCompile(`(?i)^\s*<\?xml\s+[^>]*\?>`)
	loc := reXMLDecl.FindStringIndex(content)
	if loc != nil {
		endIdx := loc[1]
		content = content[:endIdx] + "\n<?xml-stylesheet type=\"text/xsl\" href=\"/podcast.xsl\"?>\n" + content[endIdx:]
	} else {
		// 如果连 <?xml ... ?> 都没有，那就直接在最开头插入
		content = "<?xml-stylesheet type=\"text/xsl\" href=\"/podcast.xsl\"?>\n" + content
	}

	return content
}

// replaceWithRegex 使用正则表达式替换URL
func (ft *FeedTransformer) replaceWithRegex(content string, regexName string, 
	builder *ProxyURLBuilder, apikey string, resourceType ProxyResource) string {
	
	regex := ft.regexes[regexName]
	if regex == nil {
		return content
	}

	return regex.ReplaceAllStringFunc(content, func(match string) string {
		parts := regex.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}

		prefix := parts[1]
		originalURL := parts[2]

		var proxyURL string
		switch resourceType {
		case ResourceAudio:
			proxyURL = builder.BuildAudioURL(apikey, originalURL)
		case ResourceImage:
			proxyURL = builder.BuildImageURL(apikey, originalURL)
		case ResourceStyle:
			proxyURL = builder.BuildStyleURL(apikey, originalURL)
		default:
			proxyURL = originalURL
		}

		return prefix + proxyURL
	})
}

// replaceImageURL 特殊处理<image><url>格式
func (ft *FeedTransformer) replaceImageURL(content string, builder *ProxyURLBuilder, apikey string) string {
	regex := ft.regexes["image_url"]
	
	return regex.ReplaceAllStringFunc(content, func(match string) string {
		parts := regex.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}

		prefix := parts[1]
		originalURL := strings.TrimSpace(parts[2])
		suffix := parts[3]

		proxyURL := builder.BuildImageURL(apikey, originalURL)
		return prefix + proxyURL + suffix
	})
}

// replaceStylesheet 替换xml-stylesheet
func (ft *FeedTransformer) replaceStylesheet(content string, builder *ProxyURLBuilder, apikey string) string {
	regex := ft.regexes["stylesheet"]
	
	return regex.ReplaceAllStringFunc(content, func(match string) string {
		parts := regex.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}

		prefix := parts[1]
		originalURL := parts[2]
		suffix := parts[3]

		proxyURL := builder.BuildStyleURL(apikey, originalURL)
		return prefix + proxyURL + suffix
	})
}

// replaceMediaContent 特殊处理<media:content>（需判断音频/图片）
func (ft *FeedTransformer) replaceMediaContent(content string, builder *ProxyURLBuilder, apikey string) string {
	regex := ft.regexes["media_content"]
	sh := &StringHelper{}

	return regex.ReplaceAllStringFunc(content, func(match string) string {
		// 判断类型
		var proxyFunc func(string, string) string

		if strings.Contains(match, `type="audio/`) {
			proxyFunc = builder.BuildAudioURL
		} else if strings.Contains(match, `type="image/`) {
			proxyFunc = builder.BuildImageURL
		} else {
			// 根据URL后缀判断
			urlRegex := regexp.MustCompile(`url="([^"]+)"`)
			urlMatch := urlRegex.FindStringSubmatch(match)
			if len(urlMatch) > 1 {
				url := urlMatch[1]
				if sh.IsImageURL(url) {
					proxyFunc = builder.BuildImageURL
				} else {
					proxyFunc = builder.BuildAudioURL
				}
			} else {
				return match
			}
		}

		// 替换url属性值
		urlAttrRegex := regexp.MustCompile(`(url=")([^"]+)`)
		return urlAttrRegex.ReplaceAllStringFunc(match, func(attrMatch string) string {
			parts := urlAttrRegex.FindStringSubmatch(attrMatch)
			if len(parts) < 3 {
				return attrMatch
			}
			return parts[1] + proxyFunc(apikey, parts[2])
		})
	})
}

// FeedValidator RSS源验证器
type FeedValidator struct{}

// ValidateFeedURL 验证RSS源URL
func (*FeedValidator) ValidateFeedURL(feedURL string) bool {
	if feedURL == "" {
		return false
	}
	
	// 基本URL格式检查
	if !strings.HasPrefix(feedURL, "http://") && !strings.HasPrefix(feedURL, "https://") {
		return false
	}
	
	return true
}

// IsFeedContent 判断是否是有效的RSS/Atom内容
func (*FeedValidator) IsFeedContent(content string) bool {
	lower := strings.ToLower(content)
	
	// 检查是否包含RSS或Atom标记
	return strings.Contains(lower, "<rss") || 
	       strings.Contains(lower, "<feed") ||
	       strings.Contains(lower, "<?xml")
}

// ValidateTransformedFeed 验证转换后的RSS是否是有效的XML
// 这确保了转换不会破坏RSS的XML结构
func (*FeedValidator) ValidateTransformedFeed(content string) error {
	// 尝试将内容解析为XML
	decoder := xml.NewDecoder(strings.NewReader(content))
	decoder.Strict = false // 允许某些非标准的XML
	
	for {
		t, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				return nil // 解析成功
			}
			return err
		}
		if t == nil {
			break
		}
	}
	return nil
}


