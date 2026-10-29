package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"net/url"
	"regexp"
	"strings"
)

const (
	itunesNamespace   = "http://www.itunes.com/dtds/podcast-1.0.dtd"
	podcastNamespace  = "https://podcastindex.org/namespace/1.0"
	atomNamespace     = "http://www.w3.org/2005/Atom"
	mediaRSSNamespace = "http://search.yahoo.com/mrss/"
)

var (
	xmlDeclarationRE = regexp.MustCompile(`(?is)^\s*<\?xml\b[^?]*\?>`)
	encodingAttrRE   = regexp.MustCompile(`(?i)\bencoding\s*=\s*(['"])([^'"]+)(['"])`)
)

// FeedTransformer rewrites media URLs in RSS 2.0 and Atom while preserving the
// original XML byte-for-byte except for selected URL values and the browser PI.
type FeedTransformer struct{}

func NewFeedTransformer() *FeedTransformer { return &FeedTransformer{} }

type xmlReplacement struct {
	start int
	end   int
	value string
}

type feedElementFrame struct {
	Name xml.Name
	Base string
}

type feedRewriteResult struct {
	Content string
}

func (ft *FeedTransformer) Transform(content string, builder *ProxyURLBuilder, apikey string) string {
	return ft.TransformWithoutStylesheet(content, builder, apikey, false)
}

// TransformWithoutStylesheet supports a presentation-free feed for API clients.
func (ft *FeedTransformer) TransformWithoutStylesheet(content string, builder *ProxyURLBuilder, apikey string, stylesheet bool) string {
	transformed, err := ft.TransformStrict(content, builder, apikey, stylesheet)
	if err != nil {
		return content
	}
	return transformed
}

func (ft *FeedTransformer) TransformStrict(content string, builder *ProxyURLBuilder, apikey string, stylesheet bool, feedCredentials ...string) (string, error) {
	result, err := ft.TransformWithRoutes(content, builder, apikey, stylesheet, feedCredentials...)
	return result.Content, err
}

func (ft *FeedTransformer) TransformWithRoutes(content string, builder *ProxyURLBuilder, apikey string, stylesheet bool, feedCredentials ...string) (feedRewriteResult, error) {
	if builder == nil {
		return feedRewriteResult{}, fmt.Errorf("proxy URL builder is required")
	}
	replacements, err := collectFeedURLReplacements(content, builder, apikey, feedCredentials...)
	if err != nil {
		return feedRewriteResult{}, err
	}
	transformed, err := applyXMLReplacements(content, replacements)
	if err != nil {
		return feedRewriteResult{}, err
	}
	transformed, err = stripFeedStylesheets(transformed)
	if err != nil {
		return feedRewriteResult{}, err
	}
	// Keep the parameter for source compatibility with callers compiled against
	// the previous API. Presentation is now native HTML served by /feed, never an
	// XSLT processing instruction in subscribed RSS/Atom documents.
	_ = stylesheet
	return feedRewriteResult{Content: transformed}, nil
}

func normalizeFeedXML(content string) (string, error) {
	label := ""
	if strings.HasPrefix(content, "\xff\xfe") {
		label = "utf-16le"
	} else if strings.HasPrefix(content, "\xfe\xff") {
		label = "utf-16be"
	} else if strings.HasPrefix(content, "<\x00?\x00x\x00m\x00l\x00") {
		label = "utf-16le"
	} else if strings.HasPrefix(content, "\x00<\x00?\x00x\x00m\x00l") {
		label = "utf-16be"
	}
	if label == "" {
		declaration := xmlDeclarationRE.FindString(content)
		if declaration == "" {
			return content, nil
		}
		match := encodingAttrRE.FindStringSubmatch(declaration)
		if len(match) != 4 || match[1] != match[3] {
			return content, nil
		}
		label = strings.TrimSpace(match[2])
	}
	if strings.EqualFold(label, "utf-8") || strings.EqualFold(label, "utf8") || strings.EqualFold(label, "us-ascii") {
		return content, nil
	}
	reader, err := newCharsetReader(label, strings.NewReader(content))
	if err != nil {
		return "", err
	}
	const maxNormalizedFeedSize = 64 << 20
	decoded, err := io.ReadAll(io.LimitReader(reader, maxNormalizedFeedSize+1))
	if err != nil {
		return "", err
	}
	if len(decoded) > maxNormalizedFeedSize {
		return "", fmt.Errorf("normalized feed exceeds size limit")
	}
	normalized := string(decoded)
	declaration := xmlDeclarationRE.FindString(normalized)
	if declaration != "" {
		match := encodingAttrRE.FindStringSubmatchIndex(declaration)
		if len(match) == 8 {
			start, end := match[4], match[5]
			declaration = declaration[:start] + "UTF-8" + declaration[end:]
			normalized = strings.Replace(normalized, xmlDeclarationRE.FindString(string(decoded)), declaration, 1)
		}
	}
	return normalized, nil
}

func collectFeedURLReplacements(content string, builder *ProxyURLBuilder, apikey string, feedCredentials ...string) ([]xmlReplacement, error) {
	decoder := newXMLDecoder(content)
	decoder.Strict = true
	var replacements []xmlReplacement
	var stack []feedElementFrame
	for {
		tokenStart := decoder.InputOffset()
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			baseURL := builder.sourceURL
			if len(stack) > 0 {
				baseURL = stack[len(stack)-1].Base
			}
			for _, attr := range value.Attr {
				if attr.Name.Space == "http://www.w3.org/XML/1998/namespace" && attr.Name.Local == "base" {
					if resolved, err := resolveFeedURL(baseURL, attr.Value); err == nil {
						baseURL = resolved
					}
				}
			}
			elementBuilder := *builder
			elementBuilder.sourceURL = baseURL
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1].Name.Local
			}
			tokenEnd := decoder.InputOffset()
			elementName := qualifiedElementName(content, tokenEnd)
			mimeType, hasInferredType := inferredEnclosureType(value.Name, parent, stack, value.Attr)
			hasTypeAttribute := false
			for _, attr := range value.Attr {
				if attr.Name.Space == "" && attr.Name.Local == "type" {
					hasTypeAttribute = true
					currentType := strings.ToLower(strings.TrimSpace(attr.Value))
					if hasInferredType && !strings.HasPrefix(currentType, "audio/") && !strings.HasPrefix(currentType, "video/") {
						replacement, ok := locateAttributeReplacement(
							content,
							tokenEnd,
							elementName,
							attr.Name.Local,
							attr.Value,
							mimeType,
						)
						if ok {
							replacements = append(replacements, replacement)
						}
					}
				}
				resource, ok := feedAttributeResource(stack, parent, value.Name, value.Attr, attr)
				if !ok {
					continue
				}
				proxyURL, err := buildFeedProxyURL(&elementBuilder, apikey, resource, attr.Value, feedCredentials...)
				if err != nil {
					return nil, fmt.Errorf("invalid media URL in feed")
				}
				if resource == ResourceAudio || resource == ResourceImage || resource == ResourceStyle {
					if token := routeTokenFromProxyURL(proxyURL); token != "" {
						if currentURL, err := resolveFeedURL(elementBuilder.sourceURL, attr.Value); err == nil {
							UpdateResourceRouteURL(resource, token, currentURL)
						}
					}
				}
				if resource == ResourceAudio || resource == ResourceImage || resource == ResourceStyle {
					proxyURL = registerCredentialsOnMediaURL(proxyURL, feedCredentials...)
				}
				replacement, ok := locateAttributeReplacement(content, tokenEnd, elementName, attr.Name.Local, attr.Value, proxyURL)
				if !ok {
					return nil, fmt.Errorf("unable to safely locate XML media attribute")
				}
				replacements = append(replacements, replacement)
			}
			if hasInferredType && !hasTypeAttribute {
				if insertion, ok := locateAttributeInsertion(content, tokenEnd, mimeType); ok {
					replacements = append(replacements, insertion)
				}
			}
			stack = append(stack, feedElementFrame{Name: value.Name, Base: baseURL})
		case xml.CharData:
			resource, ok := feedTextResource(stack)
			if !ok {
				continue
			}
			start, end := int(tokenStart), int(decoder.InputOffset())
			if start < 0 || end > len(content) || start >= end {
				continue
			}
			rawText := content[start:end]
			decoded, err := xmlUnescapeText(rawText)
			if err != nil {
				return nil, fmt.Errorf("invalid text URL in feed")
			}
			if strings.TrimSpace(decoded) == "" {
				continue
			}
			elementBuilder := *builder
			elementBuilder.sourceURL = stack[len(stack)-1].Base
			proxyURL, err := buildFeedProxyURL(&elementBuilder, apikey, resource, strings.TrimSpace(decoded), feedCredentials...)
			if err != nil {
				return nil, fmt.Errorf("invalid media URL in feed")
			}
			if resource == ResourceImage {
				if token := routeTokenFromProxyURL(proxyURL); token != "" {
					if currentURL, err := resolveFeedURL(elementBuilder.sourceURL, strings.TrimSpace(decoded)); err == nil {
						UpdateResourceRouteURL(resource, token, currentURL)
					}
				}
			}
			if resource == ResourceImage {
				proxyURL = registerCredentialsOnMediaURL(proxyURL, feedCredentials...)
			}
			left := len(rawText) - len(strings.TrimLeftFunc(rawText, isXMLSpaceRune))
			right := len(strings.TrimRightFunc(rawText, isXMLSpaceRune))
			replacements = append(replacements, xmlReplacement{
				start: start + left,
				end:   start + right,
				value: xmlEscape(proxyURL),
			})
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return replacements, nil
}

func feedAttributeResource(stack []feedElementFrame, parent string, element xml.Name, attrs []xml.Attr, attr xml.Attr) (ProxyResource, bool) {
	if attr.Name.Space != "" {
		return "", false
	}
	local := element.Local
	namespace := element.Space
	attrName := attr.Name.Local
	switch {
	case local == "enclosure" && attrName == "url":
		return ResourceAudio, true
	case local == "link" && attrName == "href" && elementNamespaceMatches(namespace, atomNamespace) && !inElementStack(stack, "entry"):
		for _, item := range attrs {
			if item.Name.Space == "" && item.Name.Local == "rel" && strings.EqualFold(strings.TrimSpace(item.Value), "self") {
				return ResourceFeed, true
			}
		}
	case local == "link" && attrName == "href" && elementNamespaceMatches(namespace, atomNamespace) && inElementStack(stack, "entry"):
		rel, mediaType := "", ""
		for _, item := range attrs {
			if item.Name.Space == "" && item.Name.Local == "rel" {
				rel = item.Value
			}
			if item.Name.Space == "" && item.Name.Local == "type" {
				mediaType = item.Value
			}
		}
		rel = strings.ToLower(strings.TrimSpace(rel))
		mediaType = strings.ToLower(strings.TrimSpace(mediaType))
		if rel == "enclosure" {
			if strings.HasPrefix(mediaType, "audio/") || strings.HasPrefix(mediaType, "video/") {
				return ResourceAudio, true
			}
			if inferredType, ok := inferredEnclosureType(element, parent, stack, attrs); ok {
				return mediaContentResource(inferredType)
			}
		}
	case local == "content" && attrName == "url" && elementNamespaceMatches(namespace, mediaRSSNamespace):
		for _, item := range attrs {
			if item.Name.Space == "" && item.Name.Local == "type" {
				return mediaContentResource(item.Value)
			}
		}
	case local == "thumbnail" && attrName == "url" && elementNamespaceMatches(namespace, mediaRSSNamespace):
		return ResourceImage, true
	case local == "image" && attrName == "href" && elementNamespaceMatches(namespace, itunesNamespace):
		return ResourceImage, true
	case local == "chapters" && attrName == "url" && elementNamespaceMatches(namespace, podcastNamespace):
		return ResourceStyle, true
	case local == "transcript" && attrName == "url" && elementNamespaceMatches(namespace, podcastNamespace):
		return ResourceStyle, true
	case local == "person" && attrName == "img" && elementNamespaceMatches(namespace, podcastNamespace):
		return ResourceImage, true
	case local == "url" && parent == "image" && attrName == "":
		return "", false // Text content is rewritten separately.
	}
	return "", false
}

func feedTextResource(stack []feedElementFrame) (ProxyResource, bool) {
	if len(stack) == 0 {
		return "", false
	}
	last := stack[len(stack)-1]
	if last.Name.Local == "url" && len(stack) >= 2 && stack[len(stack)-2].Name.Local == "image" {
		return ResourceImage, true
	}
	if (last.Name.Local == "logo" || last.Name.Local == "icon") && elementNamespaceMatches(last.Name.Space, atomNamespace) {
		return ResourceImage, true
	}
	if last.Name.Local == "new-feed-url" && elementNamespaceMatches(last.Name.Space, itunesNamespace) {
		return ResourceFeed, true
	}
	return "", false
}

func elementNamespaceMatches(space, namespace string) bool {
	return space == namespace
}

func inElementStack(stack []feedElementFrame, name string) bool {
	for _, element := range stack {
		if element.Name.Local == name {
			return true
		}
	}
	return false
}

func mediaContentResource(mimeType string) (ProxyResource, bool) {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if strings.HasPrefix(mimeType, "audio/") || strings.HasPrefix(mimeType, "video/") {
		return ResourceAudio, true
	}
	if strings.HasPrefix(mimeType, "image/") {
		return ResourceImage, true
	}
	return "", false
}

func inferredEnclosureType(element xml.Name, parent string, stack []feedElementFrame, attrs []xml.Attr) (string, bool) {
	resourceURL := ""
	rel := ""
	typeValue := ""
	for _, attr := range attrs {
		if attr.Name.Space != "" {
			continue
		}
		switch attr.Name.Local {
		case "url":
			if element.Local == "enclosure" {
				resourceURL = attr.Value
			}
		case "href":
			if element.Local == "link" {
				resourceURL = attr.Value
			}
		case "rel":
			rel = strings.ToLower(strings.TrimSpace(attr.Value))
		case "type":
			typeValue = strings.TrimSpace(attr.Value)
		}
	}
	typeValue = strings.ToLower(strings.TrimSpace(typeValue))
	if resourceURL == "" || strings.HasPrefix(typeValue, "audio/") || strings.HasPrefix(typeValue, "video/") {
		return "", false
	}
	rssEnclosure := element.Local == "enclosure" && len(stack) > 0
	atomEnclosure := element.Local == "link" && element.Space == atomNamespace && parent == "entry" && rel == "enclosure"
	if !rssEnclosure && !atomEnclosure {
		return "", false
	}
	parsed, err := url.Parse(resourceURL)
	if err != nil {
		return "", false
	}
	mediaType := mime.TypeByExtension(strings.ToLower(feedPathExtension(parsed.Path)))
	if mediaType == "" {
		mediaType = "audio/mpeg"
	}
	mediaType, _, _ = strings.Cut(mediaType, ";")
	mediaType = strings.TrimSpace(mediaType)
	resource, ok := mediaContentResource(mediaType)
	return mediaType, ok && resource == ResourceAudio
}

func feedPathExtension(path string) string {
	if slash := strings.LastIndexByte(path, '/'); slash >= 0 {
		path = path[slash+1:]
	}
	if index := strings.LastIndexByte(path, '.'); index >= 0 {
		return path[index:]
	}
	return ""
}

func buildFeedProxyURL(builder *ProxyURLBuilder, apikey string, resource ProxyResource, raw string, credentials ...string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("empty resource URL")
	}
	absolute, err := resolveFeedURL(builder.sourceURL, raw)
	if err != nil {
		return "", err
	}
	switch resource {
	case ResourceAudio:
		return builder.BuildAudioURL(apikey, absolute, credentials...), nil
	case ResourceImage:
		return builder.BuildImageURL(apikey, absolute, credentials...), nil
	case ResourceStyle:
		return builder.BuildStyleURL(apikey, absolute, credentials...), nil
	case ResourceFeed:
		return builder.BuildFeedURLFor(apikey, absolute), nil
	default:
		return "", fmt.Errorf("unsupported resource type %q", resource)
	}
}

func routeTokenFromProxyURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

func resolveFeedURL(base, raw string) (string, error) {
	relative, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if relative.User != nil {
		return "", fmt.Errorf("resource URL must not contain embedded credentials")
	}
	if base == "" {
		if !relative.IsAbs() || (relative.Scheme != "http" && relative.Scheme != "https") || relative.Host == "" {
			return "", fmt.Errorf("resource URL must be an absolute HTTP(S) URL")
		}
		return relative.String(), nil
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	resolved := baseURL.ResolveReference(relative)
	if (resolved.Scheme != "http" && resolved.Scheme != "https") || resolved.Host == "" || resolved.User != nil {
		return "", fmt.Errorf("resource URL did not resolve to HTTP(S)")
	}
	return resolved.String(), nil
}

func locateAttributeReplacement(content string, tokenEnd int64, elementName, targetName, targetValue, replacement string) (xmlReplacement, bool) {
	start, end, ok := startTagRange(content, tokenEnd)
	if !ok {
		return xmlReplacement{}, false
	}
	pos := start + 1
	for pos < end && !isXMLSpace(content[pos]) && content[pos] != '/' && content[pos] != '>' {
		pos++
	}
	if content[start+1:pos] != elementName {
		return xmlReplacement{}, false
	}
	for pos < end {
		for pos < end && (isXMLSpace(content[pos]) || content[pos] == '/') {
			pos++
		}
		if pos >= end || content[pos] == '>' {
			break
		}
		nameStart := pos
		for pos < end && !isXMLSpace(content[pos]) && content[pos] != '=' && content[pos] != '/' && content[pos] != '>' {
			pos++
		}
		if nameStart == pos {
			pos++
			continue
		}
		qualifiedName := content[nameStart:pos]
		for pos < end && isXMLSpace(content[pos]) {
			pos++
		}
		if pos >= end || content[pos] != '=' {
			continue
		}
		pos++
		for pos < end && isXMLSpace(content[pos]) {
			pos++
		}
		if pos >= end || (content[pos] != '\'' && content[pos] != '"') {
			return xmlReplacement{}, false
		}
		quote := content[pos]
		valueStart := pos + 1
		valueEnd := valueStart
		for valueEnd < end && content[valueEnd] != quote {
			valueEnd++
		}
		if valueEnd >= end {
			return xmlReplacement{}, false
		}
		if qualifiedName == targetName {
			decoded, err := xmlUnescapeAttribute(content[valueStart:valueEnd])
			if err == nil && decoded == targetValue {
				return xmlReplacement{start: valueStart, end: valueEnd, value: xmlEscape(replacement)}, true
			}
		}
		pos = valueEnd + 1
	}
	return xmlReplacement{}, false
}

func locateAttributeInsertion(content string, tokenEnd int64, mimeType string) (xmlReplacement, bool) {
	start, end, ok := startTagRange(content, tokenEnd)
	if !ok || end <= start+1 {
		return xmlReplacement{}, false
	}
	insertAt := end - 1
	for insertAt > start && isXMLSpace(content[insertAt-1]) {
		insertAt--
	}
	if insertAt > start && content[insertAt-1] == '/' {
		insertAt--
	}
	return xmlReplacement{start: insertAt, end: insertAt, value: ` type="` + xmlEscape(mimeType) + `"`}, true
}

func qualifiedElementName(content string, tokenEnd int64) string {
	start, end, ok := startTagRange(content, tokenEnd)
	if !ok || start+1 >= end {
		return ""
	}
	pos := start + 1
	for pos < end && !isXMLSpace(content[pos]) && content[pos] != '/' && content[pos] != '>' {
		pos++
	}
	return content[start+1 : pos]
}

func startTagRange(content string, tokenEnd int64) (int, int, bool) {
	end := int(tokenEnd)
	if end > len(content) || end < 0 {
		return 0, 0, false
	}
	start := strings.LastIndex(content[:end], "<")
	if start < 0 {
		return 0, 0, false
	}
	quote := byte(0)
	for i := start + 1; i < end; i++ {
		c := content[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
		} else if c == '>' {
			return start, i + 1, true
		}
	}
	return 0, 0, false
}

func xmlUnescapeAttribute(raw string) (string, error) {
	decoder := newXMLDecoder("<value attr=\"" + strings.ReplaceAll(raw, "\"", "&quot;") + "\"/>")
	var result struct {
		Value string `xml:"attr,attr"`
	}
	if err := decoder.Decode(&result); err != nil {
		return "", err
	}
	return result.Value, nil
}

func xmlUnescapeText(raw string) (string, error) {
	decoder := newXMLDecoder("<value>" + raw + "</value>")
	var result struct {
		Value string `xml:",chardata"`
	}
	if err := decoder.Decode(&result); err != nil {
		return "", err
	}
	return result.Value, nil
}

func isXMLSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

func isXMLSpaceRune(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n'
}

func applyXMLReplacements(content string, replacements []xmlReplacement) (string, error) {
	if len(replacements) == 0 {
		return content, nil
	}
	for i := 1; i < len(replacements); i++ {
		if replacements[i-1].end > replacements[i].start {
			return "", fmt.Errorf("overlapping XML replacements")
		}
	}
	var out strings.Builder
	out.Grow(len(content) + len(replacements)*64)
	last := 0
	for _, replacement := range replacements {
		if replacement.start < last || replacement.end > len(content) {
			return "", fmt.Errorf("invalid XML replacement range")
		}
		out.WriteString(content[last:replacement.start])
		out.WriteString(replacement.value)
		last = replacement.end
	}
	out.WriteString(content[last:])
	return out.String(), nil
}

func stripFeedStylesheets(content string) (string, error) {
	decoder := newXMLDecoder(content)
	decoder.Strict = true
	var ranges [][2]int
	for {
		before := decoder.InputOffset()
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		instruction, ok := token.(xml.ProcInst)
		if !ok || !strings.EqualFold(instruction.Target, "xml-stylesheet") {
			continue
		}
		start, end := int(before), int(decoder.InputOffset())
		if start < 0 || end < start || end > len(content) {
			return "", fmt.Errorf("invalid stylesheet processing instruction range")
		}
		if start == 0 && strings.HasPrefix(content, "\ufeff") {
			return "", fmt.Errorf("stylesheet processing instruction precedes XML BOM")
		}
		ranges = append(ranges, [2]int{start, end})
	}
	for i := len(ranges) - 1; i >= 0; i-- {
		content = content[:ranges[i][0]] + content[ranges[i][1]:]
	}
	return content, nil
}

// FeedValidator validates HTTP(S) feed URLs and well-formed RSS/Atom XML.
type FeedValidator struct{}

func (*FeedValidator) ValidateFeedURL(feedURL string) bool {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(feedURL))
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil
}

func (*FeedValidator) IsFeedContent(content string) bool {
	decoder := newXMLDecoder(content)
	decoder.Strict = true
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return false
		}
		if err != nil {
			return false
		}
		if root, ok := token.(xml.StartElement); ok {
			return root.Name.Local == "rss" || root.Name.Local == "feed" || root.Name.Local == "RDF"
		}
	}
}

func (*FeedValidator) ValidateTransformedFeed(content string) error {
	decoder := newXMLDecoder(content)
	decoder.Strict = true
	for {
		if _, err := decoder.Token(); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func newXMLDecoder(content string) *xml.Decoder {
	decoder := xml.NewDecoder(strings.NewReader(content))
	decoder.CharsetReader = newCharsetReader
	return decoder
}
