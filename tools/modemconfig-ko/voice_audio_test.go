// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/damonto/ims-go/ims/voice"
)

func TestVoiceOverloadKeepsLatestMicrophoneFrames(t *testing.T) {
	v := &voiceService{input: make(chan []int16, 3)}
	data := make([]byte, 7*640)
	for frame := 0; frame < 7; frame++ {
		binary.LittleEndian.PutUint16(data[frame*640:], uint16(frame*1000))
	}
	v.queueInputPCM(data)
	if v.inputFrames != 7 || v.inputDropped != 4 || v.inputVoiced != 6 || v.inputPeak != 6000 || len(v.input) != 3 {
		t.Fatal("audio overload lost counters or did not bound the queue")
	}
	for _, want := range []int16{4000, 5000, 6000} {
		if pcm := <-v.input; pcm[0] != want {
			t.Fatal("stale microphone audio displaced the latest input")
		}
	}
	data = make([]byte, 640)
	binary.LittleEndian.PutUint16(data, 0x8000)
	v.queueInputPCM(data)
	if v.inputPeak != 32768 || v.inputVoiced != 7 || (<-v.input)[0] != -32768 {
		t.Fatal("negative microphone peak overflowed")
	}
}

func TestVoicePacketDurationMatchesAMRFrames(t *testing.T) {
	for _, ms := range []int{0, 20, 40, 60, 120, 240} {
		media := voice.NegotiatedMedia{AudioFormat: voice.AudioFormat{Codec: voice.CodecAMRWB, ClockRate: 16000, OctetAlign: true, ModeSet: "0,1,2"}, PTime: time.Duration(ms) * time.Millisecond}
		count, err := voicePacketFrames(media)
		if err != nil {
			t.Fatal(err)
		}
		codec, err := newVoicePCM(media)
		if err != nil {
			t.Fatal(err)
		}
		var frames []voice.AMRFrame
		for i := 0; i < count; i++ {
			frame, err := codec.encode(make([]int16, 320))
			if err != nil {
				t.Fatal(err)
			}
			if frame.FrameType != 0 {
				t.Fatal("WB encoder exceeded A8 real-time mode budget")
			}
			frames = append(frames, frame)
		}
		payload, err := (voice.AMRPayloadCodec{Media: media}).Marshal(voice.AMRPayload{CMR: 15, Frames: frames})
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := (voice.AMRPayloadCodec{Media: media}).Unmarshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		duration := max(ms, 20)
		if len(decoded.Frames)*20 != duration {
			t.Fatalf("packet has %d ms audio for %d ms RTP timestamp", len(decoded.Frames)*20, duration)
		}
	}
	for _, ms := range []int{-20, 10, 30, 260} {
		if _, err := voicePacketFrames(voice.NegotiatedMedia{PTime: time.Duration(ms) * time.Millisecond}); err == nil {
			t.Fatal("invalid AMR packet duration accepted")
		}
	}
}

func TestVoiceWBRespectsNegotiatedModeSet(t *testing.T) {
	for _, test := range []struct {
		modes string
		want  int
	}{{"0,1,2", 0}, {"2,1", 1}, {"2", 2}} {
		codec, err := newVoicePCM(voice.NegotiatedMedia{AudioFormat: voice.AudioFormat{Codec: voice.CodecAMRWB, ModeSet: test.modes}})
		if err != nil {
			t.Fatal(err)
		}
		frame, err := codec.encode(make([]int16, 320))
		if err != nil || frame.FrameType != test.want {
			t.Fatalf("modes %s: frame mode %d, error %v", test.modes, frame.FrameType, err)
		}
	}
}
