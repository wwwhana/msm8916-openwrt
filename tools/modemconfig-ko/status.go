// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const runtimeStatusPath = "/var/run/modemconfig-ko.json"

// Volatile UI state contains no subscriber identifiers or message content.
func writeRuntimeStatus(path string, slot uint8, registered, received bool, identity ...string) error {
	registration := "registering"
	if registered {
		registration = "registered"
	}
	return writeMessagingStatus(path, slot, registration, "ims", registered, registered, identity...)
}

func writeMessagingStatus(path string, slot uint8, registration, transport string, smsReady, mmsReady bool, identity ...string) error {
	sms := "unavailable"
	if smsReady {
		sms = "ready"
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".modemconfig-ko-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	state := map[string]any{
		"version": 1, "pid": os.Getpid(), "updated_at": time.Now().Unix(),
		"slot": slot, "registration": registration, "sms": sms,
		"sms_transport": transport, "sms_ready": smsReady, "mms_ready": mmsReady,
	}
	runtimeServices.RLock()
	if runtimeServices.voice != nil {
		state["voice_ready"] = registeredVoice(runtimeServices.voice)
	}
	if runtimeServices.rcs != nil {
		state["rcs"] = runtimeServices.rcs.snapshot()
	}
	runtimeServices.RUnlock()
	if len(identity) >= 2 {
		state["subscription"], state["session"] = identity[0], identity[1]
	}
	if len(identity) >= 3 {
		state["plmn"] = identity[2]
	}
	err = json.NewEncoder(file).Encode(state)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}

func registeredVoice(v *voiceService) bool { return v.client.Registration().Registered }
