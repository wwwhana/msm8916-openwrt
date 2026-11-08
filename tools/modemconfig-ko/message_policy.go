// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/korean"
)

type messagePolicy struct {
	Provider      string `json:"provider"`
	SMSCharacters int    `json:"sms_characters"`
	SMSBytes      int    `json:"sms_bytes"`
	TextBytes     int    `json:"text_bytes"`
	MessageBytes  int    `json:"message_bytes"`
	ImageWidth    int    `json:"image_width"`
	ImageHeight   int    `json:"image_height"`
	Attachments   int    `json:"attachments"`
}

// AOSP CarrierConfig 1890/1891/1892 and APNs select the SIM's profile.
// LTE SMS has one 140-octet TP-UD; a longer text is an MMS text message (LMS).
func koreanMessagePolicy(plmn string) messagePolicy {
	provider := map[string]string{"45005": "SKT", "45006": "LG U+", "45008": "KT"}[plmn]
	if provider == "" {
		return messagePolicy{}
	}
	p := messagePolicy{Provider: provider, SMSBytes: 140, TextBytes: 2000, MessageBytes: 1048576, ImageWidth: 2592, ImageHeight: 1944, Attachments: 3}
	if plmn == "45006" {
		p.SMSCharacters = 80
	}
	return p
}

func mmsTextBytes(text string) ([]byte, uint64) {
	if encoded, err := korean.EUCKR.NewEncoder().Bytes([]byte(text)); err == nil {
		return encoded, 38
	}
	return []byte(text), 106
}

type messagePlan struct {
	Kind  string `json:"kind"`
	Bytes int    `json:"bytes"`
	Limit int    `json:"limit"`
	Valid bool   `json:"valid"`
	Error string `json:"reason"`
}

func planMessage(policy messagePolicy, text string, attachments int) messagePlan {
	plan := messagePlan{Kind: "SMS", Limit: policy.SMSBytes}
	if policy.Provider == "" {
		plan.Error = "unsupported-carrier"
		return plan
	}
	if attachments < 0 || attachments > policy.Attachments || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		plan.Error = "invalid-message"
		return plan
	}
	// Match ims-go's GSM 7-bit default and extension alphabets exactly.
	const alphabet = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"
	const extension = "\f^{}\\[~]|€"
	septets, gsm := 0, true
	for _, c := range text {
		switch {
		case strings.ContainsRune(alphabet, c):
			septets++
		case strings.ContainsRune(extension, c):
			septets += 2
		default:
			gsm = false
		}
	}
	units := len(utf16.Encode([]rune(text)))
	plan.Bytes = units * 2
	if gsm {
		plan.Bytes = (septets*7 + 7) / 8
	}
	if attachments > 0 || plan.Bytes > policy.SMSBytes || (policy.SMSCharacters > 0 && units > policy.SMSCharacters) {
		plan.Kind, plan.Limit = "LMS", policy.TextBytes
		if attachments > 0 {
			plan.Kind = "MMS"
		}
		encoded, _ := mmsTextBytes(text)
		plan.Bytes = len(encoded)
	}
	plan.Valid = (strings.TrimSpace(text) != "" || attachments > 0) && plan.Bytes <= plan.Limit
	if plan.Bytes > plan.Limit {
		plan.Error = "message-too-long"
	}
	return plan
}
