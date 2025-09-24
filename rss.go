// rss.go
package main

import "encoding/xml"

// RSS 是整个 feed 的根结构
type RSS struct {
        XMLName xml.Name `xml:"rss"`
        Channel Channel  `xml:"channel"`
}

// Channel 包含 feed 的元数据和条目列表
type Channel struct {
        Title       string      `xml:"title"`
        Link        string      `xml:"link"`
        Description string      `xml:"description"`
        Image       Image       `xml:"image"`
        Items       []Item      `xml:"item"`
        ITunesImage ItunesImage `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd image"`
}

// Item 代表一个单独的播客单集
type Item struct {
        Title       string      `xml:"title"`
        Link        string      `xml:"link"`
        Description string      `xml:"description"`
        GUID        string      `xml:"guid"`
        PubDate     string      `xml:"pubDate"`
        Enclosure   Enclosure   `xml:"enclosure"`
        ITunesImage ItunesImage `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd image"`
}

// Image 代表一个图片链接
type Image struct {
        URL   string `xml:"url"`
        Title string `xml:"title"`
        Link  string `xml:"link"`
}

// ItunesImage 是 iTunes 命名空间下的图片标签
type ItunesImage struct {
        Href string `xml:"href,attr"`
}

// Enclosure 代表媒体文件，如音频
type Enclosure struct {
        URL    string `xml:"url,attr"`
        Length string `xml:"length,attr"`
        Type   string `xml:"type,attr"`
}
