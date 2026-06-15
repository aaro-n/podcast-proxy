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
			// 链接标签（atom）
			"atom_link": regexp.MustCompile(`(<link\s+[^>]*?href=")([^"]+)`),
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

	// 7. 替换atom:link中的相对URL（可选，默认保持原始）
	// 这样可以让客户端直接访问原始源的分页等功能

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

// FeedMetadata RSS元数据提取器
type FeedMetadata struct{}

// ExtractTitle 提取RSS标题
func (*FeedMetadata) ExtractTitle(content string) string {
	regex := regexp.MustCompile(`<title>([^<]+)</title>`)
	matches := regex.FindStringSubmatch(content)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// ExtractDescription 提取RSS描述
func (*FeedMetadata) ExtractDescription(content string) string {
	regex := regexp.MustCompile(`<description>([^<]+)</description>`)
	matches := regex.FindStringSubmatch(content)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// ExtractLastBuildDate 提取RSS最后构建时间
func (*FeedMetadata) ExtractLastBuildDate(content string) string {
	regex := regexp.MustCompile(`<lastBuildDate>([^<]+)</lastBuildDate>`)
	matches := regex.FindStringSubmatch(content)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}
