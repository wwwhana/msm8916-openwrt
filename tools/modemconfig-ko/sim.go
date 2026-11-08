// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"path/filepath"

	"github.com/damonto/wwan-go/qcom"
	simcard "github.com/damonto/wwan-go/sim/card"
	"github.com/damonto/wwan-go/sim/command"
	"github.com/damonto/wwan-go/sim/simfile"
)

const simStateDirectory = "/etc/modemconfig-ko/sims"

func simScope(iccid string) (string, error) {
	if len(iccid) < 18 || len(iccid) > 22 {
		return "", errors.New("SIM identifier unavailable")
	}
	for _, digit := range iccid {
		if digit < '0' || digit > '9' {
			return "", errors.New("invalid SIM identifier")
		}
	}
	key := sha256.Sum256([]byte("modemconfig-ko/sim\x00" + iccid))
	return hex.EncodeToString(key[:]), nil
}

func simProfileName(scope string) (string, error) {
	key, err := hex.DecodeString(scope)
	if err != nil || len(key) != sha256.Size {
		return "", errors.New("invalid SIM profile scope")
	}
	// Older modem profile databases accept only a short name. Keep 72 bits
	// of the SIM fingerprint here; the private storage uses all 256 bits.
	return "KO-" + base64.RawURLEncoding.EncodeToString(key[:9]), nil
}

func scopedSIMPaths(root, scope string) (inbox, segments string, err error) {
	key, decodeErr := hex.DecodeString(scope)
	if decodeErr != nil || len(key) != sha256.Size {
		return "", "", errors.New("invalid SIM storage scope")
	}
	base := filepath.Join(root, scope)
	inbox, segments = filepath.Join(base, "inbox"), filepath.Join(base, "segments")
	for _, directory := range []string{root, base, inbox, segments} {
		if err := prepareInbox(directory); err != nil {
			return "", "", err
		}
	}
	return inbox, segments, nil
}

func currentSIMScope(ctx context.Context, reader simcard.Reader) (string, error) {
	data, err := (command.App{Reader: reader}).Transparent(ctx, []byte{0x2f, 0xe2})
	if err != nil {
		return "", err
	}
	var iccid simfile.ICCID
	if err := iccid.UnmarshalBinary(data); err != nil {
		return "", errors.New("invalid SIM identifier response")
	}
	return simScope(iccid.String())
}

func verifySIMScope(ctx context.Context, reader simcard.Reader, expected string) error {
	actual, err := currentSIMScope(ctx, reader)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("active SIM changed")
	}
	return nil
}

func verifySIMSession(ctx context.Context, client *qcom.Client, reader simcard.Reader, expected string) error {
	slot, _, err := activeSIMSlot(ctx, client)
	if err != nil {
		return err
	}
	if slot != client.Slot() {
		return errors.New("active SIM mapping changed")
	}
	return verifySIMScope(ctx, reader, expected)
}

// The IMS PDN adapter currently uses the primary data subscription. Follow
// its logical slot, which need not be physical slot 1; never switch it here.
func primaryPhysicalSlot(status qcom.SlotStatus) (uint8, error) {
	var selected uint8
	for index, slot := range status.Slots {
		if slot.PhysicalSlotStatus == qcom.SlotStateActive && slot.LogicalSlot == 1 {
			if selected != 0 || slot.PhysicalCardStatus != qcom.PhysicalCardStatePresent || index >= 5 {
				return 0, errors.New("invalid primary SIM mapping")
			}
			selected = uint8(index + 1)
		}
	}
	if selected == 0 {
		return 0, errors.New("primary SIM mapping unavailable")
	}
	return selected, nil
}

func activeSIMSlot(ctx context.Context, client *qcom.Client) (uint8, map[string]any, error) {
	status, err := client.SlotStatus(ctx)
	info := map[string]any{"stage": "sim-slots", "mapping_known": err == nil}
	if err == nil {
		info["slot_count"] = len(status.Slots)
		slot, err := primaryPhysicalSlot(status)
		info["selected_slot"] = slot
		return slot, info, err
	}
	if !errors.Is(err, qcom.QMIErrorNotSupported) {
		return 0, info, err
	}
	cards, err := client.CardStatus(ctx)
	if err != nil {
		return 0, info, err
	}
	info["card_status_count"] = len(cards.Cards)
	if len(cards.Cards) != 1 || cards.Cards[0].State != qcom.CardStatePresent {
		return 0, info, errors.New("multi-slot mapping is unavailable on this modem")
	}
	info["selected_slot"] = 1
	return 1, info, nil
}

func probeSlots(ctx context.Context) (result error) {
	client, err := openQCOMSlot(ctx, 1)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, client.Close()) }()
	_, info, err := activeSIMSlot(ctx, client)
	emit(info)
	if err != nil {
		return failed("sim-slot-selection", err)
	}
	return nil
}
