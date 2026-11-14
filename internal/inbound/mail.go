package inbound

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding/htmlindex"
)

// Mail is what the rules and the AI get from one raw message.
type Mail struct {
	MessageID string
	// From is the sender of the original notification, lower case: the
	// From header, or the "De:" line of a forwarded block when the person
	// forwarded it by hand.
	From    string
	Subject string
	Date    time.Time
	Text    string
}

// maxParts and maxDepth keep a crafted message from making the walk slow.
const (
	maxParts = 30
	maxDepth = 5
	maxText  = 64 << 10
)

var wordDecoder = &mime.WordDecoder{CharsetReader: charsetReader}

func charsetReader(charset string, r io.Reader) (io.Reader, error) {
	enc, err := htmlindex.Get(charset)
	if err != nil {
		return nil, fmt.Errorf("inbound: charset %q: %w", charset, err)
	}
	return enc.NewDecoder().Reader(r), nil
}

// ParseMail reads a raw RFC 5322 message.
func ParseMail(raw []byte) (Mail, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return Mail{}, fmt.Errorf("inbound: read message: %w", err)
	}
	h := msg.Header
	m := Mail{MessageID: strings.TrimSpace(h.Get("Message-Id"))}
	if s, err := wordDecoder.DecodeHeader(h.Get("Subject")); err == nil {
		m.Subject = strings.Join(strings.Fields(s), " ")
	}
	if a, err := (&mail.AddressParser{WordDecoder: wordDecoder}).Parse(h.Get("From")); err == nil {
		m.From = strings.ToLower(a.Address)
	}
	if d, err := h.Date(); err == nil {
		m.Date = d
	}
	var parts int
	plain, htm := walk(h.Get("Content-Type"), h.Get("Content-Transfer-Encoding"), msg.Body, 0, &parts)
	text := plain
	if strings.TrimSpace(text) == "" {
		text = htmlText(htm)
	}
	m.Text = cleanText(text)
	if from := forwardedFrom(m.Text); from != "" {
		m.From = from
	}
	return m, nil
}

// walk returns the first text/plain and text/html bodies, decoded to UTF-8.
func walk(contentType, transfer string, body io.Reader, depth int, parts *int) (plain, htm string) {
	*parts++
	if depth > maxDepth || *parts > maxParts {
		return "", ""
	}
	if contentType == "" {
		contentType = "text/plain"
	}
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", ""
	}
	if strings.HasPrefix(media, "multipart/") {
		mr := multipart.NewReader(body, params["boundary"])
		for {
			p, err := mr.NextRawPart()
			if err != nil {
				return plain, htm
			}
			pp, ph := walk(p.Header.Get("Content-Type"), p.Header.Get("Content-Transfer-Encoding"), p, depth+1, parts)
			if plain == "" {
				plain = pp
			}
			if htm == "" {
				htm = ph
			}
		}
	}
	if media != "text/plain" && media != "text/html" {
		return "", ""
	}
	r := decodeTransfer(transfer, body)
	if cs := params["charset"]; cs != "" && !strings.EqualFold(cs, "utf-8") && !strings.EqualFold(cs, "us-ascii") {
		if cr, err := charsetReader(cs, r); err == nil {
			r = cr
		}
	}
	b, _ := io.ReadAll(io.LimitReader(r, maxText))
	if media == "text/html" {
		return "", string(b)
	}
	return string(b), ""
}

func decodeTransfer(enc string, r io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, newlineStripper{r})
	}
	return r
}

// newlineStripper drops line breaks the base64 decoder does not skip.
type newlineStripper struct{ r io.Reader }

func (n newlineStripper) Read(p []byte) (int, error) {
	k, err := n.r.Read(p)
	out := p[:0]
	for _, c := range p[:k] {
		if c != '\r' && c != '\n' && c != ' ' && c != '\t' {
			out = append(out, c)
		}
	}
	return len(out), err
}

var blockTags = map[string]bool{
	"br": true, "p": true, "div": true, "tr": true, "li": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "table": true, "td": true, "th": true,
}

// htmlText keeps the visible text, with a line break per block.
func htmlText(s string) string {
	if s == "" {
		return ""
	}
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	skip := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return b.String()
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if tag == "script" || tag == "style" || tag == "head" {
				skip++
			}
			if blockTags[tag] {
				b.WriteString("\n")
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if (tag == "script" || tag == "style" || tag == "head") && skip > 0 {
				skip--
			}
			if blockTags[tag] {
				b.WriteString("\n")
			}
		case html.TextToken:
			if skip == 0 {
				b.Write(z.Text())
				b.WriteString(" ")
			}
		}
	}
}

// cleanText collapses spaces inside lines and drops empty lines.
func cleanText(s string) string {
	s = strings.ReplaceAll(s, " ", " ")
	var lines []string
	for line := range strings.SplitSeq(s, "\n") {
		if l := strings.Join(strings.Fields(line), " "); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// A mail forwarded by hand starts the original with a block such as
// "---------- Forwarded message ---------\nDe: Mercado Pago <info@mercadopago.com>".
var (
	forwardMark = regexp.MustCompile(`(?i)(forwarded message|mensaje reenviado|original message|mensaje original)`)
	forwardFrom = regexp.MustCompile(`(?im)^(?:from|de):\s*(?:.*<)?([^\s<>@]+@[^\s<>]+?)>?\s*$`)
)

func forwardedFrom(text string) string {
	loc := forwardMark.FindStringIndex(text)
	if loc == nil {
		return ""
	}
	m := forwardFrom.FindStringSubmatch(text[loc[1]:])
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1])
}
