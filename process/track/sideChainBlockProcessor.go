package track

import (
	"github.com/ElrondNetwork/elrond-go-core/data"
)

func (bp *blockProcessor) shouldProcessReceivedHeader(headerHandler data.HeaderHandler) bool {
	lastNotarizedHeader, _, err := bp.selfNotarizer.GetLastNotarizedHeader(headerHandler.GetShardID())
	if err != nil {
		log.Warn("shouldProcessReceivedHeader: selfNotarizer.GetLastNotarizedHeader",
			"shard", headerHandler.GetShardID(), "error", err.Error())
		return false
	}

	shouldProcessReceivedHeader := headerHandler.GetNonce() > lastNotarizedHeader.GetNonce()
	return shouldProcessReceivedHeader
}
