package block

import (
	"github.com/multiversx/mx-chain-go/process"
	"github.com/multiversx/mx-chain-go/process/block"
)

// ArgShardProcessor holds all dependencies required by the process data factory in order to create
// new instances of shard processor
type ArgShardProcessor struct {
	block.ArgBaseProcessor
	ValidatorStatisticsProcessor process.ValidatorStatisticsProcessor
	SCToProtocol                 process.SmartContractToProtocolHandler
}
