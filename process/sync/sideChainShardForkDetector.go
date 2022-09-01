package sync

import (
	"github.com/ElrondNetwork/elrond-go-core/data"
	logger "github.com/ElrondNetwork/elrond-go-logger"
)

var log = logger.GetOrCreate("process/sync")

func (sfd *shardForkDetector) doJobOnBHProcessed(
	header data.HeaderHandler,
	headerHash []byte,
	_ []data.HeaderHandler,
	_ [][]byte,
) {
	sfd.setFinalCheckpoint(sfd.lastCheckpoint())
	sfd.addCheckpoint(&checkpointInfo{nonce: header.GetNonce(), round: header.GetRound(), hash: headerHash})
	sfd.removePastOrInvalidRecords()
}
