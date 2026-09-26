use std::collections::HashMap;

use thiserror::Error;

pub(crate) const TS_PACKET_SIZE: usize = 188;
const MAX_ACCESS_UNIT_BYTES: usize = 4 * 1024 * 1024;

#[derive(Debug, Error, PartialEq, Eq)]
pub(crate) enum TransportStreamError {
    #[error("RIST payload is not a sequence of 188-byte MPEG-TS packets")]
    InvalidPacketAlignment,
    #[error("MPEG-TS sync byte is missing")]
    MissingSyncByte,
    #[error("MPEG-TS packet has an invalid adaptation field")]
    InvalidAdaptationField,
    #[error("MPEG-TS continuity gap on PID {0}")]
    ContinuityGap(u16),
}

#[derive(Debug, Default)]
pub(crate) struct Inspector {
    pmt_pid: Option<u16>,
    video_pid: Option<u16>,
    pat: Option<[u8; TS_PACKET_SIZE]>,
    pmt: Option<[u8; TS_PACKET_SIZE]>,
    current_access_unit: Vec<[u8; TS_PACKET_SIZE]>,
    current_es: Vec<u8>,
    current_has_sps: bool,
    current_has_pps: bool,
    sps_access_unit: Vec<[u8; TS_PACKET_SIZE]>,
    pps_access_unit: Vec<[u8; TS_PACKET_SIZE]>,
    continuity: HashMap<u16, u8>,
}

impl Inspector {
    pub(crate) fn validate_payload(payload: &[u8]) -> Result<(), TransportStreamError> {
        if payload.is_empty() || !payload.len().is_multiple_of(TS_PACKET_SIZE) {
            return Err(TransportStreamError::InvalidPacketAlignment);
        }
        if payload
            .chunks_exact(TS_PACKET_SIZE)
            .any(|packet| packet[0] != 0x47)
        {
            return Err(TransportStreamError::MissingSyncByte);
        }
        Ok(())
    }

    /// Observes one TS packet and returns a self-contained H.264 random-access
    /// prefix when the packet completes the start gate.
    pub(crate) fn observe(
        &mut self,
        packet: &[u8],
    ) -> Result<Option<Vec<u8>>, TransportStreamError> {
        let packet: &[u8; TS_PACKET_SIZE] = packet
            .try_into()
            .map_err(|_| TransportStreamError::InvalidPacketAlignment)?;
        if packet[0] != 0x47 {
            return Err(TransportStreamError::MissingSyncByte);
        }

        let pid = (u16::from(packet[1] & 0x1f) << 8) | u16::from(packet[2]);
        let payload_unit_start = packet[1] & 0x40 != 0;
        let adaptation_control = (packet[3] >> 4) & 0x03;
        let continuity_counter = packet[3] & 0x0f;
        let has_payload = adaptation_control & 0x01 != 0;
        let mut payload_offset = 4;
        let mut discontinuity = false;

        if adaptation_control == 0 {
            return Err(TransportStreamError::InvalidAdaptationField);
        }
        if adaptation_control & 0x02 != 0 {
            let length = usize::from(packet[4]);
            if length > 183 {
                return Err(TransportStreamError::InvalidAdaptationField);
            }
            if length != 0 {
                discontinuity = packet[5] & 0x80 != 0;
            }
            payload_offset += length + 1;
        }
        if payload_offset > TS_PACKET_SIZE {
            return Err(TransportStreamError::InvalidAdaptationField);
        }
        if has_payload
            && let Some(previous) = self.continuity.insert(pid, continuity_counter)
            && !discontinuity
            && continuity_counter != ((previous + 1) & 0x0f)
        {
            return Err(TransportStreamError::ContinuityGap(pid));
        }
        if !has_payload || payload_offset == TS_PACKET_SIZE {
            return Ok(None);
        }
        let payload = &packet[payload_offset..];

        if pid == 0 && payload_unit_start {
            if let Some(pmt_pid) = parse_pat(payload) {
                self.pmt_pid = Some(pmt_pid);
                self.pat = Some(*packet);
            }
            return Ok(None);
        }
        if Some(pid) == self.pmt_pid && payload_unit_start {
            if let Some(video_pid) = parse_h264_pmt(payload) {
                self.video_pid = Some(video_pid);
                self.pmt = Some(*packet);
            }
            return Ok(None);
        }
        if Some(pid) != self.video_pid {
            return Ok(None);
        }

        if payload_unit_start {
            self.cache_parameter_sets();
            self.current_access_unit.clear();
            self.current_es.clear();
            self.current_has_sps = false;
            self.current_has_pps = false;
        }
        if self.current_access_unit.len() * TS_PACKET_SIZE >= MAX_ACCESS_UNIT_BYTES {
            return Ok(None);
        }
        self.current_access_unit.push(*packet);
        self.current_es.extend_from_slice(payload);
        let nal_types = h264_nal_types(&self.current_es);
        self.current_has_sps = nal_types.contains(&7);
        self.current_has_pps = nal_types.contains(&8);
        let has_idr = nal_types.contains(&5);
        let has_sps = self.current_has_sps || !self.sps_access_unit.is_empty();
        let has_pps = self.current_has_pps || !self.pps_access_unit.is_empty();
        if !has_idr || !has_sps || !has_pps {
            return Ok(None);
        }
        let (Some(pat), Some(pmt)) = (self.pat, self.pmt) else {
            return Ok(None);
        };

        let mut prefix = Vec::with_capacity(
            (2 + self.sps_access_unit.len()
                + self.pps_access_unit.len()
                + self.current_access_unit.len())
                * TS_PACKET_SIZE,
        );
        prefix.extend_from_slice(&pat);
        prefix.extend_from_slice(&pmt);
        if !self.current_has_sps {
            append_packets(&mut prefix, &self.sps_access_unit);
        }
        if !self.current_has_pps && self.pps_access_unit != self.sps_access_unit {
            append_packets(&mut prefix, &self.pps_access_unit);
        }
        append_packets(&mut prefix, &self.current_access_unit);
        Ok(Some(prefix))
    }

    fn cache_parameter_sets(&mut self) {
        if self.current_has_sps {
            self.sps_access_unit.clone_from(&self.current_access_unit);
        }
        if self.current_has_pps {
            self.pps_access_unit.clone_from(&self.current_access_unit);
        }
    }
}

fn append_packets(output: &mut Vec<u8>, packets: &[[u8; TS_PACKET_SIZE]]) {
    for packet in packets {
        output.extend_from_slice(packet);
    }
}

fn psi_section(payload: &[u8]) -> Option<&[u8]> {
    let pointer = usize::from(*payload.first()?);
    let section = payload.get(1 + pointer..)?;
    let length = usize::from(*section.get(1)? & 0x0f) << 8 | usize::from(*section.get(2)?);
    section.get(..3 + length)
}

fn parse_pat(payload: &[u8]) -> Option<u16> {
    let section = psi_section(payload)?;
    if section.first() != Some(&0x00) || section.len() < 12 {
        return None;
    }
    let entries_end = section.len().checked_sub(4)?;
    for entry in section.get(8..entries_end)?.chunks_exact(4) {
        let program = u16::from_be_bytes([entry[0], entry[1]]);
        if program != 0 {
            return Some((u16::from(entry[2] & 0x1f) << 8) | u16::from(entry[3]));
        }
    }
    None
}

fn parse_h264_pmt(payload: &[u8]) -> Option<u16> {
    let section = psi_section(payload)?;
    if section.first() != Some(&0x02) || section.len() < 16 {
        return None;
    }
    let program_info_length = usize::from(section[10] & 0x0f) << 8 | usize::from(section[11]);
    let mut offset = 12 + program_info_length;
    let streams_end = section.len().checked_sub(4)?;
    while offset + 5 <= streams_end {
        let stream_type = section[offset];
        let pid = (u16::from(section[offset + 1] & 0x1f) << 8) | u16::from(section[offset + 2]);
        let info_length =
            usize::from(section[offset + 3] & 0x0f) << 8 | usize::from(section[offset + 4]);
        if stream_type == 0x1b {
            return Some(pid);
        }
        offset = offset.checked_add(5 + info_length)?;
    }
    None
}

fn h264_nal_types(bytes: &[u8]) -> Vec<u8> {
    let mut result = Vec::new();
    let mut index = 0;
    while index + 4 <= bytes.len() {
        let header = if bytes[index..].starts_with(&[0, 0, 1]) {
            Some(index + 3)
        } else if bytes[index..].starts_with(&[0, 0, 0, 1]) {
            Some(index + 4)
        } else {
            None
        };
        if let Some(header) = header
            && let Some(byte) = bytes.get(header)
        {
            result.push(byte & 0x1f);
            index = header + 1;
        } else {
            index += 1;
        }
    }
    result
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn validates_packet_alignment_and_sync_bytes() {
        assert_eq!(
            Inspector::validate_payload(&[]),
            Err(TransportStreamError::InvalidPacketAlignment)
        );
        assert_eq!(
            Inspector::validate_payload(&[0; TS_PACKET_SIZE]),
            Err(TransportStreamError::MissingSyncByte)
        );
        let mut packet = [0xff; TS_PACKET_SIZE];
        packet[0] = 0x47;
        assert_eq!(Inspector::validate_payload(&packet), Ok(()));
    }

    #[test]
    fn finds_annex_b_parameter_sets_and_idr() {
        assert_eq!(
            h264_nal_types(&[0, 0, 1, 0x67, 1, 0, 0, 0, 1, 0x68, 0, 0, 1, 0x65]),
            vec![7, 8, 5]
        );
    }

    #[test]
    fn opens_gate_only_after_pat_pmt_parameter_sets_and_idr() {
        let mut inspector = Inspector::default();
        assert_eq!(inspector.observe(&pat_packet(0x100)).unwrap(), None);
        assert_eq!(inspector.observe(&pmt_packet(0x100, 0x101)).unwrap(), None);
        let prefix = inspector
            .observe(&video_packet(0x101, 0, &[7, 8, 5]))
            .unwrap()
            .expect("IDR with parameter sets opens gate");
        assert_eq!(prefix.len(), 3 * TS_PACKET_SIZE);
        assert!(
            prefix
                .chunks_exact(TS_PACKET_SIZE)
                .all(|packet| packet[0] == 0x47)
        );
    }

    pub(super) fn pat_packet(pmt_pid: u16) -> [u8; TS_PACKET_SIZE] {
        let mut packet = payload_packet(0, 0, true);
        let section = [
            0x00,
            0xb0,
            0x0d,
            0x00,
            0x01,
            0xc1,
            0x00,
            0x00,
            0x00,
            0x01,
            0xe0 | ((pmt_pid >> 8) as u8 & 0x1f),
            pmt_pid as u8,
            0,
            0,
            0,
            0,
        ];
        packet[4] = 0;
        packet[5..5 + section.len()].copy_from_slice(&section);
        packet
    }

    pub(super) fn pmt_packet(pmt_pid: u16, video_pid: u16) -> [u8; TS_PACKET_SIZE] {
        let mut packet = payload_packet(pmt_pid, 0, true);
        let section = [
            0x02,
            0xb0,
            0x12,
            0x00,
            0x01,
            0xc1,
            0x00,
            0x00,
            0xe0 | ((video_pid >> 8) as u8 & 0x1f),
            video_pid as u8,
            0xf0,
            0x00,
            0x1b,
            0xe0 | ((video_pid >> 8) as u8 & 0x1f),
            video_pid as u8,
            0xf0,
            0x00,
            0,
            0,
            0,
            0,
        ];
        packet[4] = 0;
        packet[5..5 + section.len()].copy_from_slice(&section);
        packet
    }

    pub(super) fn video_packet(
        video_pid: u16,
        continuity: u8,
        nal_types: &[u8],
    ) -> [u8; TS_PACKET_SIZE] {
        let mut packet = payload_packet(video_pid, continuity, true);
        let mut offset = 4;
        packet[offset..offset + 9].copy_from_slice(&[0, 0, 1, 0xe0, 0, 0, 0x80, 0, 0]);
        offset += 9;
        for nal_type in nal_types {
            packet[offset..offset + 4].copy_from_slice(&[0, 0, 1, *nal_type]);
            offset += 4;
        }
        packet
    }

    fn payload_packet(pid: u16, continuity: u8, start: bool) -> [u8; TS_PACKET_SIZE] {
        let mut packet = [0xff; TS_PACKET_SIZE];
        packet[0] = 0x47;
        packet[1] = ((pid >> 8) as u8 & 0x1f) | if start { 0x40 } else { 0 };
        packet[2] = pid as u8;
        packet[3] = 0x10 | (continuity & 0x0f);
        packet
    }
}
