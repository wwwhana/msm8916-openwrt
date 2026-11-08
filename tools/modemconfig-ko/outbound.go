// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type outgoingSMS struct {
	To   string `json:"to"`
	Text string `json:"text"`
}

// stdin keeps the destination and message out of command arguments and logs.
func readOutgoingSMS(reader io.Reader) (*outgoingSMS, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 4097))
	if err != nil || len(data) > 4096 || !utf8.Valid(data) {
		return nil, errors.New("invalid SMS input")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var request outgoingSMS
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid SMS input")
	}
	number := strings.TrimPrefix(request.To, "+")
	if len(number) < 8 || len(number) > 15 {
		return nil, errors.New("invalid SMS destination")
	}
	for _, digit := range number {
		if digit < '0' || digit > '9' {
			return nil, errors.New("invalid SMS destination")
		}
	}
	if strings.TrimSpace(request.Text) == "" || len(utf16.Encode([]rune(request.Text))) > 70 {
		return nil, errors.New("SMS test requires one short message")
	}
	return &request, nil
}
