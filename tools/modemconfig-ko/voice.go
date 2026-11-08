// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	ims "github.com/damonto/ims-go"
	"github.com/damonto/ims-go/ims/voice"
	"github.com/damonto/ims-go/ims/voice/codec/amr/amrnb"
	"github.com/damonto/ims-go/ims/voice/codec/amr/amrwb"
)

// Audio endpoints exchange mono PCM16 at 16 kHz. Only negotiated AMR frames
// cross the LTE IMS bearer; microphone audio is never saved to disk.
type voiceService struct {
	mu                                                   sync.Mutex
	client                                               *ims.Client
	verify                                               func(context.Context) error
	ctx                                                  context.Context
	call                                                 *voice.Call
	id, peer, direction, state, failure                  string
	busy, mediaStarted                                   bool
	input                                                chan []int16
	output                                               []int16
	uplink                                               int64
	tx, rx                                               uint64
	inputFrames, inputVoiced, inputDropped, inputMissing uint64
	inputPeak                                            int
	dialCancel                                           context.CancelFunc
	lastAudio                                            time.Time
	bluetooth                                            *bluetoothAudio
	audio                                                string
	headset                                              bluetoothPCM
	owner                                                voiceOwner
	startedAt, connectedAt, endedAt                      time.Time
	endReason, detail                                    string
	db                                                   *messageDB
	historyMu                                            sync.Mutex
	historySaved                                         string
}

func koreanVoiceConfig() voice.Config {
	return voice.Config{
		Codecs: []voice.AudioCodecConfig{
			{Name: voice.CodecAMRWB, PayloadTypes: []int{96}, ClockRate: 16000, Channels: 1, OctetAlign: true, ModeSet: "0,1,2"},
			{Name: voice.CodecAMR, PayloadTypes: []int{97}, ClockRate: 8000, Channels: 1, OctetAlign: true, ModeSet: "0,1,2,3,4,5,6,7"},
			{Name: voice.CodecTelephoneEvent, PayloadTypes: []int{101}, ClockRate: 16000, Channels: 1},
			{Name: voice.CodecTelephoneEvent, PayloadTypes: []int{102}, ClockRate: 8000, Channels: 1},
		},
		RequestURIFormat:    voice.RequestURIFormatTEL,
		ReliableProvisional: true, SessionTimer: true,
		PTime: 20 * time.Millisecond, MaxPTime: 240 * time.Millisecond,
		MOCallRequestTimeout: 45 * time.Second, RingingTimeout: 60 * time.Second,
	}
}

func startVoice(ctx context.Context, client *ims.Client, verify func(context.Context) error, db *messageDB) (*voiceService, func()) {
	v := &voiceService{ctx: ctx, client: client, verify: verify, db: db, state: "idle", audio: "browser", bluetooth: startBluetoothAudio(ctx)}
	events := client.Voice().Events()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		defer os.Remove(voiceDisplayPath)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				v.expireOwner(time.Now())
				v.publishDisplay()
			case event, ok := <-events.Incoming:
				if !ok {
					return
				}
				v.mu.Lock()
				if v.busy || v.call != nil && !callFinished(v.call.State()) {
					v.mu.Unlock()
					go func() {
						c, stop := context.WithTimeout(ctx, 5*time.Second)
						defer stop()
						_ = event.Call.Reject(c, 486, "Busy Here")
					}()
					continue
				}
				v.assign(event.Call, event.FromNumber, "incoming")
				v.mu.Unlock()
				v.publishDisplay()
			case event, ok := <-events.Events:
				if !ok {
					return
				}
				v.mu.Lock()
				if v.busy && v.call == nil && event.State == voice.CallStateDialing && event.Call.Direction() == voice.CallDirectionOutgoing {
					v.call = event.Call
				}
				if v.call == event.Call {
					v.trackState(string(event.State), time.Now())
					if event.State == voice.CallStateFailed {
						v.failure = "call-failed"
						v.detail = voiceFailure(errors.New(event.Cause))
					}
					traceVoiceState(string(event.State), event.Cause)
					v.startMediaLocked()
				}
				cancelPending := v.call == event.Call && v.owner.releasing && !callFinished(event.Call.State())
				id := v.id
				v.mu.Unlock()
				if cancelPending {
					go v.releaseCall(event.Call, id)
				}
				v.publishDisplay()
			}
		}
	}()
	return v, func() { events.Close(); <-done }
}

func callFinished(state voice.CallState) bool {
	return state == voice.CallStateEnded || state == voice.CallStateFailed
}

func (v *voiceService) assign(call *voice.Call, peer, direction string) {
	v.call, v.id, v.peer, v.direction = call, opaqueID(), peer, direction
	v.state, v.failure, v.mediaStarted, v.uplink = string(call.State()), "", false, 0
	v.startedAt, v.connectedAt, v.endedAt = time.Now(), time.Time{}, time.Time{}
	v.endReason, v.detail = "", ""
	v.owner = voiceOwner{}
	v.trackState(v.state, time.Now())
	v.input, v.output, v.tx, v.rx = make(chan []int16, 15), nil, 0, 0
	v.inputFrames, v.inputVoiced, v.inputDropped, v.inputMissing, v.inputPeak = 0, 0, 0, 0, 0
	v.lastAudio = time.Now()
	v.audio = "browser"
	v.startMediaLocked()
}

func (v *voiceService) startMediaLocked() {
	if v.call == nil || v.mediaStarted || !v.call.Media().Negotiated {
		return
	}
	switch v.call.State() {
	case voice.CallStateEarlyMedia, voice.CallStateConfirmed, voice.CallStateActive:
		v.mediaStarted = true
		v.lastAudio = time.Now()
		go v.pump(v.call, v.id, v.input)
	}
}

func (v *voiceService) snapshot(owner string) map[string]any {
	v.mu.Lock()
	defer v.mu.Unlock()
	ready := v.client.Registration().Registered
	state := v.state
	codec := ""
	if v.call != nil {
		state = v.callState()
		v.trackState(state, time.Now())
		codec = string(v.call.Media().Codec)
	}
	return map[string]any{"ready": ready, "id": v.id, "peer": v.peer, "direction": v.direction, "state": state, "audio": v.audio, "bluetooth": v.bluetooth.snapshot(),
		"codec": codec, "sample_rate": 16000, "tx_packets": v.tx, "rx_packets": v.rx, "error": v.failure,
		"input_frames": v.inputFrames, "input_voiced": v.inputVoiced, "input_dropped": v.inputDropped, "input_missing": v.inputMissing, "input_peak": v.inputPeak, "input_queue": len(v.input),
		"owned": v.owner.matches(owner), "occupied": v.owner.token != "", "duration": v.duration(time.Now()), "detail": v.detail, "end_reason": v.endReason,
		"lease_seconds": max(0, int64(time.Until(v.owner.expires)/time.Second)), "trace": snapshotVoiceTrace()}
}

func (v *voiceService) action(ctx context.Context, method string, r messageRequest) (any, error) {
	if method == "voice_status" {
		// Status is read-only. Only the explicit lease heartbeat keeps a call alive.
		return v.snapshot(r.Owner), nil
	}
	if method == "voice_release" {
		reason := "hangup"
		if r.Reason == "page-left" {
			reason = r.Reason
		}
		return v.releaseOwner(r.Owner, reason)
	}
	if method == "voice_hangup" {
		v.mu.Lock()
		same := r.ID == v.id
		v.mu.Unlock()
		if !same {
			return nil, errors.New("call-unavailable")
		}
		return v.releaseOwner(r.Owner, "hangup")
	}
	if v.ctx.Err() != nil || !v.client.Registration().Registered {
		return nil, errors.New("sim-unavailable")
	}
	if method != "voice_audio" && method != "voice_lease" {
		if err := v.verify(ctx); err != nil {
			return nil, errors.New("sim-unavailable")
		}
	}
	v.mu.Lock()
	if method == "voice_claim" {
		err := v.owner.claim(r.Owner, time.Now())
		v.mu.Unlock()
		return map[string]any{"owned": err == nil}, err
	}
	if !v.owner.matches(r.Owner) || !v.owner.alive(time.Now()) || v.owner.releasing {
		v.mu.Unlock()
		return nil, errors.New("call-in-use")
	}
	if method == "voice_lease" {
		v.owner.expires = time.Now().Add(voiceLeaseDuration)
		v.mu.Unlock()
		return map[string]any{"owned": true}, nil
	}
	v.mu.Unlock()
	if method == "voice_dial" {
		audio, headset, err := v.audioDevice(r)
		if err != nil {
			return nil, err
		}
		number := canonicalPeer(r.To)
		if !validPhone(number) || len(number) < 9 {
			return nil, errors.New("invalid-number")
		}
		v.mu.Lock()
		if !v.owner.matches(r.Owner) || !v.owner.alive(time.Now()) {
			v.mu.Unlock()
			return nil, errors.New("call-in-use")
		}
		if v.busy || v.call != nil && !callFinished(v.call.State()) {
			v.mu.Unlock()
			return nil, errors.New("busy")
		}
		v.busy, v.state, v.failure = true, "dialing", ""
		v.call, v.id, v.peer, v.direction, v.mediaStarted = nil, opaqueID(), number, "outgoing", false
		v.startedAt, v.connectedAt, v.endedAt = time.Now(), time.Time{}, time.Time{}
		v.endReason, v.detail = "", ""
		v.input, v.output, v.uplink = make(chan []int16, 15), nil, 0
		v.tx, v.rx, v.lastAudio = 0, 0, time.Now()
		v.inputFrames, v.inputVoiced, v.inputDropped, v.inputMissing, v.inputPeak = 0, 0, 0, 0, 0
		v.audio, v.headset = audio, headset
		// The SIP layer has separate request and ringing timers. A single outer
		// deadline must not cut a normally ringing call short.
		callCtx, stop := context.WithCancel(v.ctx)
		v.dialCancel = stop
		id := v.id
		v.mu.Unlock()
		if err := v.persistHistory(); err != nil {
			stop()
			v.mu.Lock()
			v.busy = false
			v.dialCancel = nil
			v.detail = "history-unavailable"
			v.trackState("failed", time.Now())
			v.mu.Unlock()
			return nil, errors.New("call-history-unavailable")
		}
		// Dial waits for final SIP response. Returning immediately keeps LuCI's
		// authenticated RPC available for cancel, ringing and media polling.
		go func() {
			defer stop()
			v.mu.Lock()
			if v.owner.releasing {
				v.busy = false
				v.dialCancel = nil
				v.trackState("ended", time.Now())
				v.mu.Unlock()
				return
			}
			v.mu.Unlock()
			call, err := v.client.Voice().Dial(callCtx, voice.DialRequest{To: number})
			v.mu.Lock()
			defer v.mu.Unlock()
			if v.id != id {
				return
			}
			v.busy = false
			v.dialCancel = nil
			if err != nil {
				if v.endReason != "" {
					if v.call == nil || callFinished(v.call.State()) {
						v.trackState("ended", time.Now())
					}
				} else {
					v.trackState("failed", time.Now())
					v.failure, v.detail = "call-failed", voiceFailure(err)
				}
				traceVoiceState(v.state, v.detail)
				return
			}
			v.call = call
			v.trackState(string(call.State()), time.Now())
			v.startMediaLocked()
		}()
		return map[string]any{"state": "dialing"}, nil
	}
	v.mu.Lock()
	if !v.owner.matches(r.Owner) || !v.owner.alive(time.Now()) {
		v.mu.Unlock()
		return nil, errors.New("call-in-use")
	}
	call := v.call
	if call == nil || r.ID != v.id || callFinished(call.State()) {
		v.mu.Unlock()
		return nil, errors.New("call-unavailable")
	}
	if method == "voice_audio" {
		if v.audio != "browser" {
			v.mu.Unlock()
			return nil, errors.New("audio-unavailable")
		}
		v.lastAudio = time.Now()
		data, err := base64.StdEncoding.DecodeString(r.Data)
		if err != nil || len(data) > 6400 || len(data)%640 != 0 || r.Sequence < 0 {
			v.mu.Unlock()
			return nil, errors.New("invalid-audio")
		}
		if len(data) != 0 && r.Sequence > v.uplink {
			v.queueInputPCM(data)
			v.uplink = r.Sequence
		}
		output := make([]byte, len(v.output)*2)
		for i, sample := range v.output {
			binary.LittleEndian.PutUint16(output[i*2:], uint16(sample))
		}
		v.output = nil
		v.mu.Unlock()
		return map[string]any{"data": base64.StdEncoding.EncodeToString(output), "sample_rate": 16000}, nil
	}
	v.mu.Unlock()
	switch method {
	case "voice_answer":
		audio, headset, err := v.audioDevice(r)
		if err != nil {
			return nil, err
		}
		if call.Direction() != voice.CallDirectionIncoming || call.State() != voice.CallStateRinging {
			return nil, errors.New("call-unavailable")
		}
		v.mu.Lock()
		if !v.owner.matches(r.Owner) || !v.owner.alive(time.Now()) {
			v.mu.Unlock()
			return nil, errors.New("call-in-use")
		}
		v.audio, v.headset, v.lastAudio = audio, headset, time.Now()
		v.mu.Unlock()
		if err := call.Answer(ctx, voice.MediaOffer{}); err != nil {
			v.mu.Lock()
			if v.call == call && v.id == r.ID {
				v.detail = voiceFailure(err)
			}
			v.mu.Unlock()
			v.audioFailed(call, r.ID, "call-failed")
			return nil, errors.New("call-failed")
		}
	default:
		return nil, errors.New("invalid-request")
	}
	return v.snapshot(r.Owner), nil
}

// Called with v.mu held. Live audio keeps the newest input when encoding lags.
func (v *voiceService) queueInputPCM(data []byte) {
	for start := 0; start < len(data); start += 640 {
		pcm := make([]int16, 320)
		peak := 0
		for i := range pcm {
			pcm[i] = int16(binary.LittleEndian.Uint16(data[start+i*2:]))
			value := int(pcm[i])
			if value < 0 {
				value = -value
			}
			peak = max(peak, value)
		}
		v.inputFrames++
		if peak > 64 {
			v.inputVoiced++
		}
		v.inputPeak = max(v.inputPeak, peak)
		select {
		case v.input <- pcm:
		default:
			select {
			case <-v.input:
				v.inputDropped++
			default:
			}
			v.input <- pcm
		}
	}
}

func (v *voiceService) audioDevice(r messageRequest) (string, bluetoothPCM, error) {
	if r.Audio == "" || r.Audio == "browser" {
		return "browser", bluetoothPCM{}, nil
	}
	if r.Audio != "bluetooth" {
		return "", bluetoothPCM{}, errors.New("invalid-request")
	}
	device, err := v.bluetooth.selectDevice(r.Device)
	return "bluetooth", device, err
}

// Codec state belongs to one call and one direction. No audio is saved to disk.
type voicePCM struct {
	wide bool
	nbe  *amrnb.Encoder
	nbd  *amrnb.Decoder
	wbe  *amrwb.Encoder
	wbd  *amrwb.Decoder
}

func newVoicePCM(media voice.NegotiatedMedia) (*voicePCM, error) {
	c := &voicePCM{wide: media.Codec == voice.CodecAMRWB}
	mode := 7
	if c.wide {
		// On A8 the pure-Go WB mode 2 encoder exceeds its 20 ms deadline.
		// Use the lowest negotiated WB mode; keep all advertised modes for RX.
		mode = 0
	}
	if media.ModeSet != "" {
		mode = -1
		for _, s := range strings.Split(media.ModeSet, ",") {
			m, e := strconv.Atoi(strings.TrimSpace(s))
			if e == nil && m >= 0 && m <= 7 && (!c.wide || m <= 2) && (mode < 0 || c.wide && m < mode || !c.wide && m > mode) {
				mode = m
			}
		}
		if mode < 0 {
			return nil, errors.New("unsupported-codec")
		}
	}
	if c.wide {
		c.wbe, _ = amrwb.NewEncoder(amrwb.Config{Mode: amrwb.Mode(mode)})
		c.wbd = amrwb.NewDecoder()
	} else if media.Codec == voice.CodecAMR {
		c.nbe, _ = amrnb.NewEncoder(amrnb.Config{Mode: amrnb.Mode(mode)})
		c.nbd = amrnb.NewDecoder()
	} else {
		return nil, errors.New("unsupported-codec")
	}
	return c, nil
}

func voicePacketFrames(media voice.NegotiatedMedia) (int, error) {
	ptime := media.PTime
	if ptime == 0 {
		ptime = 20 * time.Millisecond
	}
	if ptime < 20*time.Millisecond || ptime > 240*time.Millisecond || ptime%(20*time.Millisecond) != 0 {
		return 0, errors.New("unsupported-audio-packet-time")
	}
	return int(ptime / (20 * time.Millisecond)), nil
}

func (c *voicePCM) encode(pcm []int16) (voice.AMRFrame, error) {
	if len(pcm) != 320 {
		return voice.AMRFrame{}, errors.New("invalid-audio")
	}
	if c.wide {
		f, e := c.wbe.Encode(nil, pcm)
		return voice.AMRFrame{FrameType: int(f.Mode), Quality: f.Quality, Data: f.Data}, e
	}
	small := make([]int16, 160)
	for i := range small {
		small[i] = int16((int32(pcm[i*2]) + int32(pcm[i*2+1])) / 2)
	}
	f, e := c.nbe.Encode(nil, small)
	return voice.AMRFrame{FrameType: int(f.Mode), Quality: f.Quality, Data: f.Data}, e
}

func (c *voicePCM) decode(frame voice.AMRFrame) ([]int16, error) {
	if c.wide {
		return c.wbd.Decode(nil, amrwb.Frame{Mode: amrwb.Mode(frame.FrameType), Quality: frame.Quality, Data: frame.Data})
	}
	small, err := c.nbd.Decode(nil, amrnb.Frame{Mode: amrnb.Mode(frame.FrameType), Quality: frame.Quality, Data: frame.Data})
	if err != nil {
		return nil, err
	}
	pcm := make([]int16, len(small)*2)
	for i, s := range small {
		pcm[i*2], pcm[i*2+1] = s, s
	}
	return pcm, nil
}

func (v *voiceService) pump(call *voice.Call, id string, input <-chan []int16) {
	media := call.Media()
	packetFrames, err := voicePacketFrames(media)
	if err != nil {
		v.audioFailed(call, id, "audio-format-unavailable")
		return
	}
	codec, err := newVoicePCM(media)
	if err != nil {
		v.audioFailed(call, id, "audio-format-unavailable")
		return
	}
	addVoiceTrace(map[string]any{"stage": "audio", "codec": string(media.Codec), "packet_ms": packetFrames * 20,
		"payload_type": media.PayloadType, "remote_payload_type": media.RemoteFormat.PayloadType,
		"clock_rate": media.ClockRate, "octet_align": media.OctetAlign, "mode_set": media.ModeSet, "direction": string(media.Direction)})
	ctx, stop := context.WithCancel(v.ctx)
	defer stop()
	defer func() {
		v.mu.Lock()
		current := v.call == call && v.id == id && !v.owner.releasing
		v.mu.Unlock()
		if current && v.ctx.Err() == nil && !callFinished(call.State()) {
			v.audioFailed(call, id, "audio-disconnected")
		}
	}()
	v.mu.Lock()
	route, headset, inputQueue := v.audio, v.headset, v.input
	v.mu.Unlock()
	var bt *bluetoothStream
	if route == "bluetooth" {
		bt, err = openBluetoothStream(ctx, headset, inputQueue)
		if err != nil {
			v.audioFailed(call, id, "bluetooth-audio-failed")
			return
		}
		defer bt.close()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var last uint16
		haveLast := false
		for {
			data, _, err := call.ReadRTP(ctx)
			if err != nil {
				return
			}
			var packet voice.RTPPacket
			if packet.UnmarshalBinary(data) != nil || packet.PayloadType != media.RemoteFormat.PayloadType {
				continue
			}
			if haveLast && int16(packet.SequenceNumber-last) <= 0 {
				continue
			}
			last, haveLast = packet.SequenceNumber, true
			payload, err := (voice.AMRPayloadCodec{Media: media}).Unmarshal(packet.Payload)
			if err != nil {
				continue
			}
			for _, frame := range payload.Frames {
				pcm, err := codec.decode(frame)
				if err != nil {
					continue
				}
				v.mu.Lock()
				if v.id == id && v.call == call && !v.owner.releasing {
					if bt != nil {
						bt.play(pcm)
					} else {
						v.output = append(v.output, pcm...)
					}
					if len(v.output) > 16000 {
						v.output = v.output[len(v.output)-16000:]
					}
					v.rx++
				}
				v.mu.Unlock()
			}
		}
	}()
	defer func() { stop(); <-done }()
	ticker := time.NewTicker(time.Duration(packetFrames) * 20 * time.Millisecond)
	defer ticker.Stop()
	var sequence uint16
	var timestamp uint32
	for {
		v.mu.Lock()
		current := v.call == call && v.id == id && !v.owner.releasing
		v.mu.Unlock()
		if !current {
			return
		}
		if bt != nil {
			select {
			case <-bt.done:
				v.audioFailed(call, id, "bluetooth-disconnected")
				return
			default:
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
		}
		if callFinished(call.State()) {
			return
		}
		v.mu.Lock()
		expired := route == "browser" && v.id == id && !v.lastAudio.IsZero() && time.Since(v.lastAudio) > 20*time.Second
		v.mu.Unlock()
		if expired {
			c, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = call.Hangup(c)
			cancel()
			return
		}
		frames := make([]voice.AMRFrame, 0, packetFrames)
		missing := uint64(0)
		for i := 0; i < packetFrames; i++ {
			pcm := make([]int16, 320)
			select {
			case pcm = <-input:
			default:
				missing++
			}
			frame, err := codec.encode(pcm)
			if err != nil {
				return
			}
			frames = append(frames, frame)
		}
		payload, err := (voice.AMRPayloadCodec{Media: media}).Marshal(voice.AMRPayload{CMR: 15, Frames: frames})
		if err != nil {
			return
		}
		packet := voice.RTPPacket{PayloadType: media.PayloadType, SequenceNumber: sequence, Timestamp: timestamp, SSRC: 1, Payload: payload}
		data, err := packet.MarshalBinary()
		if err != nil {
			return
		}
		if err = call.WriteRTP(ctx, data); err != nil {
			return
		}
		sequence++
		timestamp += uint32(media.ClockRate * packetFrames / 50)
		v.mu.Lock()
		if v.id == id && v.call == call && !v.owner.releasing {
			v.tx++
			v.inputMissing += missing
		}
		v.mu.Unlock()
	}
}

func (v *voiceService) audioFailed(call *voice.Call, id, reason string) {
	v.mu.Lock()
	if v.id == id && v.call == call && !v.owner.releasing {
		v.failure = reason
		v.owner.releasing = true
		v.state = "ending"
	} else {
		v.mu.Unlock()
		return
	}
	v.mu.Unlock()
	v.releaseCall(call, id)
}
