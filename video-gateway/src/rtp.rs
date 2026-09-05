use rist_rs::DataBlock;

const HEADER_LENGTH: usize = 12;
const MPEG_TS_PAYLOAD_TYPE: u8 = 33;

pub fn rtp_packet(data: &DataBlock) -> Vec<u8> {
    let mut packet = vec![0; HEADER_LENGTH];
    packet.extend_from_slice(data.payload());
    write_header(
        &mut packet[..HEADER_LENGTH],
        data.sequence(),
        data.ntp_timestamp(),
        data.flow_id(),
    );
    packet
}

fn write_header(header: &mut [u8], sequence: u64, ntp_timestamp: u64, flow_id: u32) {
    header[0] = 0x80;
    header[1] = MPEG_TS_PAYLOAD_TYPE;
    header[2..4].copy_from_slice(&(sequence as u16).to_be_bytes());
    let timestamp = ((u128::from(ntp_timestamp) * 90_000) >> 32) as u32;
    header[4..8].copy_from_slice(&timestamp.to_be_bytes());
    header[8..12].copy_from_slice(&flow_id.to_be_bytes());
}

#[cfg(test)]
mod tests {
    #[test]
    fn maps_recovered_rist_metadata_to_rtp_header() {
        let ntp_timestamp = (3_u64 << 32) + (1_u64 << 31);
        let mut header = [0; 12];
        super::write_header(&mut header, 0x1_2345, ntp_timestamp, 0x1122_3344);
        assert_eq!(
            header,
            [
                0x80, 33, 0x23, 0x45, 0x00, 0x04, 0xce, 0x78, 0x11, 0x22, 0x33, 0x44
            ]
        );
    }
}
