package state

const (
	stateVersion    = 1
	metadataDirName = ".kinugasa-worker"
	stateFileName   = "state.json"
)

type diskState struct {
	Version           int               `json:"version"`
	WorkerID          string            `json:"workerId"`
	LastEventSequence uint64            `json:"lastEventSequence"`
	Input             []byte            `json:"input"`
	Recording         []byte            `json:"recording,omitempty"`
	Outbox            []diskEvent       `json:"outbox"`
	CommandResults    map[string][]byte `json:"commandResults"`
}

type diskEvent struct {
	EventID  string `json:"eventId"`
	Sequence uint64 `json:"sequence"`
	Message  []byte `json:"message"`
}

func cloneDiskState(source diskState) diskState {
	clone := source
	clone.Input = append([]byte(nil), source.Input...)
	clone.Recording = append([]byte(nil), source.Recording...)
	clone.Outbox = make([]diskEvent, len(source.Outbox))
	for index, event := range source.Outbox {
		clone.Outbox[index] = event
		clone.Outbox[index].Message = append([]byte(nil), event.Message...)
	}
	clone.CommandResults = make(map[string][]byte, len(source.CommandResults))
	for commandID, result := range source.CommandResults {
		clone.CommandResults[commandID] = append([]byte(nil), result...)
	}
	return clone
}
