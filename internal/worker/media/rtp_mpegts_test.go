package media

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestRTPMPEGTSPayload(t *testing.T) {
	mpegts := make([]byte, 188*2)
	mpegts[0] = 0x47
	mpegts[188] = 0x47
	packet := append([]byte{0x90, 0x80 | mpegTSPayloadType, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3},
		0xbe, 0xde, 0, 1, 1, 2, 3, 4)
	packet = append(packet, mpegts...)
	payload, err := rtpMPEGTSPayload(packet)
	if err != nil {
		t.Fatalf("rtpMPEGTSPayload() error = %v", err)
	}
	if !bytes.Equal(payload, mpegts) {
		t.Fatalf("rtpMPEGTSPayload() returned different payload")
	}
}

func TestRTPMPEGTSPayloadRejectsMalformedPackets(t *testing.T) {
	validPayload := make([]byte, 188)
	validPayload[0] = 0x47
	valid := append([]byte{0x80, mpegTSPayloadType, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3}, validPayload...)
	tests := map[string][]byte{
		"short header":        {0x80},
		"wrong payload type":  append([]byte(nil), valid...),
		"broken transport":    append([]byte(nil), valid...),
		"truncated extension": {0x90, mpegTSPayloadType, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3, 0, 0, 0},
	}
	tests["wrong payload type"][1] = 96
	tests["broken transport"][12] = 0
	for name, packet := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := rtpMPEGTSPayload(packet); err == nil {
				t.Fatal("rtpMPEGTSPayload() error = nil")
			}
		})
	}
}

func TestRTPMPEGTSPayloadRemovesPadding(t *testing.T) {
	mpegts := make([]byte, 188)
	mpegts[0] = 0x47
	packet := append([]byte{0xa0, mpegTSPayloadType, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3}, mpegts...)
	packet = append(packet, 0, 0, 0, 4)
	payload, err := rtpMPEGTSPayload(packet)
	if err != nil || !bytes.Equal(payload, mpegts) {
		t.Fatalf("rtpMPEGTSPayload() = %d bytes, %v", len(payload), err)
	}
}

func TestRTPMPEGTSPayloadRejectsOversizedExtension(t *testing.T) {
	packet := []byte{0x90, mpegTSPayloadType, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3, 0xbe, 0xde, 0, 0}
	binary.BigEndian.PutUint16(packet[14:16], 1)
	if _, err := rtpMPEGTSPayload(packet); err == nil {
		t.Fatal("rtpMPEGTSPayload() error = nil")
	}
}
