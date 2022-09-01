package sync

import (
	"math"

	"github.com/ElrondNetwork/elrond-go-core/core/check"
	"github.com/ElrondNetwork/elrond-go-core/data"
	"github.com/ElrondNetwork/elrond-go/consensus"
	"github.com/ElrondNetwork/elrond-go/process"
)

var _ process.ForkDetector = (*shardForkDetector)(nil)

// shardForkDetector implements the shard fork detector mechanism
type shardForkDetector struct {
	*baseForkDetector
}

// NewShardForkDetector method creates a new shardForkDetector object
func NewShardForkDetector(
	roundHandler consensus.RoundHandler,
	blackListHandler process.TimeCacher,
	blockTracker process.BlockTracker,
	genesisTime int64,
) (*shardForkDetector, error) {

	if check.IfNil(roundHandler) {
		return nil, process.ErrNilRoundHandler
	}
	if check.IfNil(blackListHandler) {
		return nil, process.ErrNilBlackListCacher
	}
	if check.IfNil(blockTracker) {
		return nil, process.ErrNilBlockTracker
	}

	genesisHdr, _, err := blockTracker.GetSelfNotarizedHeader(0, 0)
	if err != nil {
		return nil, err
	}

	bfd := &baseForkDetector{
		roundHandler:     roundHandler,
		blackListHandler: blackListHandler,
		genesisTime:      genesisTime,
		blockTracker:     blockTracker,
		genesisNonce:     genesisHdr.GetNonce(),
		genesisRound:     genesisHdr.GetRound(),
		genesisEpoch:     genesisHdr.GetEpoch(),
	}

	bfd.headers = make(map[uint64][]*headerInfo)
	bfd.fork.checkpoint = make([]*checkpointInfo, 0)
	checkpoint := &checkpointInfo{
		nonce: bfd.genesisNonce,
		round: bfd.genesisRound,
	}
	bfd.setFinalCheckpoint(checkpoint)
	bfd.addCheckpoint(checkpoint)
	bfd.fork.rollBackNonce = math.MaxUint64
	bfd.fork.probableHighestNonce = bfd.genesisNonce
	bfd.fork.highestNonceReceived = bfd.genesisNonce

	sfd := shardForkDetector{
		baseForkDetector: bfd,
	}

	return &sfd, nil
}

// AddHeader method adds a new header to headers map
func (sfd *shardForkDetector) AddHeader(
	header data.HeaderHandler,
	headerHash []byte,
	state process.BlockHeaderState,
	selfNotarizedHeaders []data.HeaderHandler,
	selfNotarizedHeadersHashes [][]byte,
) error {
	return sfd.addHeader(
		header,
		headerHash,
		state,
		selfNotarizedHeaders,
		selfNotarizedHeadersHashes,
		sfd.doJobOnBHProcessed,
	)
}

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

func (sfd *shardForkDetector) computeFinalCheckpoint() {
}
