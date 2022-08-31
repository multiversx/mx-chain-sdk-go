package track

import (
	"bytes"
	"fmt"
	"sort"
	"sync"

	"github.com/ElrondNetwork/elrond-go-core/core/check"
	"github.com/ElrondNetwork/elrond-go-core/data"
	"github.com/ElrondNetwork/elrond-go-core/hashing"
	"github.com/ElrondNetwork/elrond-go-core/marshal"
	"github.com/ElrondNetwork/elrond-go-logger"
	"github.com/ElrondNetwork/elrond-go/dataRetriever"
	"github.com/ElrondNetwork/elrond-go/process"
	"github.com/ElrondNetwork/elrond-go/sharding"
)

var _ process.ValidityAttester = (*baseBlockTrack)(nil)

var log = logger.GetOrCreate("process/track")

// HeaderInfo holds the information about a header
type HeaderInfo struct {
	Hash   []byte
	Header data.HeaderHandler
}

type baseBlockTrack struct {
	hasher           hashing.Hasher
	headerValidator  process.HeaderConstructionValidator
	marshalizer      marshal.Marshalizer
	roundHandler     process.RoundHandler
	shardCoordinator sharding.Coordinator
	headersPool      dataRetriever.HeadersPool
	store            dataRetriever.StorageService

	blockProcessor               blockProcessorHandler
	selfNotarizer                blockNotarizerHandler
	selfNotarizedHeadersNotifier blockNotifierHandler
	whitelistHandler             process.WhiteListHandler
	feeHandler                   process.FeeHandler

	mutHeaders          sync.RWMutex
	headers             map[uint64][]*HeaderInfo
	maxNumHeadersToKeep int
}

func createBaseBlockTrack(arguments ArgBaseTracker) (*baseBlockTrack, error) {
	err := checkTrackerNilParameters(arguments)
	if err != nil {
		return nil, err
	}

	maxNumHeadersToKeep := arguments.PoolsHolder.Headers().MaxSize()

	selfNotarizer, err := NewBlockNotarizer(arguments.Hasher, arguments.Marshalizer, arguments.ShardCoordinator)
	if err != nil {
		return nil, err
	}

	selfNotarizedHeadersNotifier, err := NewBlockNotifier()
	if err != nil {
		return nil, err
	}

	bbt := &baseBlockTrack{
		hasher:                       arguments.Hasher,
		headerValidator:              arguments.HeaderValidator,
		marshalizer:                  arguments.Marshalizer,
		roundHandler:                 arguments.RoundHandler,
		shardCoordinator:             arguments.ShardCoordinator,
		headersPool:                  arguments.PoolsHolder.Headers(),
		store:                        arguments.Store,
		selfNotarizer:                selfNotarizer,
		selfNotarizedHeadersNotifier: selfNotarizedHeadersNotifier,
		maxNumHeadersToKeep:          maxNumHeadersToKeep,
		whitelistHandler:             arguments.WhitelistHandler,
		feeHandler:                   arguments.FeeHandler,
	}

	return bbt, nil
}

func (bbt *baseBlockTrack) receivedHeader(headerHandler data.HeaderHandler, headerHash []byte) {
	bbt.receivedShardHeader(headerHandler, headerHash)
}

func (bbt *baseBlockTrack) receivedShardHeader(headerHandler data.HeaderHandler, shardHeaderHash []byte) {
	shardHeader, ok := headerHandler.(data.ShardHeaderHandler)
	if !ok {
		log.Warn("cannot convert data.HeaderHandler in data.ShardHeaderHandler")
		return
	}

	log.Debug("received shard header from network in block tracker",
		"shard", shardHeader.GetShardID(),
		"epoch", shardHeader.GetEpoch(),
		"round", shardHeader.GetRound(),
		"nonce", shardHeader.GetNonce(),
		"hash", shardHeaderHash,
	)

	if !bbt.ShouldAddHeader(headerHandler) {
		log.Trace("received shard header is out of range", "nonce", headerHandler.GetNonce())
		return
	}

	if !bbt.addHeader(shardHeader, shardHeaderHash) {
		log.Trace("received shard header was not added", "nonce", headerHandler.GetNonce())
		return
	}

	bbt.blockProcessor.ProcessReceivedHeader(shardHeader)
}

// ShouldAddHeader returns if the given header should be added or not in the tracker list (is out of the interest range)
func (bbt *baseBlockTrack) ShouldAddHeader(headerHandler data.HeaderHandler) bool {
	return bbt.shouldAddHeader(headerHandler, bbt.selfNotarizer)
}

func (bbt *baseBlockTrack) shouldAddHeader(
	headerHandler data.HeaderHandler,
	blockNotarizer blockNotarizerHandler,
) bool {
	lastNotarizedHeader, _, err := blockNotarizer.GetLastNotarizedHeader()
	if err != nil {
		log.Debug("shouldAddHeader.GetLastNotarizedHeader",
			"shard", headerHandler.GetShardID(),
			"error", err.Error())
		return false
	}

	lastNotarizedHeaderNonce := lastNotarizedHeader.GetNonce()

	isHeaderOutOfRange := headerHandler.GetNonce() > lastNotarizedHeaderNonce+uint64(bbt.maxNumHeadersToKeep)
	return !isHeaderOutOfRange
}

func (bbt *baseBlockTrack) addHeader(header data.HeaderHandler, hash []byte) bool {
	if check.IfNil(header) {
		return false
	}

	nonce := header.GetNonce()

	bbt.mutHeaders.Lock()
	if bbt.headers == nil {
		bbt.headers = make(map[uint64][]*HeaderInfo)
	}

	for _, hdrInfo := range bbt.headers[nonce] {
		if bytes.Equal(hdrInfo.Hash, hash) {
			bbt.mutHeaders.Unlock()
			return false
		}
	}

	bbt.headers[nonce] = append(bbt.headers[nonce], &HeaderInfo{Hash: hash, Header: header})
	bbt.mutHeaders.Unlock()

	return true
}

// AddSelfNotarizedHeader adds self notarized headers to the tracker lists
func (bbt *baseBlockTrack) AddSelfNotarizedHeader(
	selfNotarizedHeader data.HeaderHandler,
	selfNotarizedHeaderHash []byte,
) {
	bbt.selfNotarizer.AddNotarizedHeader(selfNotarizedHeader, selfNotarizedHeaderHash)
}

// AddTrackedHeader adds tracked headers to the tracker lists
func (bbt *baseBlockTrack) AddTrackedHeader(header data.HeaderHandler, hash []byte) {
	bbt.receivedHeader(header, hash)
}

// CleanupHeadersBehindNonce removes from local pools old headers
func (bbt *baseBlockTrack) CleanupHeadersBehindNonce(
	selfNotarizedNonce uint64,
) {
	bbt.selfNotarizer.CleanupNotarizedHeadersBehindNonce(selfNotarizedNonce)
	bbt.cleanupTrackedHeadersBehindNonce(selfNotarizedNonce)
}

func (bbt *baseBlockTrack) cleanupTrackedHeadersBehindNonce(nonce uint64) {
	if nonce == 0 {
		return
	}

	bbt.mutHeaders.Lock()
	defer bbt.mutHeaders.Unlock()

	if bbt.headers == nil {
		return
	}

	for headersNonce := range bbt.headers {
		if headersNonce < nonce {
			delete(bbt.headers, headersNonce)
		}
	}
}

// ComputeLongestChain returns the longest valid chain from a given header
func (bbt *baseBlockTrack) ComputeLongestChain(header data.HeaderHandler) ([]data.HeaderHandler, [][]byte) {
	return bbt.blockProcessor.ComputeLongestChain(header)
}

// ComputeLongestChainFromLastSelfNotarized returns the longest valid chain from the last self notarized header
func (bbt *baseBlockTrack) ComputeLongestChainFromLastSelfNotarized() ([]data.HeaderHandler, [][]byte, []data.HeaderHandler, error) {

	lastSelfNotarizedHeader, _, err := bbt.GetLastSelfNotarizedHeader()
	if err != nil {
		return nil, nil, nil, err
	}

	headers, headersHashes := bbt.ComputeLongestChain(lastSelfNotarizedHeader)

	return headers, headersHashes, headers, nil
}

// DisplayTrackedHeaders displays tracked headers
func (bbt *baseBlockTrack) DisplayTrackedHeaders() {
		bbt.displayHeaders()
}

func (bbt *baseBlockTrack) displayHeaders() {
	bbt.displayTrackedHeaders( "tracked headers")
	bbt.selfNotarizer.DisplayNotarizedHeaders("self notarized headers")
}

func (bbt *baseBlockTrack) displayTrackedHeaders(message string) {
	headers, hashes := bbt.SortHeadersFromNonce( 0)
	shouldNotDisplay := len(headers) == 0 ||
		len(headers) == 1 && headers[0].GetNonce() == 0
	if shouldNotDisplay {
		return
	}

	log.Debug(message,
		"nb", len(headers))

	for index, header := range headers {
		log.Trace("tracked header info",
			"round", header.GetRound(),
			"nonce", header.GetNonce(),
			"hash", hashes[index])
	}
}

// CheckBlockAgainstRoundHandler verifies the provided header against the roundHandler's current round
func (bbt *baseBlockTrack) CheckBlockAgainstRoundHandler(headerHandler data.HeaderHandler) error {
	if check.IfNil(headerHandler) {
		return process.ErrNilHeaderHandler
	}

	nextRound := bbt.roundHandler.Index() + 1
	if int64(headerHandler.GetRound()) > nextRound {
		return fmt.Errorf("%w header round: %d, next chronology round: %d",
			process.ErrHigherRoundInBlock,
			headerHandler.GetRound(),
			nextRound)
	}

	return nil
}

// CheckBlockAgainstFinal checks if the given header is valid related to the final header
func (bbt *baseBlockTrack) CheckBlockAgainstFinal(headerHandler data.HeaderHandler) error {
	if check.IfNil(headerHandler) {
		return process.ErrNilHeaderHandler
	}

	finalHeader, _, err := bbt.getFinalHeader()
	if err != nil {
		return fmt.Errorf("%w: header shard: %d, header round: %d, header nonce: %d",
			err,
			headerHandler.GetShardID(),
			headerHandler.GetRound(),
			headerHandler.GetNonce())
	}

	roundDif := int64(headerHandler.GetRound()) - int64(finalHeader.GetRound())
	nonceDif := int64(headerHandler.GetNonce()) - int64(finalHeader.GetNonce())

	if roundDif < 0 {
		return fmt.Errorf("%w for header round: %d, final header round: %d",
			process.ErrLowerRoundInBlock,
			headerHandler.GetRound(),
			finalHeader.GetRound())
	}
	if nonceDif < 0 {
		return fmt.Errorf("%w for header nonce: %d, final header nonce: %d",
			process.ErrLowerNonceInBlock,
			headerHandler.GetNonce(),
			finalHeader.GetNonce())
	}
	if roundDif < nonceDif {
		return fmt.Errorf("%w for "+
			"header round: %d, final header round: %d, round dif: %d"+
			"header nonce: %d, final header nonce: %d, nonce dif: %d",
			process.ErrHigherNonceInBlock,
			headerHandler.GetRound(),
			finalHeader.GetRound(),
			roundDif,
			headerHandler.GetNonce(),
			finalHeader.GetNonce(),
			nonceDif)
	}

	return nil
}

func (bbt *baseBlockTrack) getFinalHeader() (data.HeaderHandler, []byte, error) {
	return bbt.selfNotarizer.GetFirstNotarizedHeader()
}

// CheckBlockAgainstWhitelist returns if the provided intercepted data (blocks) is whitelisted or not
func (bbt *baseBlockTrack) CheckBlockAgainstWhitelist(interceptedData process.InterceptedData) bool {
	return bbt.whitelistHandler.IsWhiteListed(interceptedData)
}

// GetLastSelfNotarizedHeader returns last self notarized header
func (bbt *baseBlockTrack) GetLastSelfNotarizedHeader() (data.HeaderHandler, []byte, error) {
	return bbt.selfNotarizer.GetLastNotarizedHeader()
}

// GetSelfNotarizedHeader returns a self notarized header with a given offset, behind last self notarized header
func (bbt *baseBlockTrack) GetSelfNotarizedHeader(offset uint64) (data.HeaderHandler, []byte, error) {
	return bbt.selfNotarizer.GetNotarizedHeader(offset)
}

// GetTrackedHeaders returns tracked headers
func (bbt *baseBlockTrack) GetTrackedHeaders() ([]data.HeaderHandler, [][]byte) {
	return bbt.SortHeadersFromNonce(0)
}

// GetTrackedHeadersForAllShards returns tracked headers
func (bbt *baseBlockTrack) GetTrackedHeadersForAllShards() []data.HeaderHandler {
	trackedHeaders, _ := bbt.GetTrackedHeaders()
	return trackedHeaders
}

// SortHeadersFromNonce gets sorted tracked headers from a given nonce
func (bbt *baseBlockTrack) SortHeadersFromNonce(nonce uint64) ([]data.HeaderHandler, [][]byte) {
	bbt.mutHeaders.RLock()
	defer bbt.mutHeaders.RUnlock()

	if bbt.headers == nil {
		return nil, nil
	}

	sortedHeadersInfo := make([]*HeaderInfo, 0)
	for headersNonce, headersInfo := range bbt.headers {
		if headersNonce < nonce {
			continue
		}

		sortedHeadersInfo = append(sortedHeadersInfo, headersInfo...)
	}

	if len(sortedHeadersInfo) > 1 {
		sort.Slice(sortedHeadersInfo, func(i, j int) bool {
			if sortedHeadersInfo[i].Header.GetNonce() == sortedHeadersInfo[j].Header.GetNonce() {
				return sortedHeadersInfo[i].Header.GetRound() < sortedHeadersInfo[j].Header.GetRound()
			}

			return sortedHeadersInfo[i].Header.GetNonce() < sortedHeadersInfo[j].Header.GetNonce()
		})
	}

	headers := make([]data.HeaderHandler, 0)
	headersHashes := make([][]byte, 0)

	for _, hdrInfo := range sortedHeadersInfo {
		headers = append(headers, hdrInfo.Header)
		headersHashes = append(headersHashes, hdrInfo.Hash)
	}

	return headers, headersHashes
}

// AddHeaderFromPool adds into tracker pool header with the given nonce if it exists in headers pool
func (bbt *baseBlockTrack) AddHeaderFromPool(nonce uint64) {
	headers, hashes, err := bbt.headersPool.GetHeadersByNonceAndShardId(nonce, bbt.shardCoordinator.SelfId())
	if err != nil {
		log.Trace("baseBlockTrack.AddHeaderFromPool", "error", err.Error())
		return
	}

	for i := 0; i < len(headers); i++ {
		bbt.AddTrackedHeader(headers[i], hashes[i])
	}
}

// GetTrackedHeadersWithNonce returns tracked headers for a given nonce
func (bbt *baseBlockTrack) GetTrackedHeadersWithNonce(nonce uint64) ([]data.HeaderHandler, [][]byte) {
	bbt.mutHeaders.RLock()
	defer bbt.mutHeaders.RUnlock()

	if bbt.headers == nil {
		return nil, nil
	}

	headersWithNonce, ok := bbt.headers[nonce]
	if !ok {
		return nil, nil
	}

	headers := make([]data.HeaderHandler, 0)
	headersHashes := make([][]byte, 0)

	for _, hdrInfo := range headersWithNonce {
		headers = append(headers, hdrInfo.Header)
		headersHashes = append(headersHashes, hdrInfo.Hash)
	}

	return headers, headersHashes
}

// RegisterSelfNotarizedHeadersHandler registers a new handler to be called when self notarized header is changed
func (bbt *baseBlockTrack) RegisterSelfNotarizedHeadersHandler(
	handler func(headers []data.HeaderHandler, headersHashes [][]byte),
) {
	bbt.selfNotarizedHeadersNotifier.RegisterHandler(handler)
}

// RemoveLastNotarizedHeaders removes last notarized headers from tracker list
func (bbt *baseBlockTrack) RemoveLastNotarizedHeaders() {
	bbt.selfNotarizer.RemoveLastNotarizedHeader()
}

// RestoreToGenesis sets class variables to theirs initial values
func (bbt *baseBlockTrack) RestoreToGenesis() {
	bbt.selfNotarizer.RestoreNotarizedHeadersToGenesis()
	bbt.restoreTrackedHeadersToGenesis()
}

func (bbt *baseBlockTrack) restoreTrackedHeadersToGenesis() {
	bbt.mutHeaders.Lock()
	bbt.headers = make(map[uint64][]*HeaderInfo)
	bbt.mutHeaders.Unlock()
}

// IsInterfaceNil returns true if there is no value under the interface
func (bbt *baseBlockTrack) IsInterfaceNil() bool {
	return bbt == nil
}

func checkTrackerNilParameters(arguments ArgBaseTracker) error {
	if check.IfNil(arguments.Hasher) {
		return process.ErrNilHasher
	}
	if check.IfNil(arguments.HeaderValidator) {
		return process.ErrNilHeaderValidator
	}
	if check.IfNil(arguments.Marshalizer) {
		return process.ErrNilMarshalizer
	}
	if check.IfNil(arguments.RequestHandler) {
		return process.ErrNilRequestHandler
	}
	if check.IfNil(arguments.RoundHandler) {
		return process.ErrNilRoundHandler
	}
	if check.IfNil(arguments.ShardCoordinator) {
		return process.ErrNilShardCoordinator
	}
	if check.IfNil(arguments.Store) {
		return process.ErrNilStorage
	}
	if check.IfNil(arguments.PoolsHolder) {
		return process.ErrNilPoolsHolder
	}
	if check.IfNil(arguments.PoolsHolder.Headers()) {
		return process.ErrNilHeadersDataPool
	}
	if check.IfNil(arguments.FeeHandler) {
		return process.ErrNilEconomicsData
	}

	return nil
}

func (bbt *baseBlockTrack) initNotarizedHeaders(startHeader data.HeaderHandler) error {
	err := bbt.selfNotarizer.InitNotarizedHeaders(startHeader)
	if err != nil {
		return err
	}

	return nil
}

func (bbt *baseBlockTrack) isHeaderOutOfRange(headerHandler data.HeaderHandler) bool {
	lastSelfNotarizedHeader, _, err := bbt.GetLastSelfNotarizedHeader()
	if err != nil {
		log.Debug("isHeaderOutOfRange.GetLastSelfNotarizedHeader",
			"shard", headerHandler.GetShardID(),
			"error", err.Error())
		return true
	}

	isHeaderOutOfRange := headerHandler.GetNonce() > lastSelfNotarizedHeader.GetNonce()+process.MaxHeadersToWhitelistInAdvance
	return isHeaderOutOfRange
}
