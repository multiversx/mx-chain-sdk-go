package nodesCoordinator

import (
	"encoding/hex"
	logger "github.com/ElrondNetwork/elrond-go-logger"
	"github.com/ElrondNetwork/elrond-go/sharding/nodesCoordinator"
)

var log = logger.GetOrCreate("sharding/nodesCoordinator")

func computeStartIndexAndNumAppearancesForValidator(expEligibleList []uint32, idx int64) (int64, int64) {
	val := expEligibleList[idx]
	startIdx := int64(0)
	listLen := int64(len(expEligibleList))

	for i := idx - 1; i >= 0; i-- {
		if expEligibleList[i] != val {
			startIdx = i + 1
			break
		}
	}

	endIdx := listLen - 1
	for i := idx + 1; i < listLen; i++ {
		if expEligibleList[i] != val {
			endIdx = i - 1
			break
		}
	}

	return startIdx, endIdx - startIdx + 1
}

func displayValidatorsForRandomness(validators []nodesCoordinator.Validator, randomness []byte) {
	if log.GetLevel() != logger.LogTrace {
		return
	}

	strValidators := ""

	for _, v := range validators {
		strValidators += "\n" + hex.EncodeToString(v.PubKey())
	}

	log.Trace("selectValidators", "randomness", randomness, "validators", strValidators)
}
