package sync

import (
	"github.com/ElrondNetwork/elrond-go-core/data"
	"github.com/ElrondNetwork/elrond-go/process"
)

// GetHeaders -
func (bfd *baseForkDetector) GetHeaders(nonce uint64) []*headerInfo {
	bfd.mutHeaders.Lock()
	defer bfd.mutHeaders.Unlock()

	headers := bfd.headers[nonce]

	if headers == nil {
		return nil
	}

	newHeaders := make([]*headerInfo, len(headers))
	copy(newHeaders, headers)

	return newHeaders
}

// LastCheckpointNonce -
func (bfd *baseForkDetector) LastCheckpointNonce() uint64 {
	return bfd.lastCheckpoint().nonce
}

// LastCheckpointRound -
func (bfd *baseForkDetector) LastCheckpointRound() uint64 {
	return bfd.lastCheckpoint().round
}

// SetFinalCheckpoint -
func (bfd *baseForkDetector) SetFinalCheckpoint(nonce uint64, round uint64, hash []byte) {
	bfd.setFinalCheckpoint(&checkpointInfo{nonce: nonce, round: round, hash: hash})
}

// FinalCheckpointNonce -
func (bfd *baseForkDetector) FinalCheckpointNonce() uint64 {
	return bfd.finalCheckpoint().nonce
}

// FinalCheckpointRound -
func (bfd *baseForkDetector) FinalCheckpointRound() uint64 {
	return bfd.finalCheckpoint().round
}

// CheckBlockValidity -
func (bfd *baseForkDetector) CheckBlockValidity(header data.HeaderHandler, headerHash []byte) error {
	return bfd.checkBlockBasicValidity(header, headerHash)
}

// RemovePastHeaders -
func (bfd *baseForkDetector) RemovePastHeaders() {
	bfd.removePastHeaders()
}

// RemoveInvalidReceivedHeaders -
func (bfd *baseForkDetector) RemoveInvalidReceivedHeaders() {
	bfd.removeInvalidReceivedHeaders()
}

// ComputeProbableHighestNonce -
func (bfd *baseForkDetector) ComputeProbableHighestNonce() uint64 {
	return bfd.computeProbableHighestNonce()
}

// IsConsensusStuck -
func (bfd *baseForkDetector) IsConsensusStuck() bool {
	return bfd.isConsensusStuck()
}

// Hash -
func (hi *headerInfo) Hash() []byte {
	return hi.hash
}

// GetBlockHeaderState -
func (hi *headerInfo) GetBlockHeaderState() process.BlockHeaderState {
	return hi.state
}

// IsHeaderReceivedTooLate -
func (bfd *baseForkDetector) IsHeaderReceivedTooLate(header data.HeaderHandler, state process.BlockHeaderState, finality int64) bool {
	return bfd.isHeaderReceivedTooLate(header, state, finality)
}

// SetProbableHighestNonce -
func (bfd *baseForkDetector) SetProbableHighestNonce(nonce uint64) {
	bfd.setProbableHighestNonce(nonce)
}

// AddCheckPoint -
func (bfd *baseForkDetector) AddCheckPoint(round uint64, nonce uint64, hash []byte) {
	bfd.addCheckpoint(&checkpointInfo{round: round, nonce: nonce, hash: hash})
}

// ComputeGenesisTimeFromHeader -
func (bfd *baseForkDetector) ComputeGenesisTimeFromHeader(headerHandler data.HeaderHandler) int64 {
	return bfd.computeGenesisTimeFromHeader(headerHandler)
}
