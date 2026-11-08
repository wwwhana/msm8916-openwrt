// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const privateBlueALSADir = "/usr/lib/modemconfig-ko/bluealsa"

type bluetoothPCM struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	source, sink string
	rate         int
}
type bluetoothAudio struct {
	mu      sync.Mutex
	devices []bluetoothPCM
	state   string
}

func blueALSACommand(ctx context.Context, args ...string) *exec.Cmd {
	binary := "/usr/bin/bluealsa-cli"
	if _, err := os.Stat(privateBlueALSADir + "/bluealsa-cli"); err == nil {
		binary = privateBlueALSADir + "/bluealsa-cli"
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LD_LIBRARY_PATH="+privateBlueALSADir+"/lib")
	return cmd
}

var bluetoothPCMPath = regexp.MustCompile(`^/org/bluealsa/hci[0-9]+/dev_[0-9A-Fa-f_]{17}/[a-z0-9]+/(source|sink)$`)

func parseBluetoothPCM(path, info string) (bluetoothPCM, string, bool) {
	if !bluetoothPCMPath.MatchString(path) {
		return bluetoothPCM{}, "", false
	}
	fields := map[string]string{}
	for _, line := range strings.Split(info, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			fields[key] = strings.TrimSpace(value)
		}
	}
	if fields["Transport"] != "HFP-AG" && fields["Transport"] != "HSP-AG" {
		return bluetoothPCM{}, "", false
	}
	if fields["Format"] != "S16_LE" || fields["Channels"] != "1" {
		return bluetoothPCM{}, "", false
	}
	rateText := strings.Fields(fields["Sampling"])
	if len(rateText) == 0 {
		return bluetoothPCM{}, "", false
	}
	rate, err := strconv.Atoi(rateText[0])
	if err != nil || (rate != 8000 && rate != 16000) {
		return bluetoothPCM{}, "", false
	}
	mode := fields["Mode"]
	if mode != "source" && mode != "sink" {
		return bluetoothPCM{}, "", false
	}
	parts := strings.Split(path, "/")
	device := strings.Join(parts[:len(parts)-1], "/")
	address := strings.ReplaceAll(strings.TrimPrefix(parts[len(parts)-3], "dev_"), "_", ":")
	return bluetoothPCM{ID: stableID(device), Label: address, rate: rate}, mode, true
}

func startBluetoothAudio(ctx context.Context) *bluetoothAudio {
	b := &bluetoothAudio{state: "unavailable"}
	go func() {
		for {
			query, stop := context.WithTimeout(ctx, 4*time.Second)
			data, err := blueALSACommand(query, "list-pcms").Output()
			devices := map[string]bluetoothPCM{}
			if err == nil && len(data) < 16384 {
				for _, path := range strings.Fields(string(data)) {
					if !bluetoothPCMPath.MatchString(path) {
						continue
					}
					info, e := blueALSACommand(query, "info", path).Output()
					if e != nil || len(info) > 16384 {
						continue
					}
					pcm, mode, ok := parseBluetoothPCM(path, string(info))
					if !ok {
						continue
					}
					pair := devices[pcm.ID]
					if pair.ID == "" {
						pair = pcm
					}
					if pair.rate != pcm.rate {
						continue
					}
					if mode == "source" {
						pair.source = path
					} else {
						pair.sink = path
					}
					devices[pcm.ID] = pair
				}
			}
			stop()
			var list []bluetoothPCM
			for _, p := range devices {
				if p.source != "" && p.sink != "" {
					list = append(list, p)
				}
			}
			sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
			state := "no-headset"
			if err != nil {
				state = "unavailable"
			} else if len(list) > 0 {
				state = "ready"
			}
			b.mu.Lock()
			b.devices, b.state = list, state
			b.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}()
	return b
}

func (b *bluetoothAudio) snapshot() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	list := append([]bluetoothPCM{}, b.devices...)
	return map[string]any{"state": b.state, "devices": list}
}
func (b *bluetoothAudio) selectDevice(id string) (bluetoothPCM, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, device := range b.devices {
		if device.ID == id || (id == "" && len(b.devices) == 1) {
			return device, nil
		}
	}
	return bluetoothPCM{}, errors.New("bluetooth-unavailable")
}

type bluetoothStream struct {
	cancel        context.CancelFunc
	input, output io.Closer
	downlink      chan []int16
	done          chan struct{}
	commands      []*exec.Cmd
	once          sync.Once
}

func (s *bluetoothStream) close() {
	s.once.Do(func() {
		s.cancel()
		if s.input != nil {
			s.input.Close()
		}
		if s.output != nil {
			s.output.Close()
		}
	})
}

func openBluetoothStream(ctx context.Context, device bluetoothPCM, uplink chan []int16) (*bluetoothStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	s := &bluetoothStream{cancel: cancel, downlink: make(chan []int16, 15), done: make(chan struct{})}
	microphone, speaker := blueALSACommand(ctx, "open", device.source), blueALSACommand(ctx, "open", device.sink)
	read, err := microphone.StdoutPipe()
	if err != nil {
		cancel()
		return nil, errors.New("bluetooth-audio-failed")
	}
	s.input = read
	write, err := speaker.StdinPipe()
	if err != nil {
		s.close()
		return nil, errors.New("bluetooth-audio-failed")
	}
	s.output = write
	if err = microphone.Start(); err != nil {
		s.close()
		return nil, errors.New("bluetooth-audio-failed")
	}
	if err = speaker.Start(); err != nil {
		s.close()
		_ = microphone.Wait()
		return nil, errors.New("bluetooth-audio-failed")
	}
	s.commands = []*exec.Cmd{microphone, speaker}
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		defer s.close()
		data := make([]byte, device.rate/50*2)
		for {
			if _, err := io.ReadFull(read, data); err != nil {
				return
			}
			pcm := make([]int16, 320)
			for i := range pcm {
				index := i
				if device.rate == 8000 {
					index = i / 2
				}
				pcm[i] = int16(binary.LittleEndian.Uint16(data[index*2:]))
			}
			select {
			case uplink <- pcm:
			case <-ctx.Done():
				return
			default:
				select {
				case <-uplink:
				default:
				}
				select {
				case uplink <- pcm:
				default:
				}
			}
		}
	}()
	go func() {
		defer workers.Done()
		defer s.close()
		for {
			select {
			case <-ctx.Done():
				return
			case pcm := <-s.downlink:
				data := make([]byte, device.rate/50*2)
				for i := 0; i < len(data)/2; i++ {
					value := pcm[i]
					if device.rate == 8000 {
						value = int16((int32(pcm[i*2]) + int32(pcm[i*2+1])) / 2)
					}
					binary.LittleEndian.PutUint16(data[i*2:], uint16(value))
				}
				if _, err := write.Write(data); err != nil {
					return
				}
			}
		}
	}()
	go func() {
		workers.Wait()
		for _, cmd := range s.commands {
			_ = cmd.Wait()
		}
		close(s.done)
	}()
	return s, nil
}

func (s *bluetoothStream) play(pcm []int16) {
	if len(pcm) != 320 {
		return
	}
	select {
	case s.downlink <- pcm:
	default:
		select {
		case <-s.downlink:
		default:
		}
		select {
		case s.downlink <- pcm:
		default:
		}
	}
}
