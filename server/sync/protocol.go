package sync

import (
	"encoding/json"

	"github.com/reearth/ygo/encoding"
)

const (
	EnvelopeSync      = 0
	EnvelopeAwareness = 1
	EnvelopeControl   = 2
)

const (
	ControlEpoch = "epoch"
	ControlReset = "reset"
)

type ControlMessage struct {
	Kind  string `json:"kind"`
	Epoch int    `json:"epoch,omitempty"`
}

func EncodeSyncEnvelope(epoch int, inner []byte) []byte {
	enc := encoding.NewEncoder()
	enc.WriteVarUint(EnvelopeSync)
	enc.WriteVarUint(uint64(epoch))
	enc.WriteRaw(inner)
	return enc.Bytes()
}

func DecodeSyncPayload(payload []byte) (int, []byte, error) {
	dec := encoding.NewDecoder(payload)
	epoch, err := dec.ReadVarUint()
	if err != nil {
		return 0, nil, err
	}
	rest := dec.RemainingBytesCopy()
	return int(epoch), rest, nil
}

func EncodeAwarenessEnvelope(payload []byte) []byte {
	enc := encoding.NewEncoder()
	enc.WriteVarUint(EnvelopeAwareness)
	enc.WriteRaw(payload)
	return enc.Bytes()
}

func EncodeControl(kind string, epoch int) []byte {
	msg, _ := json.Marshal(ControlMessage{Kind: kind, Epoch: epoch})
	enc := encoding.NewEncoder()
	enc.WriteVarUint(EnvelopeControl)
	enc.WriteRaw(msg)
	return enc.Bytes()
}

func DecodeEnvelope(msg []byte) (int, []byte, error) {
	dec := encoding.NewDecoder(msg)
	env, err := dec.ReadVarUint()
	if err != nil {
		return 0, nil, err
	}
	rest := dec.RemainingBytesCopy()
	return int(env), rest, nil
}

func DecodeControl(payload []byte) (ControlMessage, error) {
	var m ControlMessage
	if err := json.Unmarshal(payload, &m); err != nil {
		return ControlMessage{}, err
	}
	return m, nil
}
