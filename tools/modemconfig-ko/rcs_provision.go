// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/damonto/ims-go/ims/security"
	imssms "github.com/damonto/ims-go/ims/sms"
)

const rcsSMSPort = 37273
const maxRCSConfig = 1 << 20

type rcsNode struct {
	Type     string     `xml:"type,attr"`
	Params   []rcsParam `xml:"parm"`
	Children []rcsNode  `xml:"characteristic"`
}
type rcsParam struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}
type rcsDocument struct {
	XMLName xml.Name  `xml:"wap-provisioningdoc"`
	Nodes   []rcsNode `xml:"characteristic"`
}

func (n rcsNode) value(key string) string {
	for _, p := range n.Params {
		if strings.EqualFold(p.Name, key) {
			return p.Value
		}
	}
	return ""
}
func (n rcsNode) child(kind string) *rcsNode {
	for i := range n.Children {
		if strings.EqualFold(n.Children[i].Type, kind) {
			return &n.Children[i]
		}
	}
	return nil
}
func rcsNodes(nodes []rcsNode, kind string) []rcsNode {
	var result []rcsNode
	for _, n := range nodes {
		if strings.EqualFold(n.Type, kind) {
			result = append(result, n)
		}
		result = append(result, rcsNodes(n.Children, kind)...)
	}
	return result
}

func parseRCSConfig(data []byte) (*rcsDocument, error) {
	if len(data) == 0 || len(data) > maxRCSConfig {
		return nil, errors.New("rcs-config-invalid")
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	var document rcsDocument
	if err := d.Decode(&document); err != nil {
		return nil, errors.New("rcs-config-invalid")
	}
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("rcs-config-invalid")
		}
		if c, ok := token.(xml.CharData); !ok || len(bytes.TrimSpace(c)) != 0 {
			return nil, errors.New("rcs-config-invalid")
		}
	}
	count := 0
	var check func([]rcsNode, int) bool
	check = func(nodes []rcsNode, depth int) bool {
		if depth > 16 {
			return false
		}
		for _, n := range nodes {
			count++
			if count > 4096 || len(n.Type) > 128 {
				return false
			}
			seen := map[string]bool{}
			for _, p := range n.Params {
				k := strings.ToLower(p.Name)
				if seen[k] || len(k) > 128 || len(p.Value) > 8192 {
					return false
				}
				seen[k] = true
			}
			if !check(n.Children, depth+1) {
				return false
			}
		}
		return true
	}
	if !check(document.Nodes, 0) {
		return nil, errors.New("rcs-config-invalid")
	}
	versions := rcsNodes(document.Nodes, "VERS")
	if len(versions) != 1 {
		return nil, errors.New("rcs-config-invalid")
	}
	if _, err := strconv.Atoi(versions[0].value("version")); err != nil {
		return nil, errors.New("rcs-config-invalid")
	}
	return &document, nil
}

func rcsACS(plmn string) (string, string, error) {
	switch plmn {
	case "45005":
		return "https://config.rcs.mnc005.mcc450.pub.connectrcs.com/", "SKT", nil
	case "45008":
		return "https://config.rcs.mnc008.mcc450.pub.connectrcs.com/", "KT", nil
	case "45006":
		return "https://config.rcs.mnc006.mcc450.pub.connectrcs.com/", "LGU", nil
	}
	return "", "", errors.New("unsupported-carrier")
}

type rcsFailure struct {
	state  string
	status int
	retry  time.Duration
}

func (e *rcsFailure) Error() string { return e.state }

type rcsCache struct {
	PLMN    string `json:"plmn"`
	Fetched int64  `json:"fetched"`
	XML     []byte `json:"xml"`
}
type rcsService struct {
	mu              sync.Mutex
	state           string
	httpStatus      int
	version         int
	updated         int64
	retry           chan struct{}
	client          *rcsSIP
	sipStatus       int
	auth, transport string
	chat            bool
	discovery       string
	publishStatus   int
	publishUntil    time.Time
}

func (r *rcsService) set(state string, status, version int) {
	r.mu.Lock()
	r.state, r.httpStatus, r.version, r.updated = state, status, version, time.Now().Unix()
	r.mu.Unlock()
	emit(map[string]any{"stage": "rcs", "state": state, "http_status": status})
}
func (r *rcsService) snapshot() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ready := r.state, r.readyLocked()
	if state == "registered" && !ready {
		state = "publishing"
	}
	return map[string]any{"state": state, "ready": ready, "http_status": r.httpStatus, "sip_status": r.sipStatus, "config_version": r.version, "updated_at": r.updated, "auth": r.auth, "transport": r.transport, "chat": r.chat, "discovery": r.discovery, "publish_status": r.publishStatus}
}

func (r *rcsService) readyLocked() bool {
	return r.state == "registered" && (r.discovery != "presence" || time.Now().Before(r.publishUntil))
}

func rcsHTTPClient() (*http.Client, error) {
	c, err := mmsHTTPClient(mmsCarrier{})
	if err != nil {
		return nil, err
	}
	c.Transport.(*http.Transport).TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	c.Transport.(*http.Transport).DialContext = rcsDialContext
	c.Jar, _ = cookiejar.New(nil)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c, nil
}

// Bootstrap carries no identifiers. SIM identities, OTP and cookies are sent
// only to this operator's HTTPS origin; TLS verification is always enabled.
func fetchRCS(ctx context.Context, client *http.Client, server, agent string, params url.Values, otp func(context.Context) (string, error)) ([]byte, error) {
	base, err := url.Parse(server)
	if err != nil || base.Scheme != "https" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("rcs-server-invalid")
	}
	request := func(target *url.URL) ([]byte, int, string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return nil, 0, "", errors.New("rcs-network")
		}
		req.Header.Set("User-Agent", agent)
		req.Header.Set("Accept-Language", "ko-KR")
		req.Header.Set("Accept", "application/vnd.wap.connectivity-xml, application/xml, text/xml")
		resp, err := client.Do(req)
		if err != nil {
			return nil, 0, "", errors.New("rcs-network")
		}
		defer resp.Body.Close()
		retry := time.Duration(0)
		if seconds, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && seconds > 0 {
			retry = time.Duration(seconds) * time.Second
		}
		if resp.StatusCode != 200 && resp.StatusCode != 511 {
			return nil, resp.StatusCode, "", &rcsFailure{"rcs-http-rejected", resp.StatusCode, retry}
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxRCSConfig+1))
		if err != nil || len(data) > maxRCSConfig {
			return nil, resp.StatusCode, "", errors.New("rcs-config-invalid")
		}
		return data, resp.StatusCode, strings.ToLower(strings.Split(resp.Header.Get("Content-Type"), ";")[0]), nil
	}
	bootstrap := *base
	bootstrap.Scheme = "http"
	bootstrap.Host = base.Hostname()
	bootstrap.Path = "/"
	if _, _, _, err := request(&bootstrap); err != nil {
		return nil, err
	}
	target := *base
	target.RawQuery = params.Encode()
	for exchange := 0; exchange < 3; exchange++ {
		data, status, kind, err := request(&target)
		if err != nil {
			return nil, err
		}
		if status == 511 {
			return nil, &rcsFailure{"rcs-auth-required", 511, 0}
		}
		isXML := kind == "application/vnd.wap.connectivity-xml" || kind == "text/vnd.wap.connectivity-xml" || kind == "application/xml" || kind == "text/xml"
		if isXML && len(data) > 0 {
			return data, nil
		}
		if len(data) != 0 {
			return nil, errors.New("rcs-auth-unsupported")
		}
		if exchange != 0 || len(client.Jar.Cookies(base)) == 0 {
			return nil, errors.New("rcs-otp-invalid")
		}
		code, err := otp(ctx)
		if err != nil {
			return nil, err
		}
		if code == "" || len(code) > 64 || strings.ContainsAny(code, "\r\n\x00") {
			return nil, errors.New("rcs-otp-invalid")
		}
		target = *base
		target.RawQuery = url.Values{"OTP": []string{code}}.Encode()
	}
	return nil, errors.New("rcs-exchange-limit")
}

func rcsOTPText(message imssms.Incoming) string {
	data := message.UserData
	if len(data) >= len(message.UserDataHeader) && bytes.HasPrefix(data, message.UserDataHeader) {
		data = data[len(message.UserDataHeader):]
	}
	var candidates []string
	if message.Text != "" {
		candidates = append(candidates, message.Text)
	}
	if len(data) > 0 && len(data) <= 130 && len(data)%2 == 0 {
		words := make([]uint16, len(data)/2)
		order := binary.BigEndian
		for i := range words {
			words[i] = order.Uint16(data[i*2:])
		}
		if len(words) > 0 && words[0] == 0xfeff {
			words = words[1:]
		}
		candidates = append(candidates, string(utf16.Decode(words)))
	}
	// Some deployed TTA port-SMS PDUs carry ASCII despite UCS-2 DCS.
	candidates = append(candidates, string(data))
	for _, value := range candidates {
		if len(value) < 1 || len(value) > 64 || strings.Contains(value, "-rcscfg") {
			continue
		}
		valid := true
		for _, c := range value {
			if c < 33 || c > 126 {
				valid = false
			}
		}
		if valid {
			return value
		}
	}
	return ""
}

func waitRCSOTP(ctx context.Context, db *messageDB, since time.Time, verify func(context.Context) error) (string, error) {
	ctx, stop := context.WithTimeout(ctx, 120*time.Second)
	defer stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := verify(ctx); err != nil {
			return "", errors.New("sim-unavailable")
		}
		rows, err := db.run("SELECT metadata_json FROM sms_segments WHERE subscription_id="+sqlText(db.subscription)+" AND destination_port=37273 AND received_at>="+sqlText(since.UTC().Format(databaseTime))+" ORDER BY received_at DESC LIMIT 8;", true)
		if err != nil {
			return "", err
		}
		for _, row := range rows {
			var m imssms.Incoming
			if json.Unmarshal([]byte(row["metadata_json"].(string)), &m) == nil {
				if code := rcsOTPText(m); code != "" {
					return code, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", errors.New("rcs-otp-timeout")
		case <-ticker.C:
		}
	}
}

func writePrivate(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".rcs-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func startRCS(ctx context.Context, db *messageDB, scope, plmn, imsi, imei, number, private, public, session string, provider security.AKAProvider, verify func(context.Context) error) *rcsService {
	r := &rcsService{state: "configuring", retry: make(chan struct{}, 1)}
	go r.runQueue(ctx, db, session, verify)
	go r.runReceipts(ctx, db, verify)
	go func() {
		cachePath := filepath.Join(simStateDirectory, scope, "rcs.json")
		var cached rcsCache
		if info, e := os.Lstat(cachePath); e == nil && info.Mode().IsRegular() && info.Mode().Perm() == 0600 && info.Size() < 2*maxRCSConfig {
			if data, e := os.ReadFile(cachePath); e == nil {
				_ = json.Unmarshal(data, &cached)
			}
		}
		if cached.PLMN != plmn {
			cached = rcsCache{}
		}
		forceFetch := false
		for {
			if ctx.Err() != nil {
				return
			}
			if !rcsEnabled() {
				r.set("disabled", 0, 0)
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
					continue
				}
			}
			// A permanent operator deprovisioning survives a process restart.
			// A different SIM has a different scope and never inherits this marker.
			if doc, _ := parseRCSConfig(cached.XML); rcsConfigVersion(doc) == -1 {
				r.set("disabled-by-carrier", 0, -1)
				select {
				case <-ctx.Done():
					return
				case <-r.retry:
					continue
				}
			}
			server, operator, err := rcsACS(plmn)
			if err != nil {
				r.set("unsupported-carrier", 0, 0)
				return
			}
			if !strings.HasPrefix(number, "+82") || !validPhone(number) {
				r.set("number-required", 0, 0)
				return
			}
			r.set("configuring", 0, 0)
			params := url.Values{"provisioning_version": {"2.0"}, "default_sms_app": {"1"}, "vers": {"0"}, "rcs_state": {"0"}, "rcs_version": {"6.0"}, "rcs_profile": {"UP_1.0"},
				"client_vendor": {"KO"}, "client_version": {"OpenWrt-1.0"}, "terminal_vendor": {"A8"}, "terminal_model": {"A8"}, "terminal_sw_version": {"25.12.6"}, "IMSI": {imsi}, "IMEI": {imei}, "msisdn": {number}, "SMS_port": {"37273"}, "token": {""}}
			client, err := rcsHTTPClient()
			var data []byte
			fresh := false
			cachedDoc, _ := parseRCSConfig(cached.XML)
			if rcsConfigVersion(cachedDoc) > 0 {
				if _, e := parseRCSProfile(cachedDoc, private, public); e != nil {
					cachedDoc, cached = nil, rcsCache{}
				}
			}
			if rcsConfigVersion(cachedDoc) > 0 && time.Now().Unix() >= cached.Fetched && time.Now().Before(rcsExpiry(cachedDoc, cached.Fetched)) && !forceFetch {
				data = cached.XML
			} else if err == nil {
				fresh = true
				if cachedDoc != nil {
					if version := rcsConfigVersion(cachedDoc); version > 0 {
						value := strconv.Itoa(version)
						params.Set("vers", value)
						params.Set("rcs_state", value)
					}
					if tokens := rcsNodes(cachedDoc.Nodes, "TOKEN"); len(tokens) == 1 {
						params.Set("token", tokens[0].value("token"))
					}
				}
				since := time.Now()
				requestCtx, stop := context.WithTimeout(ctx, 180*time.Second)
				if err = verify(requestCtx); err == nil {
					data, err = fetchRCS(requestCtx, client, server, "TTA-RCS/1.0 A8/OpenWrt25.12.6 Device_Type/RCS_OpenWrt_Router "+operator, params, func(ctx context.Context) (string, error) {
						r.set("verifying-sms", 200, 0)
						return waitRCSOTP(ctx, db, since, verify)
					})
				}
				stop()
			}
			if client != nil {
				client.CloseIdleConnections()
			}
			forceFetch = false
			version := 0
			status := 0
			state := "rcs-network"
			retry := time.Duration(0)
			if err == nil {
				var doc *rcsDocument
				doc, err = parseRCSConfig(data)
				if err == nil {
					version, _ = strconv.Atoi(rcsNodes(doc.Nodes, "VERS")[0].value("version"))
					state = "disabled-by-carrier"
					if verify(ctx) == nil {
						if fresh {
							doc, err = rcsConfigUpdate(cachedDoc, doc, private, public)
							if err == nil {
								data, err = xml.Marshal(doc)
							}
							if err == nil {
								updated := rcsCache{PLMN: plmn, Fetched: time.Now().Unix(), XML: data}
								if version <= 0 {
									cached = updated
								}
								cache, _ := json.Marshal(updated)
								err = writePrivate(cachePath, cache)
								if err != nil && version <= 0 {
									// A full disk must not preserve the old active profile.
									_ = os.Remove(cachePath)
								}
								if err == nil {
									cached = updated
								}
							}
						}
						if err == nil && version > 0 {
							state = "configured"
						}
					} else {
						err = errors.New("sim-unavailable")
					}
				}
				if err != nil {
					state = "rcs-config-invalid"
				}
			} else {
				var f *rcsFailure
				if errors.As(err, &f) {
					state, status, retry = f.state, f.status, f.retry
					if status == 403 || status == 511 {
						cached = rcsCache{}
						if e := os.Remove(cachePath); e != nil && !os.IsNotExist(e) {
							state = "rcs-cache-invalid"
						}
					}
				} else {
					for _, code := range []string{"rcs-otp-timeout", "rcs-otp-invalid", "rcs-config-invalid", "rcs-auth-unsupported", "sim-unavailable"} {
						if err.Error() == code {
							state = code
						}
					}
				}
			}
			r.set(state, status, version)
			if state == "configured" {
				doc, _ := parseRCSConfig(cached.XML)
				profile, e := parseRCSProfile(doc, private, public)
				if e == nil {
					r.mu.Lock()
					r.auth, r.transport, r.chat = profile.auth, profile.transport, profile.chat
					r.mu.Unlock()
					e = r.connected(ctx, profile, provider, "TTA-RCS/1.0 A8/OpenWrt25.12.6 Device_Type/RCS_OpenWrt_Router "+operator, scope, db, verify, rcsExpiry(doc, cached.Fetched))
				}
				if ctx.Err() != nil {
					return
				}
				if e == nil {
					forceFetch = true
					continue
				}
				state = "rcs-profile-invalid"
				if strings.HasPrefix(e.Error(), "rcs-") {
					state = e.Error()
				}
				var f *rcsSIPFailure
				if errors.As(e, &f) {
					r.mu.Lock()
					r.sipStatus = f.status
					r.mu.Unlock()
				}
				r.set(state, 0, version)
			}
			// Authentication failures wait for an explicit retry, preventing a
			// rejected client from repeatedly requesting verification messages.
			var after <-chan time.Time
			var timer *time.Timer
			if state == "disabled" {
				retry = 5 * time.Second
				timer = time.NewTimer(retry)
				after = timer.C
			} else if state == "rcs-network" || state == "rcs-sip-network" || state == "rcs-sip-dns" || state == "rcs-sip-connect" || state == "rcs-sip-timeout" || status == 429 || status >= 500 {
				if retry < time.Minute {
					retry = time.Minute
				}
				timer = time.NewTimer(retry)
				after = timer.C
			}
			select {
			case <-ctx.Done():
				if timer != nil {
					timer.Stop()
				}
				return
			case <-r.retry:
				forceFetch = true
			case <-after:
			}
			if timer != nil {
				timer.Stop()
			}
		}
	}()
	return r
}

func rcsConfigVersion(doc *rcsDocument) int {
	if doc == nil {
		return 0
	}
	nodes := rcsNodes(doc.Nodes, "VERS")
	if len(nodes) != 1 {
		return 0
	}
	version, _ := strconv.Atoi(nodes[0].value("version"))
	return version
}
func rcsExpiry(doc *rcsDocument, fetched int64) time.Time {
	seconds := 300
	if doc != nil {
		n := rcsNodes(doc.Nodes, "VERS")
		if len(n) == 1 {
			if v, e := strconv.Atoi(n[0].value("validity")); e == nil && v > 0 && v <= 30*24*3600 {
				seconds = v
			}
		}
	}
	return time.Unix(fetched, 0).Add(time.Duration(seconds) * time.Second)
}
func mergeRCSConfig(old, fresh *rcsDocument) *rcsDocument {
	result := *old
	result.Nodes = append([]rcsNode(nil), old.Nodes...)
	for _, node := range fresh.Nodes {
		found := false
		for i := range result.Nodes {
			if strings.EqualFold(result.Nodes[i].Type, node.Type) {
				result.Nodes[i] = node
				found = true
				break
			}
		}
		if !found {
			result.Nodes = append(result.Nodes, node)
		}
	}
	return &result
}

func rcsConfigUpdate(previous, fresh *rcsDocument, private, public string) (*rcsDocument, error) {
	if fresh == nil {
		return nil, errors.New("rcs-config-invalid")
	}
	version := rcsConfigVersion(fresh)
	if version <= 0 {
		// Keep the operator's disable marker, never its obsolete credentials.
		return &rcsDocument{XMLName: fresh.XMLName, Nodes: rcsNodes(fresh.Nodes, "VERS")}, nil
	}
	if previous != nil && version == rcsConfigVersion(previous) && len(rcsNodes(fresh.Nodes, "APPLICATION")) == 0 {
		fresh = mergeRCSConfig(previous, fresh)
	}
	if _, err := parseRCSProfile(fresh, private, public); err != nil {
		return nil, err
	}
	return fresh, nil
}

func (r *rcsService) connected(ctx context.Context, profile *rcsProfile, provider security.AKAProvider, agent, scope string, db *messageDB, verify func(context.Context) error, expiry time.Time) error {
	connection, stop := context.WithCancel(ctx)
	defer stop()
	r.set("registering", 0, r.version)
	client, err := openRCSSIP(connection, profile, provider, agent, scope, db, verify)
	if err != nil {
		return err
	}
	defer client.close()
	register := func() (time.Duration, error) {
		c, cancel := context.WithTimeout(connection, 30*time.Second)
		defer cancel()
		return client.register(c, 3600)
	}
	duration, err := register()
	if err != nil {
		return err
	}
	publication := &rcsPublication{}
	defer func() {
		select {
		case <-client.done:
			return
		default:
		}
		// Remove advertised capabilities before the RCS registration ends.
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if verify(cleanup) != nil {
			return
		}
		if publication.etag != "" {
			_, _ = publication.request(cleanup, client, true)
		}
		_, _ = client.register(cleanup, 0)
	}()
	r.mu.Lock()
	r.client, r.sipStatus = client, 200
	r.discovery, r.publishStatus, r.publishUntil = "options", 0, time.Time{}
	if profile.presence {
		r.discovery = "presence"
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.client == client {
			r.client = nil
			r.state, r.updated = "configuring", time.Now().Unix()
		}
		r.mu.Unlock()
	}()
	var publishTimer *time.Timer
	var publish <-chan time.Time
	if profile.presence {
		r.set("publishing", 0, r.version)
		publishTimer = time.NewTimer(0)
		publish = publishTimer.C
		defer publishTimer.Stop()
	} else {
		r.set("registered", 0, r.version)
	}
	refresh := time.NewTimer(duration * 4 / 5)
	defer refresh.Stop()
	validity := time.NewTimer(time.Until(expiry))
	defer validity.Stop()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.retry:
			return nil
		case <-validity.C:
			return nil
		case <-client.done:
			return errors.New("rcs-sip-network")
		case <-publish:
			update, cancel := context.WithTimeout(connection, 8*time.Second)
			publication.update(update, client, time.Now())
			cancel()
			r.mu.Lock()
			r.publishStatus, r.publishUntil = publication.status, publication.until
			r.mu.Unlock()
			state := "publishing"
			if time.Now().Before(publication.until) {
				state = "registered"
			}
			r.set(state, 0, r.version)
			publishTimer.Reset(publication.delay(time.Now()))
		case <-keepalive.C:
			if !rcsEnabled() {
				return errors.New("disabled")
			}
			if err := client.keepalive(connection); err != nil {
				return errors.New("rcs-sip-network")
			}
		case <-refresh.C:
			duration, err = register()
			if err != nil {
				return err
			}
			refresh.Reset(duration * 4 / 5)
		}
	}
}
