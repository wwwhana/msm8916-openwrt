// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const uploadSchema = `CREATE TABLE IF NOT EXISTS message_uploads(
 id TEXT PRIMARY KEY, subscription_id TEXT NOT NULL REFERENCES subscriptions(id),
 session_id TEXT NOT NULL, filename TEXT NOT NULL, mime_type TEXT NOT NULL,
 byte_size INTEGER NOT NULL, received_bytes INTEGER NOT NULL DEFAULT 0,
 sha256 TEXT, created_at TEXT NOT NULL);`

func (db *messageDB) uploadDirectory() string { return filepath.Join(filepath.Dir(db.path), "uploads") }

// RPC requests run in separate processes. Serialize file changes with queuing.
func (db *messageDB) withUploadLock(action func() error) error {
	if err := prepareInbox(db.uploadDirectory()); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(db.uploadDirectory(), ".lock"), os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(file.Fd()), unix.LOCK_UN)
	return action()
}

func outgoingMedia(data []byte, policy messagePolicy) (string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err == nil {
		media := map[string]string{"jpeg": "image/jpeg", "png": "image/png", "gif": "image/gif"}[format]
		if media != "" && config.Width > 0 && config.Height > 0 && config.Width <= policy.ImageWidth && config.Height <= policy.ImageHeight {
			return media, nil
		}
		return "", errors.New("attachment-dimensions")
	}
	if bytes.HasPrefix(data, []byte("#!AMR\n")) || bytes.HasPrefix(data, []byte("#!AMR-WB\n")) {
		return "audio/amr", nil
	}
	media := http.DetectContentType(data)
	if media == "audio/mpeg" {
		return media, nil
	}
	if len(data) >= 12 && string(data[4:8]) == "ftyp" {
		brand := string(data[8:12])
		if strings.HasPrefix(brand, "3g") {
			return "video/3gpp", nil
		}
		if media == "video/mp4" {
			return media, nil
		}
	}
	return "", errors.New("attachment-type")
}

func (db *messageDB) cleanUploads() error {
	rows, err := db.run("DELETE FROM message_uploads WHERE created_at<"+sqlText(time.Now().Add(-time.Hour).UTC().Format(databaseTime))+" RETURNING id;", true)
	if err != nil {
		return err
	}
	for _, row := range rows {
		id, _ := row["id"].(string)
		if hexID(id, 64) {
			if err := os.Remove(filepath.Join(db.uploadDirectory(), id)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	// Recover files left between creation/removal and the corresponding SQL.
	entries, err := os.ReadDir(db.uploadDirectory())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !hexID(entry.Name(), 64) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.ModTime().Before(time.Now().Add(-time.Hour)) {
			continue
		}
		rows, err := db.run("SELECT id FROM message_uploads WHERE id="+sqlText(entry.Name())+";", true)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			if err := os.Remove(filepath.Join(db.uploadDirectory(), entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func (db *messageDB) upload(method string, request messageRequest, policy messagePolicy) (any, error) {
	var result any
	err := db.withUploadLock(func() error {
		if err := db.cleanUploads(); err != nil {
			return err
		}
		id := request.ID
		if method == "begin_upload" {
			name := filepath.Base(strings.ReplaceAll(request.Filename, "\\", "/"))
			if !hexID(request.RequestID, 32) || request.Size < 1 || request.Size > int64(policy.MessageBytes-16384) || name == "." || name == "/" || name == "" || utf8.RuneCountInString(name) > 120 || strings.IndexFunc(name, unicode.IsControl) >= 0 || !utf8.ValidString(name) {
				return errors.New("invalid-attachment")
			}
			id = stableID(db.subscription, request.Session, "upload", request.RequestID)
			existing, err := db.run("SELECT * FROM message_uploads WHERE id="+sqlText(id)+";", true)
			if err != nil {
				return err
			}
			if len(existing) > 0 {
				if existing[0]["filename"] != name || existing[0]["mime_type"] != request.MimeType || existing[0]["byte_size"] != float64(request.Size) {
					return errors.New("request-conflict")
				}
				result = map[string]any{"id": id, "offset": existing[0]["received_bytes"]}
				return nil
			}
			rows, err := db.run("SELECT COUNT(*) AS count,COALESCE(SUM(byte_size),0) AS size FROM message_uploads WHERE subscription_id="+sqlText(db.subscription)+";", true)
			if err != nil {
				return err
			}
			if rows[0]["count"].(float64) >= 6 || rows[0]["size"].(float64)+float64(request.Size) > float64(policy.MessageBytes*3) {
				return errors.New("attachment-storage")
			}
			path := filepath.Join(db.uploadDirectory(), id)
			file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|unix.O_NOFOLLOW, 0600)
			if err != nil {
				return err
			}
			_ = file.Close()
			_, err = db.run("INSERT INTO message_uploads(id,subscription_id,session_id,filename,mime_type,byte_size,created_at) VALUES("+sqlText(id)+","+sqlText(db.subscription)+","+sqlText(request.Session)+","+sqlText(name)+","+sqlText(request.MimeType)+","+strconv.FormatInt(request.Size, 10)+","+sqlText(timestamp())+");", false)
			if err != nil {
				_ = os.Remove(path)
				return err
			}
			result = map[string]any{"id": id, "offset": 0}
			return nil
		}
		if !hexID(id, 64) || request.Offset < 0 {
			return errors.New("invalid-request")
		}
		rows, err := db.run("SELECT * FROM message_uploads WHERE id="+sqlText(id)+" AND subscription_id="+sqlText(db.subscription)+" AND session_id="+sqlText(request.Session)+";", true)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return errors.New("attachment-unavailable")
		}
		path := filepath.Join(db.uploadDirectory(), id)
		if method == "delete_upload" {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			_, err := db.run("DELETE FROM message_uploads WHERE id="+sqlText(id)+";", false)
			result = map[string]any{"ok": err == nil}
			return err
		}
		data, err := base64.StdEncoding.Strict().DecodeString(request.Data)
		if err != nil || len(data) == 0 || len(data) > 8192 || request.Offset+int64(len(data)) > int64(rows[0]["byte_size"].(float64)) {
			return errors.New("invalid-attachment")
		}
		file, err := os.OpenFile(path, os.O_RDWR|unix.O_NOFOLLOW, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() < request.Offset {
			return errors.New("invalid-attachment")
		}
		if info.Size() > request.Offset {
			previous := make([]byte, len(data))
			if _, err := file.ReadAt(previous, request.Offset); err != nil || !bytes.Equal(previous, data) {
				return errors.New("request-conflict")
			}
		} else {
			if _, err := file.WriteAt(data, request.Offset); err != nil {
				return err
			}
			if err := file.Sync(); err != nil {
				return err
			}
		}
		info, err = file.Stat()
		if err != nil {
			return err
		}
		digest := "NULL"
		if info.Size() == int64(rows[0]["byte_size"].(float64)) {
			all, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			media, err := outgoingMedia(all, policy)
			if err != nil {
				return err
			}
			if media != rows[0]["mime_type"] {
				return errors.New("attachment-type")
			}
			digest = sqlText(rawDigest(all))
		}
		_, err = db.run("UPDATE message_uploads SET received_bytes="+strconv.FormatInt(info.Size(), 10)+",sha256="+digest+" WHERE id="+sqlText(id)+";", false)
		result = map[string]any{"id": id, "offset": info.Size(), "complete": digest != "NULL"}
		return err
	})
	return result, err
}
