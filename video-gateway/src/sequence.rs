#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum PacketOrder {
    Continuous,
    Gap(u64),
    DuplicateOrLate,
    Discontinuity,
}

#[derive(Debug, Default)]
pub struct SequenceState {
    previous_sequence: Option<u64>,
    previous_timestamp: Option<u64>,
}

impl SequenceState {
    pub fn observe(
        &mut self,
        sequence: u64,
        timestamp: u64,
        explicitly_discontinuous: bool,
    ) -> PacketOrder {
        let Some(previous_sequence) = self.previous_sequence else {
            self.set_baseline(sequence, timestamp);
            return if explicitly_discontinuous {
                PacketOrder::Discontinuity
            } else {
                PacketOrder::Continuous
            };
        };
        if explicitly_discontinuous
            || (sequence > previous_sequence
                && self.previous_timestamp.is_some_and(|old| timestamp < old))
        {
            self.set_baseline(sequence, timestamp);
            return PacketOrder::Discontinuity;
        }
        if sequence <= previous_sequence {
            return PacketOrder::DuplicateOrLate;
        }
        let gap = sequence - previous_sequence - 1;
        self.set_baseline(sequence, timestamp);
        if gap == 0 {
            PacketOrder::Continuous
        } else {
            PacketOrder::Gap(gap)
        }
    }

    fn set_baseline(&mut self, sequence: u64, timestamp: u64) {
        self.previous_sequence = Some(sequence);
        self.previous_timestamp = Some(timestamp);
    }
}

#[cfg(test)]
mod tests {
    use super::{PacketOrder, SequenceState};

    #[test]
    fn counts_sequence_gaps_without_counting_first_packet() {
        let mut state = SequenceState::default();
        assert_eq!(state.observe(10, 100, false), PacketOrder::Continuous);
        assert_eq!(state.observe(11, 101, false), PacketOrder::Continuous);
        assert_eq!(state.observe(15, 105, false), PacketOrder::Gap(3));
    }

    #[test]
    fn ignores_duplicates_and_late_packets() {
        let mut state = SequenceState::default();
        state.observe(10, 100, false);
        state.observe(12, 102, false);
        assert_eq!(state.observe(12, 102, false), PacketOrder::DuplicateOrLate);
        assert_eq!(state.observe(11, 101, false), PacketOrder::DuplicateOrLate);
        assert_eq!(state.observe(13, 103, false), PacketOrder::Continuous);
    }

    #[test]
    fn resets_baseline_on_explicit_discontinuity() {
        let mut state = SequenceState::default();
        state.observe(100, 1_000, false);
        assert_eq!(state.observe(200, 2_000, true), PacketOrder::Discontinuity);
        assert_eq!(state.observe(201, 2_001, false), PacketOrder::Continuous);
    }

    #[test]
    fn timestamp_reset_is_not_loss() {
        let mut state = SequenceState::default();
        state.observe(100, 10_000, false);
        assert_eq!(state.observe(101, 1_000, false), PacketOrder::Discontinuity);
    }

    #[test]
    fn extended_sequence_wrap_is_continuous() {
        let mut state = SequenceState::default();
        state.observe(u32::MAX as u64, 100, false);
        assert_eq!(
            state.observe(u32::MAX as u64 + 1, 101, false),
            PacketOrder::Continuous
        );
    }
}
