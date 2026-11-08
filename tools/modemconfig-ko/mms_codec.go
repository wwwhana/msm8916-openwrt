// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/korean"
	textunicode "golang.org/x/text/encoding/unicode"
)

const maxMMSBytes = 8 << 20

// OMA MMS Encapsulation 1.3 §§7.1–7.4 uses WSP integers and strings,
// including inside the individual multipart headers. Never treat it as MIME.
type wspReader struct {
	data []byte
	pos  int
	err  error
}

func (r *wspReader) left() int { return len(r.data) - r.pos }
func (r *wspReader) take(n int) []byte {
	if r.err != nil || n < 0 || n > r.left() {
		r.err = io.ErrUnexpectedEOF
		return nil
	}
	value := r.data[r.pos : r.pos+n]
	r.pos += n
	return value
}
func (r *wspReader) peek() byte {
	if r.left() == 0 {
		r.err = io.ErrUnexpectedEOF
		return 0
	}
	return r.data[r.pos]
}
func (r *wspReader) octet() byte {
	value := r.take(1)
	if len(value) == 0 {
		return 0
	}
	return value[0]
}
func (r *wspReader) uintvar() int {
	value := 0
	for range 5 {
		b := r.octet()
		value = value<<7 | int(b&0x7f)
		if b&0x80 == 0 {
			if value > maxMMSBytes {
				r.err = errors.New("MMS value exceeds limit")
			}
			return value
		}
	}
	r.err = errors.New("invalid WSP uintvar")
	return 0
}
func (r *wspReader) length() int {
	n := r.octet()
	if n == 31 {
		return r.uintvar()
	}
	if n > 30 {
		r.err = errors.New("invalid WSP length")
		return 0
	}
	return int(n)
}
func (r *wspReader) integer() uint64 {
	if r.peek()&0x80 != 0 {
		return uint64(r.octet() & 0x7f)
	}
	n := int(r.octet())
	if n < 1 || n > 8 {
		r.err = errors.New("invalid WSP integer")
		return 0
	}
	var value uint64
	for _, b := range r.take(n) {
		value = value<<8 | uint64(b)
	}
	return value
}
func (r *wspReader) text() []byte {
	if r.peek() == 127 || r.peek() == '"' {
		r.octet()
	}
	start := r.pos
	for r.left() > 0 && r.err == nil {
		if r.octet() == 0 {
			return r.data[start : r.pos-1]
		}
	}
	r.err = io.ErrUnexpectedEOF
	return nil
}
func (r *wspReader) block() *wspReader {
	n := r.length()
	return &wspReader{data: r.take(n), err: r.err}
}
func (r *wspReader) finish(child *wspReader) {
	if child.err != nil {
		r.err = child.err
	}
}
func (r *wspReader) skip() {
	switch {
	case r.peek() <= 31:
		n := r.length()
		r.take(n)
	case r.peek() >= 128:
		r.octet()
	default:
		r.text()
	}
}

func mmsText(data []byte, charset uint64) (string, error) {
	var decoded []byte
	var err error
	switch charset {
	case 0, 3, 106:
		if !utf8.Valid(data) {
			return "", errors.New("invalid UTF-8 MMS text")
		}
		decoded = data
	case 4:
		decoded, err = charmap.ISO8859_1.NewDecoder().Bytes(data)
	case 36, 38:
		decoded, err = korean.EUCKR.NewDecoder().Bytes(data)
	case 1000, 1013:
		decoded, err = textunicode.UTF16(textunicode.BigEndian, textunicode.UseBOM).NewDecoder().Bytes(data)
	case 1014:
		decoded, err = textunicode.UTF16(textunicode.LittleEndian, textunicode.UseBOM).NewDecoder().Bytes(data)
	case 1015:
		decoded, err = textunicode.UTF16(textunicode.BigEndian, textunicode.ExpectBOM).NewDecoder().Bytes(data)
	default:
		return "", errors.New("unsupported MMS text encoding")
	}
	if err != nil {
		return "", errors.New("invalid MMS text encoding")
	}
	return strings.TrimRight(string(decoded), "\x00"), nil
}
func (r *wspReader) encoded() string {
	if r.peek() == 0 {
		r.octet()
		return ""
	}
	var value string
	var err error
	if r.peek() <= 31 {
		child := r.block()
		charset := child.integer()
		value, err = mmsText(child.text(), charset)
		r.finish(child)
	} else {
		value, err = mmsText(r.text(), 106)
	}
	if err != nil {
		r.err = err
	}
	return value
}

var wspMediaTypes = map[uint64]string{
	0x00: "*/*", 0x02: "text/html", 0x03: "text/plain", 0x08: "text/vnd.wap.wml",
	0x1b: "image/gif", 0x1c: "image/jpeg", 0x1d: "image/tiff", 0x1e: "image/png",
	0x23: "application/vnd.wap.multipart.mixed", 0x26: "application/xml",
	0x33: "application/vnd.wap.multipart.related", 0x3e: "application/vnd.wap.mms-message",
}

type mmsContentType struct {
	media, name, start string
	charset            uint64
}

func (r *wspReader) mediaType() string {
	if r.peek() >= 128 {
		value := r.integer()
		if media := wspMediaTypes[value]; media != "" {
			return media
		}
		return "application/octet-stream"
	}
	return strings.ToLower(string(r.text()))
}
func (r *wspReader) contentType() mmsContentType {
	child := r
	if r.peek() <= 31 {
		child = r.block()
	}
	value := mmsContentType{media: child.mediaType(), charset: 106}
	if child != r {
		for child.left() > 0 && child.err == nil {
			key := ""
			if child.peek() >= 128 {
				key = map[byte]string{0x81: "charset", 0x85: "name", 0x97: "name", 0x86: "name", 0x98: "name", 0x8a: "start", 0x99: "start"}[child.octet()]
			} else {
				key = strings.ToLower(string(child.text()))
			}
			switch key {
			case "charset":
				if child.peek() < 32 || child.peek() >= 128 {
					value.charset = child.integer()
				} else {
					charset := strings.ToLower(string(child.text()))
					value.charset = map[string]uint64{"utf-8": 106, "us-ascii": 3, "euc-kr": 38, "ks_c_5601-1987": 36, "utf-16": 1015, "iso-8859-1": 4}[charset]
					if value.charset == 0 {
						child.err = errors.New("unsupported MMS charset")
					}
				}
			case "name", "filename":
				value.name = string(child.text())
			case "start":
				value.start = string(child.text())
			default:
				child.skip()
			}
		}
		r.finish(child)
	}
	return value
}

type mmsPart struct {
	media, name, contentID, location, disposition, text string
	charset                                             uint64
	data                                                []byte
}
type mmsPDU struct {
	kind, version, retrieveStatus, reportAllowed, responseStatus byte
	transaction, messageID, from, subject, location              string
	recipients                                                   []string
	expires, date                                                time.Time
	size                                                         uint64
	parts                                                        []mmsPart
}

func decodeMMS(data []byte, received time.Time) (mmsPDU, error) {
	var p mmsPDU
	if len(data) > maxMMSBytes {
		return p, errors.New("MMS exceeds size limit")
	}
	r := &wspReader{data: data}
	for fields := 0; r.left() > 0 && r.err == nil; fields++ {
		if fields >= 128 {
			r.err = errors.New("too many MMS headers")
			break
		}
		field := r.octet()
		if field < 128 {
			r.pos--
			r.text()
			r.skip()
			continue
		}
		switch field {
		case 0x8c:
			p.kind = r.octet()
		case 0x8d:
			p.version = byte(r.integer())
		case 0x98:
			p.transaction = string(r.text())
		case 0x8b:
			p.messageID = string(r.text())
		case 0x83:
			p.location = string(r.text())
		case 0x89:
			child := r.block()
			if child.octet() == 128 {
				p.from = child.encoded()
			}
			r.finish(child)
		case 0x81, 0x82, 0x97:
			p.recipients = append(p.recipients, r.encoded())
		case 0x96:
			p.subject = r.encoded()
		case 0x85:
			p.date = time.Unix(int64(r.integer()), 0).UTC()
		case 0x88:
			child := r.block()
			token := child.octet()
			value := child.integer()
			if value > 1<<40 || (token == 129 && value > 315360000) || (token != 128 && token != 129) {
				child.err = errors.New("invalid MMS expiry")
			} else if token == 128 {
				p.expires = time.Unix(int64(value), 0).UTC()
			} else {
				p.expires = received.Add(time.Duration(value) * time.Second)
			}
			r.finish(child)
		case 0x8e:
			p.size = r.integer()
		case 0x99:
			p.retrieveStatus = r.octet()
		case 0x92:
			p.responseStatus = r.octet()
		case 0x91:
			p.reportAllowed = r.octet()
		case 0x84:
			content := r.contentType()
			p.parts = mmsParts(r, content, 0)
			if r.left() != 0 {
				r.err = errors.New("trailing MMS content")
			}
		default:
			r.skip()
		}
	}
	if r.err != nil || p.kind == 0 || p.version < 0x10 || p.version > 0x1f {
		return p, errors.New("invalid MMS PDU")
	}
	return p, nil
}

func mmsParts(r *wspReader, content mmsContentType, depth int) []mmsPart {
	if depth > 4 {
		r.err = errors.New("MMS nesting exceeds limit")
		return nil
	}
	if !strings.HasPrefix(content.media, "application/vnd.wap.multipart.") {
		part := mmsPart{media: content.media, charset: content.charset, name: content.name, data: r.take(r.left())}
		if part.media == "text/plain" {
			part.text, r.err = mmsText(part.data, part.charset)
		}
		return []mmsPart{part}
	}
	count := r.uintvar()
	if count > 64 {
		r.err = errors.New("too many MMS parts")
		return nil
	}
	var parts []mmsPart
	for range count {
		if r.err != nil {
			break
		}
		headerLength, dataLength := r.uintvar(), r.uintvar()
		headers := &wspReader{data: r.take(headerLength), err: r.err}
		partType := headers.contentType()
		part := mmsPart{media: partType.media, charset: partType.charset, name: partType.name}
		for headers.left() > 0 && headers.err == nil {
			field := headers.octet()
			switch field {
			case 0xc0:
				part.contentID = string(headers.text())
			case 0x8e:
				part.location = string(headers.text())
			case 0xae, 0xc5:
				value := headers.block()
				if value.peek() >= 128 {
					part.disposition = map[byte]string{128: "form-data", 129: "attachment", 130: "inline"}[value.octet()]
				} else {
					part.disposition = string(value.text())
				}
				// Preserve the complete raw PDU even for unrecognized parameters.
				headers.finish(value)
			default:
				if field < 128 {
					headers.pos--
					headers.text()
				}
				headers.skip()
			}
		}
		r.finish(headers)
		part.data = r.take(dataLength)
		if r.err != nil {
			break
		}
		if strings.HasPrefix(part.media, "application/vnd.wap.multipart.") {
			child := &wspReader{data: part.data}
			parts = append(parts, mmsParts(child, partType, depth+1)...)
			r.finish(child)
		} else {
			if part.media == "text/plain" {
				part.text, r.err = mmsText(part.data, part.charset)
			}
			parts = append(parts, part)
		}
		if len(parts) > 64 {
			r.err = errors.New("too many MMS parts")
		}
	}
	return parts
}

func decodeMMSPush(data []byte) ([]byte, error) {
	r := &wspReader{data: data}
	r.octet() // WSP transaction ID is independent of the MMS transaction ID.
	if kind := r.octet(); kind != 6 && kind != 7 {
		return nil, errors.New("unsupported WAP push")
	}
	n := r.uintvar()
	headers := &wspReader{data: r.take(n), err: r.err}
	content := headers.contentType()
	if headers.err != nil || r.err != nil || content.media != "application/vnd.wap.mms-message" {
		return nil, errors.New("not an MMS push")
	}
	return r.take(r.left()), r.err
}

func mmsAddress(value string) string {
	if index := strings.Index(strings.ToUpper(value), "/TYPE="); index >= 0 {
		value = value[:index]
	}
	return canonicalPeer(value)
}
func mmsVersion(value byte) string {
	return strconv.Itoa(int(value>>4)) + "." + strconv.Itoa(int(value&15))
}
