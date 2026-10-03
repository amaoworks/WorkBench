package news

import (
	"bytes"
	"encoding/xml"
	"errors"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type feedItem struct {
	GUID, Title, URL, Content, Language, Publisher string
	PublishedAt                                    time.Time
}
type xmlText struct {
	Text  string `xml:",chardata"`
	Inner string `xml:",innerxml"`
}

func (v xmlText) value() string {
	if strings.TrimSpace(v.Text) != "" {
		return v.Text
	}
	return v.Inner
}

type rssItem struct {
	GUID        string `xml:"guid"`
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Encoded     string `xml:"encoded"`
	Date        string `xml:"date"`
	PubDate     string `xml:"pubDate"`
	Source      string `xml:"source"`
}
type atomEntry struct {
	ID        string  `xml:"id"`
	Title     xmlText `xml:"title"`
	Summary   xmlText `xml:"summary"`
	Content   xmlText `xml:"content"`
	Published string  `xml:"published"`
	Updated   string  `xml:"updated"`
	Links     []struct {
		Href string `xml:"href,attr"`
		Rel  string `xml:"rel,attr"`
	} `xml:"link"`
}

var hiddenHTML = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(?:script|style)\s*>`)
var tagsHTML = regexp.MustCompile(`(?s)<[^>]*>`)

func plainText(raw string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagsHTML.ReplaceAllString(hiddenHTML.ReplaceAllString(raw, " "), " "))), " ")
}
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
func feedDate(raw string, now time.Time) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, time.RFC850, "Mon, 2 Jan 2006 15:04:05 -0700", "2006-01-02T15:04:05-0700", "2006-01-02"} {
		if v, err := time.Parse(layout, strings.TrimSpace(raw)); err == nil {
			if v.After(now.Add(10 * time.Minute)) {
				return now
			}
			return v.UTC()
		}
	}
	return now
}
func resolveLink(base, link string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || strings.TrimSpace(link) == "" {
		return ""
	}
	return canonicalURL(b.ResolveReference(u).String())
}
func parseFeed(raw []byte, base string, now time.Time) ([]feedItem, error) {
	var doc struct {
		XMLName  xml.Name
		Language string `xml:"lang,attr"`
		Channel  struct {
			Language string    `xml:"language"`
			Items    []rssItem `xml:"item"`
		} `xml:"channel"`
		Items   []rssItem   `xml:"item"`
		Entries []atomEntry `xml:"entry"`
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&doc); err != nil {
		return nil, errors.New("订阅内容不是有效的 RSS 或 Atom")
	}
	out := []feedItem{}
	switch doc.XMLName.Local {
	case "rss", "RDF":
		items := doc.Channel.Items
		if doc.XMLName.Local == "RDF" {
			items = doc.Items
		}
		for _, v := range items {
			body := v.Encoded
			if body == "" {
				body = v.Description
			}
			date := v.PubDate
			if date == "" {
				date = v.Date
			}
			link := resolveLink(base, v.Link)
			if link == "" {
				link = canonicalURL(v.GUID)
			}
			out = append(out, feedItem{GUID: boundedGUID(v.GUID), Title: clip(plainText(v.Title), 1000), URL: link, Content: clip(plainText(body), 12000), Language: clip(doc.Channel.Language, 80), Publisher: clip(plainText(v.Source), 200), PublishedAt: feedDate(date, now)})
		}
	case "feed":
		for _, v := range doc.Entries {
			link := ""
			for _, l := range v.Links {
				if l.Rel == "" || l.Rel == "alternate" {
					link = resolveLink(base, l.Href)
					if link != "" {
						break
					}
				}
			}
			body := v.Content.value()
			if body == "" {
				body = v.Summary.value()
			}
			date := v.Published
			if date == "" {
				date = v.Updated
			}
			out = append(out, feedItem{GUID: boundedGUID(v.ID), Title: clip(plainText(v.Title.value()), 1000), URL: link, Content: clip(plainText(body), 12000), Language: clip(doc.Language, 80), PublishedAt: feedDate(date, now)})
		}
	default:
		return nil, errors.New("订阅内容不是 RSS 或 Atom")
	}
	if len(out) > 500 {
		out = out[:500]
	}
	return out, nil
}

func boundedGUID(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) > 4096 {
		return "sha256:" + hash(raw)
	}
	return raw
}
