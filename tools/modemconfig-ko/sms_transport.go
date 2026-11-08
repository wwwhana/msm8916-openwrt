// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// TTA §9 requires both transports. These are defaults, not a substitute for
// operator provisioning; a provisioned choice belongs to one SIM, not a slot.
func smsTransport(root, scope, plmn string) (string, error) {
	if !hexID(scope, 64) {
		return "", errors.New("invalid SIM scope")
	}
	transport := map[string]string{"45005": "nas", "45008": "ims", "45006": "ims"}[plmn]
	if transport == "" {
		return "", errors.New("unsupported SMS carrier")
	}
	if _, _, err := scopedSIMPaths(root, scope); err != nil {
		return "", err
	}
	name := filepath.Join(root, scope, "sms-transport")
	info, err := os.Lstat(name)
	if os.IsNotExist(err) {
		return transport, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 16 {
		return "", errors.New("invalid SMS transport policy")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	transport = strings.TrimSpace(string(data))
	if transport != "nas" && transport != "ims" {
		return "", errors.New("invalid SMS transport policy")
	}
	return transport, nil
}

func sessionMessageDB(scope, plmn string, slot uint8, identities ...string) (*messageDB, string, error) {
	inbox, segments, err := scopedSIMPaths(simStateDirectory, scope)
	if err != nil {
		return nil, "", err
	}
	db, err := openMessageDB(messageDatabasePath)
	if err != nil {
		return nil, "", err
	}
	if err := db.bind(scope, slot, plmn); err != nil {
		return nil, "", err
	}
	session := opaqueID()
	steps := []func() error{
		func() error { return db.recoverPending(session) },
		func() error { return db.importSegments(segments) },
		func() error { return db.importInbox("/etc/a8-ims/inbox", identities, false) },
		func() error { return db.importInbox(inbox, nil, true) },
		func() error { return db.importContacts("/etc/modem/sms_manager_phonebook.user") },
		db.recoverSegments,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, "", err
		}
	}
	return db, session, nil
}
