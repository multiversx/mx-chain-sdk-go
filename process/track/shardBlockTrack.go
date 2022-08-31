package track

import (
	"bytes"

	"github.com/ElrondNetwork/elrond-go-core/data"
	"github.com/ElrondNetwork/elrond-go/process"
)

type shardBlockTrack struct {
	*baseBlockTrack
}

// NewShardBlockTrack creates an object for tracking the received shard blocks
func NewShardBlockTrack(arguments ArgShardTracker) (*shardBlockTrack, error) {
	err := checkTrackerNilParameters(arguments.ArgBaseTracker)
	if err != nil {
		return nil, err
	}

	bbt, err := createBaseBlockTrack(arguments.ArgBaseTracker)
	if err != nil {
		return nil, err
	}

	err = bbt.initNotarizedHeaders(arguments.StartHeader)
	if err != nil {
		return nil, err
	}

	sbt := shardBlockTrack{
		baseBlockTrack: bbt,
	}

	argBlockProcessor := ArgBlockProcessor{
		HeaderValidator:                       arguments.HeaderValidator,
		RequestHandler:                        arguments.RequestHandler,
		ShardCoordinator:                      arguments.ShardCoordinator,
		BlockTracker:                          &sbt,
		SelfNotarizer:                         bbt.selfNotarizer,
		SelfNotarizedHeadersNotifier:          bbt.selfNotarizedHeadersNotifier,
		RoundHandler:                          arguments.RoundHandler,
	}

	blockProcessorObject, err := NewBlockProcessor(argBlockProcessor)
	if err != nil {
		return nil, err
	}

	sbt.blockProcessor = blockProcessorObject
	sbt.headers = make(map[uint64][]*HeaderInfo)
	sbt.headersPool.RegisterHandler(sbt.receivedHeader)
	sbt.headersPool.Clear()

	return &sbt, nil
}

func (sbt *shardBlockTrack) getTrackedShardHeaderWithNonceAndHash(
	nonce uint64,
	hash []byte,
) (data.ShardHeaderHandler, error) {

	headers, headersHashes := sbt.GetTrackedHeadersWithNonce(nonce)
	for i := 0; i < len(headers); i++ {
		if !bytes.Equal(headersHashes[i], hash) {
			continue
		}

		header, ok := headers[i].(data.ShardHeaderHandler)
		if !ok {
			return nil, process.ErrWrongTypeAssertion
		}

		return header, nil
	}

	return nil, process.ErrMissingHeader
}

// ComputeLongestSelfChain computes the longest chain from self shard
func (sbt *shardBlockTrack) ComputeLongestSelfChain() (data.HeaderHandler, []byte, []data.HeaderHandler, [][]byte) {
	lastSelfNotarizedHeader, lastSelfNotarizedHeaderHash, err := sbt.selfNotarizer.GetLastNotarizedHeader()
	if err != nil {
		log.Warn("ComputeLongestSelfChain.GetLastNotarizedHeader", "error", err.Error())
		return nil, nil, nil, nil
	}

	headers, hashes := sbt.ComputeLongestChain(lastSelfNotarizedHeader)
	return lastSelfNotarizedHeader, lastSelfNotarizedHeaderHash, headers, hashes
}
