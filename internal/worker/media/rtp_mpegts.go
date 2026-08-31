package media

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"
)

const mpegTSPayloadType = 33

// RunRTPMPEGTSBridge removes the RTP transport header from an RTP/MP2T stream
// and forwards the MPEG-TS payload to MediaMTX's UDP MPEG-TS input. Container
// demultiplexing remains a MediaMTX responsibility.
func RunRTPMPEGTSBridge(ctx context.Context, listenAddress, destinationAddress string, logger *slog.Logger) error {
	listener, err := net.ListenPacket("udp", listenAddress)
	if err != nil {
		return fmt.Errorf("listen for gateway RTP: %w", err)
	}
	defer func() { _ = listener.Close() }()
	destination, err := net.ResolveUDPAddr("udp", destinationAddress)
	if err != nil {
		return fmt.Errorf("resolve MediaMTX MPEG-TS address: %w", err)
	}
	output, err := net.DialUDP("udp", nil, destination)
	if err != nil {
		return fmt.Errorf("connect to MediaMTX MPEG-TS input: %w", err)
	}
	defer func() { _ = output.Close() }()
	if logger == nil {
		logger = slog.Default()
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	buffer := make([]byte, 64*1024)
	var lastInvalidLog time.Time
	for {
		n, _, err := listener.ReadFrom(buffer)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("read gateway RTP: %w", err)
		}
		payload, err := rtpMPEGTSPayload(buffer[:n])
		if err != nil {
			if time.Since(lastInvalidLog) >= time.Second {
				logger.Warn("discarding invalid RTP/MP2T packet", "error", err)
				lastInvalidLog = time.Now()
			}
			continue
		}
		if _, err := output.Write(payload); err != nil {
			return fmt.Errorf("forward MPEG-TS payload to MediaMTX: %w", err)
		}
	}
}

func rtpMPEGTSPayload(packet []byte) ([]byte, error) {
	if len(packet) < 12 {
		return nil, fmt.Errorf("RTP packet is shorter than its fixed header")
	}
	if packet[0]>>6 != 2 {
		return nil, fmt.Errorf("unsupported RTP version %d", packet[0]>>6)
	}
	if packet[1]&0x7f != mpegTSPayloadType {
		return nil, fmt.Errorf("RTP payload type is %d, expected MP2T payload type %d", packet[1]&0x7f, mpegTSPayloadType)
	}
	headerLength := 12 + int(packet[0]&0x0f)*4
	if headerLength > len(packet) {
		return nil, fmt.Errorf("RTP CSRC list exceeds packet length")
	}
	if packet[0]&0x10 != 0 {
		if headerLength+4 > len(packet) {
			return nil, fmt.Errorf("RTP extension header is truncated")
		}
		extensionLength := int(binary.BigEndian.Uint16(packet[headerLength+2:headerLength+4])) * 4
		headerLength += 4 + extensionLength
		if headerLength > len(packet) {
			return nil, fmt.Errorf("RTP extension data exceeds packet length")
		}
	}
	payloadEnd := len(packet)
	if packet[0]&0x20 != 0 {
		paddingLength := int(packet[len(packet)-1])
		if paddingLength == 0 || paddingLength > payloadEnd-headerLength {
			return nil, fmt.Errorf("RTP padding is invalid")
		}
		payloadEnd -= paddingLength
	}
	payload := packet[headerLength:payloadEnd]
	if len(payload) == 0 || len(payload)%188 != 0 {
		return nil, fmt.Errorf("MP2T payload length %d is not a positive multiple of 188", len(payload))
	}
	for offset := 0; offset < len(payload); offset += 188 {
		if payload[offset] != 0x47 {
			return nil, fmt.Errorf("MPEG-TS sync byte is missing at payload offset %d", offset)
		}
	}
	return payload, nil
}
