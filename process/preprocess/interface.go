package preprocess

import (
	"github.com/ElrondNetwork/elrond-go-core/data/block"
	"github.com/ElrondNetwork/elrond-go/process"
	"github.com/ElrondNetwork/elrond-go/storage/txcache"
)

// TransactionPreprocessor complete interface for transaction preprocessor from elrond-go
type TransactionPreprocessor interface {
	process.PreProcessor
	CreateAndProcessScheduledMiniBlocksFromMeAsProposer(
		haveTime func() bool,
		haveAdditionalTime func() bool,
		sortedTxs []*txcache.WrappedTransaction,
		mapSCTxs map[string]struct{},
	) (block.MiniBlockSlice, error)
	PrefilterTransactions(
		initialTxs []*txcache.WrappedTransaction,
		additionalTxs []*txcache.WrappedTransaction,
		initialTxsGasEstimation uint64,
		gasBandwidth uint64,
	) ([]*txcache.WrappedTransaction, []*txcache.WrappedTransaction)
	ComputeSortedTxs(
		sndShardId uint32,
		dstShardId uint32,
		gasBandwidth uint64,
		randomness []byte,
	) ([]*txcache.WrappedTransaction, []*txcache.WrappedTransaction, error)
}
