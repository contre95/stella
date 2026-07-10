// Package opds implements an OPDS 1.2 catalog feed server. It exposes the
// user's decks (and their groups) as an Atom-based catalog that OPDS clients
// (such as stella-x4) can browse and download from.
package opds

import "encoding/xml"

// Feed is an Atom feed with OPDS catalog extensions.
type Feed struct {
	XMLName xml.Name `xml:"http://www.w3.org/2005/Atom feed"`
	Title   string   `xml:"title"`
	ID      string   `xml:"id"`
	Updated string   `xml:"updated"`
	Links   []Link   `xml:"link"`
	Entries []Entry  `xml:"entry"`
}

// Link is an Atom link element with optional OPDS attributes.
type Link struct {
	Rel      string `xml:"rel,attr,omitempty"`
	Href     string `xml:"href,attr"`
	Type     string `xml:"type,attr,omitempty"`
	Title    string `xml:"title,attr,omitempty"`
	Length   int64  `xml:"length,attr,omitempty"`
}

// Entry is an Atom entry representing a deck or a navigation link to a group.
type Entry struct {
	Title    string  `xml:"title"`
	ID       string  `xml:"id"`
	Updated  string  `xml:"updated"`
	Summary  string  `xml:"summary,omitempty"`
	Links    []Link  `xml:"link"`
	Content  *Content `xml:"content,omitempty"`
}

// Content is an Atom content element.
type Content struct {
	Type string `xml:"type,attr,omitempty"`
	Text string `xml:",chardata"`
}
