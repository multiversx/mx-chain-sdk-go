package block

import (
	"github.com/ElrondNetwork/elrond-go/process"
	"github.com/ElrondNetwork/elrond-go/process/block"
)

// ArgShardProcessor holds all dependencies required by the process data factory in order to create
// new instances of shard processor
type ArgShardProcessor struct {
	block.ArgBaseProcessor
	ValidatorStatisticsProcessor process.ValidatorStatisticsProcessor
	SCToProtocol                 process.SmartContractToProtocolHandler
}
