package block

import (
	"bytes"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/multiversx/mx-chain-core-go/core"
	"github.com/multiversx/mx-chain-core-go/core/check"
	"github.com/multiversx/mx-chain-core-go/data"
	"github.com/multiversx/mx-chain-core-go/data/block"
	"github.com/multiversx/mx-chain-core-go/data/scheduled"
	"github.com/multiversx/mx-chain-core-go/data/typeConverters"
	"github.com/multiversx/mx-chain-core-go/hashing"
	"github.com/multiversx/mx-chain-core-go/marshal"
	nodeFactory "github.com/multiversx/mx-chain-go/cmd/node/factory"
	"github.com/multiversx/mx-chain-go/common"
	"github.com/multiversx/mx-chain-go/common/holders"
	"github.com/multiversx/mx-chain-go/common/logging"
	"github.com/multiversx/mx-chain-go/consensus"
	"github.com/multiversx/mx-chain-go/dataRetriever"
	"github.com/multiversx/mx-chain-go/dblookupext"
	"github.com/multiversx/mx-chain-go/outport"
	"github.com/multiversx/mx-chain-go/process"
	"github.com/multiversx/mx-chain-go/process/block/bootstrapStorage"
	"github.com/multiversx/mx-chain-go/process/block/processedMb"
	"github.com/multiversx/mx-chain-go/sharding"
	"github.com/multiversx/mx-chain-go/sharding/nodesCoordinator"
	"github.com/multiversx/mx-chain-go/state"
	logger "github.com/multiversx/mx-chain-logger-go"
)

var log = logger.GetOrCreate("process/block")

type hashAndHdr struct {
	hdr  data.HeaderHandler
	hash []byte
}

type nonceAndHashInfo struct {
	hash  []byte
	nonce uint64
}

type hdrInfo struct {
	usedInBlock bool
	hdr         data.HeaderHandler
}

type baseProcessor struct {
	shardCoordinator        sharding.Coordinator
	nodesCoordinator        nodesCoordinator.NodesCoordinator
	accountsDB              map[state.AccountsDbIdentifier]state.AccountsAdapter
	forkDetector            process.ForkDetector
	hasher                  hashing.Hasher
	marshalizer             marshal.Marshalizer
	store                   dataRetriever.StorageService
	uint64Converter         typeConverters.Uint64ByteSliceConverter
	blockSizeThrottler      process.BlockSizeThrottler
	epochStartTrigger       process.EpochStartTriggerHandler
	headerValidator         process.HeaderConstructionValidator
	blockChainHook          process.BlockChainHookHandler
	txCoordinator           process.TransactionCoordinator
	roundHandler            consensus.RoundHandler
	bootStorer              process.BootStorer
	requestBlockBodyHandler process.RequestBlockBodyHandler
	requestHandler          process.RequestHandler
	blockTracker            process.BlockTracker
	dataPool                dataRetriever.PoolsHolder
	feeHandler              process.TransactionFeeHandler
	blockChain              data.ChainHandler
	hdrsForCurrBlock        *hdrForBlock
	genesisNonce            uint64

	versionedHeaderFactory       nodeFactory.VersionedHeaderFactory
	headerIntegrityVerifier      process.HeaderIntegrityVerifier
	scheduledTxsExecutionHandler process.ScheduledTxsExecutionHandler

	appStatusHandler       core.AppStatusHandler
	stateCheckpointModulus uint
	txCounter              *transactionCounter

	outportHandler      outport.OutportHandler
	historyRepo         dblookupext.HistoryRepository
	epochNotifier       process.EpochNotifier
	roundNotifier       process.RoundNotifier
	vmContainerFactory  process.VirtualMachinesContainerFactory
	vmContainer         process.VirtualMachinesContainer
	gasConsumedProvider gasConsumedProvider
	economicsData       process.EconomicsDataHandler

	processDataTriesOnCommitEpoch bool
	lastRestartNonce              uint64
	pruningDelay                  uint32
	processedMiniBlocksTracker    process.ProcessedMiniBlocksTracker
	receiptsRepository            receiptsRepository
}

type bootStorerDataArgs struct {
	headerInfo                 bootstrapStorage.BootstrapHeaderInfo
	lastSelfNotarizedHeaders   []bootstrapStorage.BootstrapHeaderInfo
	round                      uint64
	highestFinalBlockNonce     uint64
	pendingMiniBlocks          []bootstrapStorage.PendingMiniBlocksInfo
	processedMiniBlocks        []bootstrapStorage.MiniBlocksInMeta
	nodesCoordinatorConfigKey  []byte
	epochStartTriggerConfigKey []byte
}

func checkForNils(
	headerHandler data.HeaderHandler,
	bodyHandler data.BodyHandler,
) error {
	if check.IfNil(headerHandler) {
		return process.ErrNilBlockHeader
	}
	if check.IfNil(bodyHandler) {
		return process.ErrNilBlockBody
	}
	return nil
}

// checkBlockValidity method checks if the given block is valid
func (bp *baseProcessor) checkBlockValidity(
	headerHandler data.HeaderHandler,
	bodyHandler data.BodyHandler,
) error {

	err := checkForNils(headerHandler, bodyHandler)
	if err != nil {
		return err
	}

	currentBlockHeader := bp.blockChain.GetCurrentBlockHeader()

	if check.IfNil(currentBlockHeader) {
		if headerHandler.GetNonce() == bp.genesisNonce+1 { // first block after genesis
			if bytes.Equal(headerHandler.GetPrevHash(), bp.blockChain.GetGenesisHeaderHash()) {
				// TODO: add genesis block verification
				return nil
			}

			log.Debug("hash does not match",
				"local block hash", bp.blockChain.GetGenesisHeaderHash(),
				"received previous hash", headerHandler.GetPrevHash())

			return process.ErrBlockHashDoesNotMatch
		}

		log.Debug("nonce does not match",
			"local block nonce", 0,
			"received nonce", headerHandler.GetNonce())

		return process.ErrWrongNonceInBlock
	}

	if headerHandler.GetRound() <= currentBlockHeader.GetRound() {
		log.Debug("round does not match",
			"local block round", currentBlockHeader.GetRound(),
			"received block round", headerHandler.GetRound())

		return process.ErrLowerRoundInBlock
	}

	if headerHandler.GetNonce() != currentBlockHeader.GetNonce()+1 {
		log.Debug("nonce does not match",
			"local block nonce", currentBlockHeader.GetNonce(),
			"received nonce", headerHandler.GetNonce())

		return process.ErrWrongNonceInBlock
	}

	if !bytes.Equal(headerHandler.GetPrevHash(), bp.blockChain.GetCurrentBlockHeaderHash()) {
		log.Debug("hash does not match",
			"local block hash", bp.blockChain.GetCurrentBlockHeaderHash(),
			"received previous hash", headerHandler.GetPrevHash())

		return process.ErrBlockHashDoesNotMatch
	}

	if !bytes.Equal(headerHandler.GetPrevRandSeed(), currentBlockHeader.GetRandSeed()) {
		log.Debug("random seed does not match",
			"local random seed", currentBlockHeader.GetRandSeed(),
			"received previous random seed", headerHandler.GetPrevRandSeed())

		return process.ErrRandSeedDoesNotMatch
	}

	// verification of epoch
	if headerHandler.GetEpoch() < currentBlockHeader.GetEpoch() {
		return process.ErrEpochDoesNotMatch
	}

	return nil
}

// verifyStateRoot verifies the state root hash given as parameter against the
// Merkle trie root hash stored for accounts and returns if equal or not
func (bp *baseProcessor) verifyStateRoot(rootHash []byte) bool {
	trieRootHash, err := bp.accountsDB[state.UserAccountsState].RootHash()
	if err != nil {
		log.Debug("verify account.RootHash", "error", err.Error())
	}

	return bytes.Equal(trieRootHash, rootHash)
}

// getRootHash returns the accounts merkle tree root hash
func (bp *baseProcessor) getRootHash() []byte {
	rootHash, err := bp.accountsDB[state.UserAccountsState].RootHash()
	if err != nil {
		log.Trace("get account.RootHash", "error", err.Error())
	}

	return rootHash
}

// checkProcessorNilParameters will check the input parameters for nil values
func checkProcessorNilParameters(arguments ArgBaseProcessor) error {

	for key := range arguments.AccountsDB {
		if check.IfNil(arguments.AccountsDB[key]) {
			return process.ErrNilAccountsAdapter
		}
	}
	if check.IfNil(arguments.DataComponents) {
		return process.ErrNilDataComponentsHolder
	}
	if check.IfNil(arguments.CoreComponents) {
		return process.ErrNilCoreComponentsHolder
	}
	if check.IfNil(arguments.BootstrapComponents) {
		return process.ErrNilBootstrapComponentsHolder
	}
	if check.IfNil(arguments.StatusComponents) {
		return process.ErrNilStatusComponentsHolder
	}
	if check.IfNil(arguments.ForkDetector) {
		return process.ErrNilForkDetector
	}
	if check.IfNil(arguments.CoreComponents.Hasher()) {
		return process.ErrNilHasher
	}
	if check.IfNil(arguments.CoreComponents.InternalMarshalizer()) {
		return process.ErrNilMarshalizer
	}
	if check.IfNil(arguments.DataComponents.StorageService()) {
		return process.ErrNilStorage
	}
	if check.IfNil(arguments.BootstrapComponents.ShardCoordinator()) {
		return process.ErrNilShardCoordinator
	}
	if check.IfNil(arguments.NodesCoordinator) {
		return process.ErrNilNodesCoordinator
	}
	if check.IfNil(arguments.CoreComponents.Uint64ByteSliceConverter()) {
		return process.ErrNilUint64Converter
	}
	if check.IfNil(arguments.RequestHandler) {
		return process.ErrNilRequestHandler
	}
	if check.IfNil(arguments.EpochStartTrigger) {
		return process.ErrNilEpochStartTrigger
	}
	if check.IfNil(arguments.CoreComponents.RoundHandler()) {
		return process.ErrNilRoundHandler
	}
	if check.IfNil(arguments.BootStorer) {
		return process.ErrNilStorage
	}
	if check.IfNil(arguments.BlockChainHook) {
		return process.ErrNilBlockChainHook
	}
	if check.IfNil(arguments.TxCoordinator) {
		return process.ErrNilTransactionCoordinator
	}
	if check.IfNil(arguments.HeaderValidator) {
		return process.ErrNilHeaderValidator
	}
	if check.IfNil(arguments.BlockTracker) {
		return process.ErrNilBlockTracker
	}
	if check.IfNil(arguments.FeeHandler) {
		return process.ErrNilEconomicsFeeHandler
	}
	if check.IfNil(arguments.DataComponents.Blockchain()) {
		return process.ErrNilBlockChain
	}
	if check.IfNil(arguments.BlockSizeThrottler) {
		return process.ErrNilBlockSizeThrottler
	}
	if check.IfNil(arguments.StatusComponents.OutportHandler()) {
		return process.ErrNilOutportHandler
	}
	if check.IfNil(arguments.HistoryRepository) {
		return process.ErrNilHistoryRepository
	}
	if check.IfNil(arguments.BootstrapComponents.HeaderIntegrityVerifier()) {
		return process.ErrNilHeaderIntegrityVerifier
	}
	if check.IfNil(arguments.EpochNotifier) {
		return process.ErrNilEpochNotifier
	}
	if check.IfNil(arguments.RoundNotifier) {
		return process.ErrNilRoundNotifier
	}
	if check.IfNil(arguments.CoreComponents.StatusHandler()) {
		return process.ErrNilAppStatusHandler
	}
	if check.IfNil(arguments.GasHandler) {
		return process.ErrNilGasHandler
	}
	if check.IfNil(arguments.CoreComponents.EconomicsData()) {
		return process.ErrNilEconomicsData
	}
	if check.IfNil(arguments.ScheduledTxsExecutionHandler) {
		return process.ErrNilScheduledTxsExecutionHandler
	}
	if check.IfNil(arguments.BootstrapComponents.VersionedHeaderFactory()) {
		return process.ErrNilVersionedHeaderFactory
	}
	if check.IfNil(arguments.ProcessedMiniBlocksTracker) {
		return process.ErrNilProcessedMiniBlocksTracker
	}
	if check.IfNil(arguments.ReceiptsRepository) {
		return process.ErrNilReceiptsRepository
	}

	return nil
}

func (bp *baseProcessor) createBlockStarted() error {
	bp.hdrsForCurrBlock.resetMissingHdrs()
	bp.hdrsForCurrBlock.initMaps()
	scheduledGasAndFees := bp.scheduledTxsExecutionHandler.GetScheduledGasAndFees()
	bp.txCoordinator.CreateBlockStarted()
	bp.feeHandler.CreateBlockStarted(scheduledGasAndFees)

	err := bp.txCoordinator.AddIntermediateTransactions(bp.scheduledTxsExecutionHandler.GetScheduledIntermediateTxs())
	if err != nil {
		return err
	}

	return nil
}

func (bp *baseProcessor) verifyFees(header data.HeaderHandler) error {
	if header.GetAccumulatedFees().Cmp(bp.feeHandler.GetAccumulatedFees()) != 0 {
		return process.ErrAccumulatedFeesDoNotMatch
	}
	if header.GetDeveloperFees().Cmp(bp.feeHandler.GetDeveloperFees()) != 0 {
		return process.ErrDeveloperFeesDoNotMatch
	}

	return nil
}

// TODO: remove bool parameter and give instead the set to sort
func (bp *baseProcessor) sortHeadersForCurrentBlockByNonce(usedInBlock bool) map[uint32][]data.HeaderHandler {
	hdrsForCurrentBlock := make(map[uint32][]data.HeaderHandler)

	bp.hdrsForCurrBlock.mutHdrsForBlock.RLock()
	for _, headerInfo := range bp.hdrsForCurrBlock.hdrHashAndInfo {
		if headerInfo.usedInBlock != usedInBlock {
			continue
		}

		hdrsForCurrentBlock[headerInfo.hdr.GetShardID()] = append(hdrsForCurrentBlock[headerInfo.hdr.GetShardID()], headerInfo.hdr)
	}
	bp.hdrsForCurrBlock.mutHdrsForBlock.RUnlock()

	// sort headers for each shard
	for _, hdrsForShard := range hdrsForCurrentBlock {
		process.SortHeadersByNonce(hdrsForShard)
	}

	return hdrsForCurrentBlock
}

func (bp *baseProcessor) sortHeaderHashesForCurrentBlockByNonce(usedInBlock bool) map[uint32][][]byte {
	hdrsForCurrentBlockInfo := make(map[uint32][]*nonceAndHashInfo)

	bp.hdrsForCurrBlock.mutHdrsForBlock.RLock()
	for metaBlockHash, headerInfo := range bp.hdrsForCurrBlock.hdrHashAndInfo {
		if headerInfo.usedInBlock != usedInBlock {
			continue
		}

		hdrsForCurrentBlockInfo[headerInfo.hdr.GetShardID()] = append(hdrsForCurrentBlockInfo[headerInfo.hdr.GetShardID()],
			&nonceAndHashInfo{nonce: headerInfo.hdr.GetNonce(), hash: []byte(metaBlockHash)})
	}
	bp.hdrsForCurrBlock.mutHdrsForBlock.RUnlock()

	for _, hdrsForShard := range hdrsForCurrentBlockInfo {
		if len(hdrsForShard) > 1 {
			sort.Slice(hdrsForShard, func(i, j int) bool {
				return hdrsForShard[i].nonce < hdrsForShard[j].nonce
			})
		}
	}

	hdrsHashesForCurrentBlock := make(map[uint32][][]byte, len(hdrsForCurrentBlockInfo))
	for shardId, hdrsForShard := range hdrsForCurrentBlockInfo {
		for _, hdrForShard := range hdrsForShard {
			hdrsHashesForCurrentBlock[shardId] = append(hdrsHashesForCurrentBlock[shardId], hdrForShard.hash)
		}
	}

	return hdrsHashesForCurrentBlock
}

func (bp *baseProcessor) createMiniBlockHeaderHandlers(
	body *block.Body,
	processedMiniBlocksDestMeInfo map[string]*processedMb.ProcessedMiniBlockInfo,
) (int, []data.MiniBlockHeaderHandler, error) {
	if len(body.MiniBlocks) == 0 {
		return 0, nil, nil
	}

	totalTxCount := 0
	miniBlockHeaderHandlers := make([]data.MiniBlockHeaderHandler, len(body.MiniBlocks))

	for i := 0; i < len(body.MiniBlocks); i++ {
		txCount := len(body.MiniBlocks[i].TxHashes)
		totalTxCount += txCount

		miniBlockHash, err := core.CalculateHash(bp.marshalizer, bp.hasher, body.MiniBlocks[i])
		if err != nil {
			return 0, nil, err
		}

		miniBlockHeaderHandlers[i] = &block.MiniBlockHeader{
			Hash:            miniBlockHash,
			SenderShardID:   body.MiniBlocks[i].SenderShardID,
			ReceiverShardID: body.MiniBlocks[i].ReceiverShardID,
			TxCount:         uint32(txCount),
			Type:            body.MiniBlocks[i].Type,
		}

		err = bp.setMiniBlockHeaderReservedField(body.MiniBlocks[i], miniBlockHeaderHandlers[i], processedMiniBlocksDestMeInfo)
		if err != nil {
			return 0, nil, err
		}
	}

	return totalTxCount, miniBlockHeaderHandlers, nil
}

func (bp *baseProcessor) setMiniBlockHeaderReservedField(
	miniBlock *block.MiniBlock,
	miniBlockHeaderHandler data.MiniBlockHeaderHandler,
	processedMiniBlocksDestMeInfo map[string]*processedMb.ProcessedMiniBlockInfo,
) error {
	if !bp.flagScheduledMiniBlocks.IsSet() {
		return nil
	}

	err := bp.setIndexOfFirstTxProcessed(miniBlockHeaderHandler)
	if err != nil {
		return err
	}

	err = bp.setIndexOfLastTxProcessed(miniBlockHeaderHandler, processedMiniBlocksDestMeInfo)
	if err != nil {
		return err
	}

	notEmpty := len(miniBlock.TxHashes) > 0
	isScheduledMiniBlock := notEmpty && bp.scheduledTxsExecutionHandler.IsScheduledTx(miniBlock.TxHashes[0])
	if isScheduledMiniBlock {
		return bp.setProcessingTypeAndConstructionStateForScheduledMb(miniBlockHeaderHandler, processedMiniBlocksDestMeInfo)
	}

	return bp.setProcessingTypeAndConstructionStateForNormalMb(miniBlockHeaderHandler, processedMiniBlocksDestMeInfo)
}

func (bp *baseProcessor) setIndexOfFirstTxProcessed(miniBlockHeaderHandler data.MiniBlockHeaderHandler) error {
	processedMiniBlockInfo, _ := bp.processedMiniBlocksTracker.GetProcessedMiniBlockInfo(miniBlockHeaderHandler.GetHash())
	return miniBlockHeaderHandler.SetIndexOfFirstTxProcessed(processedMiniBlockInfo.IndexOfLastTxProcessed + 1)
}

func (bp *baseProcessor) setIndexOfLastTxProcessed(
	miniBlockHeaderHandler data.MiniBlockHeaderHandler,
	processedMiniBlocksDestMeInfo map[string]*processedMb.ProcessedMiniBlockInfo,
) error {
	processedMiniBlockInfo := processedMiniBlocksDestMeInfo[string(miniBlockHeaderHandler.GetHash())]
	if processedMiniBlockInfo != nil {
		return miniBlockHeaderHandler.SetIndexOfLastTxProcessed(processedMiniBlockInfo.IndexOfLastTxProcessed)
	}

	return miniBlockHeaderHandler.SetIndexOfLastTxProcessed(int32(miniBlockHeaderHandler.GetTxCount()) - 1)
}

func (bp *baseProcessor) setProcessingTypeAndConstructionStateForScheduledMb(
	miniBlockHeaderHandler data.MiniBlockHeaderHandler,
	processedMiniBlocksDestMeInfo map[string]*processedMb.ProcessedMiniBlockInfo,
) error {
	err := miniBlockHeaderHandler.SetProcessingType(int32(block.Scheduled))
	if err != nil {
		return err
	}

	if miniBlockHeaderHandler.GetSenderShardID() == bp.shardCoordinator.SelfId() {
		err = miniBlockHeaderHandler.SetConstructionState(int32(block.Proposed))
		if err != nil {
			return err
		}
	} else {
		constructionState := getConstructionState(miniBlockHeaderHandler, processedMiniBlocksDestMeInfo)
		err = miniBlockHeaderHandler.SetConstructionState(constructionState)
		if err != nil {
			return err
		}
	}
	return nil
}

func (bp *baseProcessor) setProcessingTypeAndConstructionStateForNormalMb(
	miniBlockHeaderHandler data.MiniBlockHeaderHandler,
	processedMiniBlocksDestMeInfo map[string]*processedMb.ProcessedMiniBlockInfo,
) error {
	if bp.scheduledTxsExecutionHandler.IsMiniBlockExecuted(miniBlockHeaderHandler.GetHash()) {
		err := miniBlockHeaderHandler.SetProcessingType(int32(block.Processed))
		if err != nil {
			return err
		}
	} else {
		err := miniBlockHeaderHandler.SetProcessingType(int32(block.Normal))
		if err != nil {
			return err
		}
	}

	constructionState := getConstructionState(miniBlockHeaderHandler, processedMiniBlocksDestMeInfo)
	err := miniBlockHeaderHandler.SetConstructionState(constructionState)
	if err != nil {
		return err
	}

	return nil
}

func getConstructionState(
	miniBlockHeaderHandler data.MiniBlockHeaderHandler,
	processedMiniBlocksDestMeInfo map[string]*processedMb.ProcessedMiniBlockInfo,
) int32 {
	constructionState := int32(block.Final)
	if isPartiallyExecuted(miniBlockHeaderHandler, processedMiniBlocksDestMeInfo) {
		constructionState = int32(block.PartialExecuted)
	}

	return constructionState
}

func isPartiallyExecuted(
	miniBlockHeaderHandler data.MiniBlockHeaderHandler,
	processedMiniBlocksDestMeInfo map[string]*processedMb.ProcessedMiniBlockInfo,
) bool {
	processedMiniBlockInfo := processedMiniBlocksDestMeInfo[string(miniBlockHeaderHandler.GetHash())]
	return processedMiniBlockInfo != nil && !processedMiniBlockInfo.FullyProcessed

}

// check if header has the same miniblocks as presented in body
func (bp *baseProcessor) checkHeaderBodyCorrelation(miniBlockHeaders []data.MiniBlockHeaderHandler, body *block.Body) error {
	mbHashesFromHdr := make(map[string]data.MiniBlockHeaderHandler, len(miniBlockHeaders))
	for i := 0; i < len(miniBlockHeaders); i++ {
		mbHashesFromHdr[string(miniBlockHeaders[i].GetHash())] = miniBlockHeaders[i]
	}

	if len(miniBlockHeaders) != len(body.MiniBlocks) {
		return process.ErrHeaderBodyMismatch
	}

	for i := 0; i < len(body.MiniBlocks); i++ {
		miniBlock := body.MiniBlocks[i]
		if miniBlock == nil {
			return process.ErrNilMiniBlock
		}

		mbHash, err := core.CalculateHash(bp.marshalizer, bp.hasher, miniBlock)
		if err != nil {
			return err
		}

		mbHdr, ok := mbHashesFromHdr[string(mbHash)]
		if !ok {
			return process.ErrHeaderBodyMismatch
		}

		if mbHdr.GetTxCount() != uint32(len(miniBlock.TxHashes)) {
			return process.ErrHeaderBodyMismatch
		}

		if mbHdr.GetReceiverShardID() != miniBlock.ReceiverShardID {
			return process.ErrHeaderBodyMismatch
		}

		if mbHdr.GetSenderShardID() != miniBlock.SenderShardID {
			return process.ErrHeaderBodyMismatch
		}

		err = process.CheckIfIndexesAreOutOfBound(mbHdr.GetIndexOfFirstTxProcessed(), mbHdr.GetIndexOfLastTxProcessed(), miniBlock)
		if err != nil {
			return err
		}

		err = checkConstructionStateAndIndexesCorrectness(mbHdr)
		if err != nil {
			return err
		}
	}

	return nil
}

func checkConstructionStateAndIndexesCorrectness(mbh data.MiniBlockHeaderHandler) error {
	if mbh.GetConstructionState() == int32(block.PartialExecuted) && mbh.GetIndexOfLastTxProcessed() == int32(mbh.GetTxCount())-1 {
		return process.ErrIndexDoesNotMatchWithPartialExecutedMiniBlock

	}
	if mbh.GetConstructionState() != int32(block.PartialExecuted) && mbh.GetIndexOfLastTxProcessed() != int32(mbh.GetTxCount())-1 {
		return process.ErrIndexDoesNotMatchWithFullyExecutedMiniBlock
	}

	return nil
}

func (bp *baseProcessor) cleanupPools(headerHandler data.HeaderHandler) {
	noncesToPrevFinal := bp.getNoncesToFinal(headerHandler) + 1
	bp.cleanupBlockTrackerPools(noncesToPrevFinal)

	highestPrevFinalBlockNonce := bp.forkDetector.GetHighestFinalBlockNonce()
	if highestPrevFinalBlockNonce > 0 {
		highestPrevFinalBlockNonce--
	}

	bp.removeHeadersBehindNonceFromPools(
		true,
		bp.shardCoordinator.SelfId(),
		highestPrevFinalBlockNonce)
}

func (bp *baseProcessor) removeHeadersBehindNonceFromPools(
	shouldRemoveBlockBody bool,
	shardId uint32,
	nonce uint64,
) {
	if nonce <= 1 {
		return
	}

	headersPool := bp.dataPool.Headers()
	nonces := headersPool.Nonces(shardId)
	for _, nonceFromCache := range nonces {
		if nonceFromCache >= nonce {
			continue
		}

		if shouldRemoveBlockBody {
			bp.removeBlocksBody(nonceFromCache, shardId)
		}

		headersPool.RemoveHeaderByNonceAndShardId(nonceFromCache, shardId)
	}
}

func (bp *baseProcessor) removeBlocksBody(nonce uint64, shardId uint32) {
	headersPool := bp.dataPool.Headers()
	headers, _, err := headersPool.GetHeadersByNonceAndShardId(nonce, shardId)
	if err != nil {
		return
	}

	for _, header := range headers {
		errNotCritical := bp.removeBlockBodyOfHeader(header)
		if errNotCritical != nil {
			log.Debug("RemoveBlockDataFromPool", "error", errNotCritical.Error())
		}
	}
}

func (bp *baseProcessor) removeBlockBodyOfHeader(headerHandler data.HeaderHandler) error {
	bodyHandler, err := bp.requestBlockBodyHandler.GetBlockBodyFromPool(headerHandler)
	if err != nil {
		return err
	}

	return bp.removeBlockDataFromPools(headerHandler, bodyHandler)
}

func (bp *baseProcessor) removeBlockDataFromPools(headerHandler data.HeaderHandler, bodyHandler data.BodyHandler) error {
	body, ok := bodyHandler.(*block.Body)
	if !ok {
		return process.ErrWrongTypeAssertion
	}

	err := bp.txCoordinator.RemoveBlockDataFromPool(body)
	if err != nil {
		return err
	}

	return nil
}

func (bp *baseProcessor) cleanupBlockTrackerPoolsForShard(shardID uint32, noncesToPrevFinal uint64) {
	selfNotarizedHeader, _, errSelfNotarized := bp.blockTracker.GetSelfNotarizedHeader(shardID, noncesToPrevFinal)
	if errSelfNotarized != nil {
		displayCleanupErrorMessage("cleanupBlockTrackerPoolsForShard.GetSelfNotarizedHeader",
			shardID,
			noncesToPrevFinal,
			errSelfNotarized)
		return
	}

	selfNotarizedNonce := selfNotarizedHeader.GetNonce()
	bp.blockTracker.CleanupHeadersBehindNonce(
		shardID,
		selfNotarizedNonce,
		selfNotarizedNonce,
	)

	log.Trace("cleanupBlockTrackerPoolsForShard.CleanupHeadersBehindNonce",
		"shard", shardID,
		"self notarized nonce", selfNotarizedNonce,
		"nonces to previous final", noncesToPrevFinal)
}

func (bp *baseProcessor) prepareDataForBootStorer(args bootStorerDataArgs) {
	bootData := bootstrapStorage.BootstrapData{
		LastHeader:                args.headerInfo,
		LastCrossNotarizedHeaders: args.lastSelfNotarizedHeaders,
		LastSelfNotarizedHeaders:  args.lastSelfNotarizedHeaders,
		HighestFinalBlockNonce:    args.highestFinalBlockNonce,
	}

	startTime := time.Now()

	err := bp.bootStorer.Put(int64(args.round), bootData)
	if err != nil {
		logging.LogErrAsWarnExceptAsDebugIfClosingError(log, err,
			"cannot save boot data in storage",
			"err", err)
	}

	elapsedTime := time.Since(startTime)
	if elapsedTime >= common.PutInStorerMaxTime {
		log.Warn("saveDataForBootStorer", "elapsed time", elapsedTime)
	}
}

func (bp *baseProcessor) getLastSelfNotarizedHeadersForShard(shardID uint32) *bootstrapStorage.BootstrapHeaderInfo {
	lastSelfNotarizedHeader, lastSelfNotarizedHeaderHash, err := bp.blockTracker.GetLastSelfNotarizedHeader(shardID)
	if err != nil {
		log.Warn("getLastSelfNotarizedHeadersForShard",
			"shard", shardID,
			"error", err.Error())
		return nil
	}

	if lastSelfNotarizedHeader.GetNonce() == 0 {
		return nil
	}

	headerInfo := &bootstrapStorage.BootstrapHeaderInfo{
		ShardId: lastSelfNotarizedHeader.GetShardID(),
		Nonce:   lastSelfNotarizedHeader.GetNonce(),
		Hash:    lastSelfNotarizedHeaderHash,
	}

	return headerInfo
}

func deleteSelfReceiptsMiniBlocks(body *block.Body) *block.Body {
	newBody := &block.Body{}
	for _, mb := range body.MiniBlocks {
		isInShardUnsignedMB := mb.ReceiverShardID == mb.SenderShardID &&
			(mb.Type == block.ReceiptBlock || mb.Type == block.SmartContractResultBlock)
		if isInShardUnsignedMB {
			continue
		}

		newBody.MiniBlocks = append(newBody.MiniBlocks, mb)
	}

	return newBody
}

func (bp *baseProcessor) getNoncesToFinal(headerHandler data.HeaderHandler) uint64 {
	currentBlockNonce := bp.genesisNonce
	if !check.IfNil(headerHandler) {
		currentBlockNonce = headerHandler.GetNonce()
	}

	noncesToFinal := uint64(0)
	finalBlockNonce := bp.forkDetector.GetHighestFinalBlockNonce()
	if currentBlockNonce > finalBlockNonce {
		noncesToFinal = currentBlockNonce - finalBlockNonce
	}

	return noncesToFinal
}

// DecodeBlockBody method decodes block body from a given byte array
func (bp *baseProcessor) DecodeBlockBody(dta []byte) data.BodyHandler {
	body := &block.Body{}
	if dta == nil {
		return body
	}

	err := bp.marshalizer.Unmarshal(body, dta)
	if err != nil {
		log.Debug("DecodeBlockBody.Unmarshal", "error", err.Error())
		return nil
	}

	return body
}

func (bp *baseProcessor) saveBody(body *block.Body, header data.HeaderHandler, headerHash []byte) {
	startTime := time.Now()

	bp.txCoordinator.SaveTxsToStorage(body)
	log.Trace("saveBody.SaveTxsToStorage", "time", time.Since(startTime))

	var errNotCritical error
	var marshalizedMiniBlock []byte
	for i := 0; i < len(body.MiniBlocks); i++ {
		marshalizedMiniBlock, errNotCritical = bp.marshalizer.Marshal(body.MiniBlocks[i])
		if errNotCritical != nil {
			log.Warn("saveBody.Marshal", "error", errNotCritical.Error())
			continue
		}

		miniBlockHash := bp.hasher.Compute(string(marshalizedMiniBlock))
		errNotCritical = bp.store.Put(dataRetriever.MiniBlockUnit, miniBlockHash, marshalizedMiniBlock)
		if errNotCritical != nil {
			logging.LogErrAsWarnExceptAsDebugIfClosingError(log, errNotCritical,
				"saveBody.Put -> MiniBlockUnit",
				"err", errNotCritical)
		}
		log.Trace("saveBody.Put -> MiniBlockUnit", "time", time.Since(startTime), "hash", miniBlockHash)
	}

	receiptsHolder := holders.NewReceiptsHolder(bp.txCoordinator.GetCreatedInShardMiniBlocks())
	errNotCritical = bp.receiptsRepository.SaveReceipts(receiptsHolder, header, headerHash)
	if errNotCritical != nil {
		logging.LogErrAsWarnExceptAsDebugIfClosingError(log, errNotCritical,
			"saveBody(), error on receiptsRepository.SaveReceipts()",
			"err", errNotCritical)
	}

	elapsedTime := time.Since(startTime)
	if elapsedTime >= common.PutInStorerMaxTime {
		log.Warn("saveBody", "elapsed time", elapsedTime)
	}
}

func (bp *baseProcessor) saveShardHeader(header data.HeaderHandler, headerHash []byte, marshalizedHeader []byte) {
	startTime := time.Now()

	nonceToByteSlice := bp.uint64Converter.ToByteSlice(header.GetNonce())
	hdrNonceHashDataUnit := dataRetriever.ShardHdrNonceHashDataUnit + dataRetriever.UnitType(header.GetShardID())

	errNotCritical := bp.store.Put(hdrNonceHashDataUnit, nonceToByteSlice, headerHash)
	if errNotCritical != nil {
		logging.LogErrAsWarnExceptAsDebugIfClosingError(log, errNotCritical,
			fmt.Sprintf("saveHeader.Put -> ShardHdrNonceHashDataUnit_%d", header.GetShardID()),
			"err", errNotCritical)
	}

	errNotCritical = bp.store.Put(dataRetriever.BlockHeaderUnit, headerHash, marshalizedHeader)
	if errNotCritical != nil {
		logging.LogErrAsWarnExceptAsDebugIfClosingError(log, errNotCritical,
			"saveHeader.Put -> BlockHeaderUnit",
			"err", errNotCritical)
	}

	elapsedTime := time.Since(startTime)
	if elapsedTime >= common.PutInStorerMaxTime {
		log.Warn("saveShardHeader", "elapsed time", elapsedTime)
	}
}

func getLastSelfNotarizedHeaderByItself(chainHandler data.ChainHandler) (data.HeaderHandler, []byte) {
	currentHeader := chainHandler.GetCurrentBlockHeader()
	if check.IfNil(currentHeader) {
		return chainHandler.GetGenesisHeader(), chainHandler.GetGenesisHeaderHash()
	}

	currentBlockHash := chainHandler.GetCurrentBlockHeaderHash()

	return currentHeader, currentBlockHash
}

func (bp *baseProcessor) setFinalizedHeaderHashInIndexer(hdrHash []byte) {
	log.Debug("baseProcessor.setFinalizedBlockInIndexer", "finalized header hash", hdrHash)

	bp.outportHandler.FinalizedBlock(hdrHash)
}

func (bp *baseProcessor) updateStateStorage(
	finalHeader data.HeaderHandler,
	currRootHash []byte,
	prevRootHash []byte,
	accounts state.AccountsAdapter,
	statePruningQueue core.Queue,
) {
	if !accounts.IsPruningEnabled() {
		return
	}

	// TODO generate checkpoint on a trigger
	if bp.stateCheckpointModulus != 0 {
		if finalHeader.GetNonce()%uint64(bp.stateCheckpointModulus) == 0 {
			log.Debug("trie checkpoint", "currRootHash", currRootHash)
			accounts.SetStateCheckpoint(currRootHash)
		}
	}

	if bytes.Equal(prevRootHash, currRootHash) {
		return
	}

	rootHashToBePruned := statePruningQueue.Add(prevRootHash)
	if len(rootHashToBePruned) == 0 {
		return
	}

	accounts.CancelPrune(rootHashToBePruned, state.NewRoot)
	accounts.PruneTrie(rootHashToBePruned, state.OldRoot, bp.getPruningHandler(finalHeader.GetNonce()))
}

// RevertCurrentBlock reverts the current block for cleanup failed process
func (bp *baseProcessor) RevertCurrentBlock() {
	bp.revertAccountState()
	bp.revertScheduledInfo()
}

func (bp *baseProcessor) revertAccountState() {
	for key := range bp.accountsDB {
		err := bp.accountsDB[key].RevertToSnapshot(0)
		if err != nil {
			log.Debug("RevertToSnapshot", "error", err.Error())
		}
	}
}

func (bp *baseProcessor) revertScheduledInfo() {
	header, headerHash := bp.getLastCommittedHeaderAndHash()
	err := bp.scheduledTxsExecutionHandler.RollBackToBlock(headerHash)
	if err != nil {
		log.Trace("baseProcessor.revertScheduledInfo", "error", err.Error())
		scheduledInfo := &process.ScheduledInfo{
			RootHash:        header.GetRootHash(),
			IntermediateTxs: make(map[block.Type][]data.TransactionHandler),
			GasAndFees:      process.GetZeroGasAndFees(),
			MiniBlocks:      make(block.MiniBlockSlice, 0),
		}
		bp.scheduledTxsExecutionHandler.SetScheduledInfo(scheduledInfo)
	}
}

func (bp *baseProcessor) getLastCommittedHeaderAndHash() (data.HeaderHandler, []byte) {
	headerHandler := bp.blockChain.GetCurrentBlockHeader()
	headerHash := bp.blockChain.GetCurrentBlockHeaderHash()
	if check.IfNil(headerHandler) {
		headerHandler = bp.blockChain.GetGenesisHeader()
		headerHash = bp.blockChain.GetGenesisHeaderHash()
	}

	return headerHandler, headerHash
}

// GetAccountsDBSnapshot returns the account snapshot
func (bp *baseProcessor) GetAccountsDBSnapshot() map[state.AccountsDbIdentifier]int {
	snapshots := make(map[state.AccountsDbIdentifier]int)
	for key := range bp.accountsDB {
		snapshots[key] = bp.accountsDB[key].JournalLen()
	}

	return snapshots
}

// RevertAccountsDBToSnapshot reverts the accountsDB to the given snapshot
func (bp *baseProcessor) RevertAccountsDBToSnapshot(accountsSnapshot map[state.AccountsDbIdentifier]int) {
	for key := range bp.accountsDB {
		err := bp.accountsDB[key].RevertToSnapshot(accountsSnapshot[key])
		if err != nil {
			log.Debug("RevertAccountsDBToSnapshot", "error", err.Error())
		}
	}
}

func (bp *baseProcessor) commit() error {
	for key := range bp.accountsDB {
		_, err := bp.accountsDB[key].Commit()
		if err != nil {
			return err
		}
	}

	return nil
}

// PruneStateOnRollback recreates the state tries to the root hashes indicated by the provided headers
func (bp *baseProcessor) PruneStateOnRollback(currHeader data.HeaderHandler, currHeaderHash []byte, prevHeader data.HeaderHandler, prevHeaderHash []byte) {
	for key := range bp.accountsDB {
		if !bp.accountsDB[key].IsPruningEnabled() {
			continue
		}

		rootHash, prevRootHash := bp.getRootHashes(currHeader, prevHeader, key)
		if bytes.Equal(rootHash, prevRootHash) {
			continue
		}

		bp.accountsDB[key].CancelPrune(prevRootHash, state.OldRoot)
		bp.accountsDB[key].PruneTrie(rootHash, state.NewRoot, bp.getPruningHandler(currHeader.GetNonce()))
	}
}

func (bp *baseProcessor) getPruningHandler(finalHeaderNonce uint64) state.PruningHandler {
	if finalHeaderNonce-bp.lastRestartNonce <= uint64(bp.pruningDelay) {
		log.Debug("will skip pruning",
			"finalHeaderNonce", finalHeaderNonce,
			"last restart nonce", bp.lastRestartNonce,
			"num blocks for pruning delay", bp.pruningDelay,
		)
		return state.NewPruningHandler(state.DisableDataRemoval)
	}

	return state.NewPruningHandler(state.EnableDataRemoval)
}

func (bp *baseProcessor) getRootHashes(currHeader data.HeaderHandler, prevHeader data.HeaderHandler, identifier state.AccountsDbIdentifier) ([]byte, []byte) {
	switch identifier {
	case state.UserAccountsState:
		return currHeader.GetRootHash(), prevHeader.GetRootHash()
	case state.PeerAccountsState:
		currMetaHeader, ok := currHeader.(data.MetaHeaderHandler)
		if !ok {
			return []byte{}, []byte{}
		}
		prevMetaHeader, ok := prevHeader.(data.MetaHeaderHandler)
		if !ok {
			return []byte{}, []byte{}
		}
		return currMetaHeader.GetValidatorStatsRootHash(), prevMetaHeader.GetValidatorStatsRootHash()
	default:
		return []byte{}, []byte{}
	}
}

func (bp *baseProcessor) displayMiniBlocksPool() {
	miniBlocksPool := bp.dataPool.MiniBlocks()

	for _, hash := range miniBlocksPool.Keys() {
		value, ok := miniBlocksPool.Get(hash)
		if !ok {
			log.Debug("displayMiniBlocksPool: mini block not found", "hash", logger.DisplayByteSlice(hash))
			continue
		}

		miniBlock, ok := value.(*block.MiniBlock)
		if !ok {
			log.Debug("displayMiniBlocksPool: wrong type assertion", "hash", logger.DisplayByteSlice(hash))
			continue
		}

		log.Trace("mini block in pool",
			"hash", logger.DisplayByteSlice(hash),
			"type", miniBlock.Type,
			"sender", miniBlock.SenderShardID,
			"receiver", miniBlock.ReceiverShardID,
			"num txs", len(miniBlock.TxHashes))
	}
}

func (bp *baseProcessor) restoreBlockBody(bodyHandler data.BodyHandler) {
	if check.IfNil(bodyHandler) {
		log.Debug("restoreMiniblocks nil bodyHandler")
		return
	}

	body, ok := bodyHandler.(*block.Body)
	if !ok {
		log.Debug("restoreMiniblocks wrong type assertion for bodyHandler")
		return
	}

	restoredTxNr, errNotCritical := bp.txCoordinator.RestoreBlockDataFromStorage(body)
	if errNotCritical != nil {
		log.Debug("restoreBlockBody RestoreBlockDataFromStorage", "error", errNotCritical.Error())
	}

	go bp.txCounter.subtractRestoredTxs(restoredTxNr)
}

// RestoreBlockBodyIntoPools restores the block body into associated pools
func (bp *baseProcessor) RestoreBlockBodyIntoPools(bodyHandler data.BodyHandler) error {
	if check.IfNil(bodyHandler) {
		return process.ErrNilBlockBody
	}

	body, ok := bodyHandler.(*block.Body)
	if !ok {
		return process.ErrWrongTypeAssertion
	}

	_, err := bp.txCoordinator.RestoreBlockDataFromStorage(body)
	if err != nil {
		return err
	}

	return nil
}

func (bp *baseProcessor) requestMiniBlocksIfNeeded(headerHandler data.HeaderHandler) {
	lastCrossNotarizedHeader, _, err := bp.blockTracker.GetLastCrossNotarizedHeader(headerHandler.GetShardID())
	if err != nil {
		log.Debug("requestMiniBlocksIfNeeded.GetLastCrossNotarizedHeader",
			"shard", headerHandler.GetShardID(),
			"error", err.Error())
		return
	}

	isHeaderOutOfRequestRange := headerHandler.GetNonce() > lastCrossNotarizedHeader.GetNonce()+process.MaxHeadersToRequestInAdvance
	if isHeaderOutOfRequestRange {
		return
	}

	waitTime := common.ExtraDelayForRequestBlockInfo
	roundDifferences := bp.roundHandler.Index() - int64(headerHandler.GetRound())
	if roundDifferences > 1 {
		waitTime = 0
	}

	// waiting for late broadcast of mini blocks and transactions to be done and received
	time.Sleep(waitTime)

	bp.txCoordinator.RequestMiniBlocks(headerHandler)
}

func (bp *baseProcessor) recordBlockInHistory(blockHeaderHash []byte, blockHeader data.HeaderHandler, blockBody data.BodyHandler) {
	scrResultsFromPool := bp.txCoordinator.GetAllCurrentUsedTxs(block.SmartContractResultBlock)
	receiptsFromPool := bp.txCoordinator.GetAllCurrentUsedTxs(block.ReceiptBlock)
	logs := bp.txCoordinator.GetAllCurrentLogs()
	intraMiniBlocks := bp.txCoordinator.GetCreatedInShardMiniBlocks()

	err := bp.historyRepo.RecordBlock(blockHeaderHash, blockHeader, blockBody, scrResultsFromPool, receiptsFromPool, intraMiniBlocks, logs)
	if err != nil {
		log.Error("historyRepo.RecordBlock()", "blockHeaderHash", blockHeaderHash, "error", err.Error())
	}
}

func (bp *baseProcessor) addHeaderIntoTrackerPool(nonce uint64, shardID uint32) {
	headersPool := bp.dataPool.Headers()
	headers, hashes, err := headersPool.GetHeadersByNonceAndShardId(nonce, shardID)
	if err != nil {
		log.Trace("baseProcessor.addHeaderIntoTrackerPool", "error", err.Error())
		return
	}

	for i := 0; i < len(headers); i++ {
		bp.blockTracker.AddTrackedHeader(headers[i], hashes[i])
	}
}

// Close - closes all underlying components
func (bp *baseProcessor) Close() error {
	var err1, err2 error
	if !check.IfNil(bp.vmContainer) {
		err1 = bp.vmContainer.Close()
	}
	if !check.IfNil(bp.vmContainerFactory) {
		err2 = bp.vmContainerFactory.Close()
	}
	if err1 != nil || err2 != nil {
		return fmt.Errorf("vmContainer close error: %v, vmContainerFactory close error: %v", err1, err2)
	}

	return nil
}

// ProcessScheduledBlock processes a scheduled block
func (bp *baseProcessor) ProcessScheduledBlock(headerHandler data.HeaderHandler, bodyHandler data.BodyHandler, haveTime func() time.Duration) error {
	var err error
	defer func() {
		if err != nil {
			bp.RevertCurrentBlock()
		}
	}()

	scheduledMiniBlocksFromMe, err := getScheduledMiniBlocksFromMe(headerHandler, bodyHandler)
	if err != nil {
		return err
	}

	bp.scheduledTxsExecutionHandler.AddScheduledMiniBlocks(scheduledMiniBlocksFromMe)

	normalProcessingGasAndFees := bp.getGasAndFees()

	startTime := time.Now()
	err = bp.scheduledTxsExecutionHandler.ExecuteAll(haveTime)
	elapsedTime := time.Since(startTime)
	log.Debug("elapsed time to execute all scheduled transactions",
		"time [s]", elapsedTime,
	)
	if err != nil {
		return err
	}

	rootHash, err := bp.accountsDB[state.UserAccountsState].RootHash()
	if err != nil {
		return err
	}

	_ = bp.txCoordinator.CreatePostProcessMiniBlocks()

	finalProcessingGasAndFees := bp.getGasAndFeesWithScheduled()

	scheduledProcessingGasAndFees := gasAndFeesDelta(normalProcessingGasAndFees, finalProcessingGasAndFees)
	bp.scheduledTxsExecutionHandler.SetScheduledRootHash(rootHash)
	bp.scheduledTxsExecutionHandler.SetScheduledGasAndFees(scheduledProcessingGasAndFees)

	return nil
}

func getScheduledMiniBlocksFromMe(headerHandler data.HeaderHandler, bodyHandler data.BodyHandler) (block.MiniBlockSlice, error) {
	body, ok := bodyHandler.(*block.Body)
	if !ok {
		return nil, process.ErrWrongTypeAssertion
	}

	if len(body.MiniBlocks) != len(headerHandler.GetMiniBlockHeaderHandlers()) {
		log.Warn("getScheduledMiniBlocksFromMe: num of mini blocks and mini blocks headers does not match", "num of mb", len(body.MiniBlocks), "num of mbh", len(headerHandler.GetMiniBlockHeaderHandlers()))
		return nil, process.ErrNumOfMiniBlocksAndMiniBlocksHeadersMismatch
	}

	miniBlocks := make(block.MiniBlockSlice, 0)
	for index, miniBlock := range body.MiniBlocks {
		miniBlockHeader := headerHandler.GetMiniBlockHeaderHandlers()[index]
		isScheduledMiniBlockFromMe := miniBlockHeader.GetSenderShardID() == headerHandler.GetShardID() && miniBlockHeader.GetProcessingType() == int32(block.Scheduled)
		if isScheduledMiniBlockFromMe {
			miniBlocks = append(miniBlocks, miniBlock)
		}

	}

	return miniBlocks, nil
}

func (bp *baseProcessor) getGasAndFees() scheduled.GasAndFees {
	return scheduled.GasAndFees{
		AccumulatedFees: bp.feeHandler.GetAccumulatedFees(),
		DeveloperFees:   bp.feeHandler.GetDeveloperFees(),
		GasProvided:     bp.gasConsumedProvider.TotalGasProvided(),
		GasPenalized:    bp.gasConsumedProvider.TotalGasPenalized(),
		GasRefunded:     bp.gasConsumedProvider.TotalGasRefunded(),
	}
}

func (bp *baseProcessor) getGasAndFeesWithScheduled() scheduled.GasAndFees {
	gasAndFees := bp.getGasAndFees()
	gasAndFees.GasProvided = bp.gasConsumedProvider.TotalGasProvidedWithScheduled()
	return gasAndFees
}

func gasAndFeesDelta(initialGasAndFees, finalGasAndFees scheduled.GasAndFees) scheduled.GasAndFees {
	zero := big.NewInt(0)
	result := process.GetZeroGasAndFees()

	deltaAccumulatedFees := big.NewInt(0).Sub(finalGasAndFees.AccumulatedFees, initialGasAndFees.AccumulatedFees)
	if deltaAccumulatedFees.Cmp(zero) < 0 {
		log.Error("gasAndFeesDelta",
			"initial accumulatedFees", initialGasAndFees.AccumulatedFees.String(),
			"final accumulatedFees", finalGasAndFees.AccumulatedFees.String(),
			"error", process.ErrNegativeValue)
		return result
	}

	deltaDevFees := big.NewInt(0).Sub(finalGasAndFees.DeveloperFees, initialGasAndFees.DeveloperFees)
	if deltaDevFees.Cmp(zero) < 0 {
		log.Error("gasAndFeesDelta",
			"initial devFees", initialGasAndFees.DeveloperFees.String(),
			"final devFees", finalGasAndFees.DeveloperFees.String(),
			"error", process.ErrNegativeValue)
		return result
	}

	deltaGasProvided := int64(finalGasAndFees.GasProvided) - int64(initialGasAndFees.GasProvided)
	if deltaGasProvided < 0 {
		log.Error("gasAndFeesDelta",
			"initial gasProvided", initialGasAndFees.GasProvided,
			"final gasProvided", finalGasAndFees.GasProvided,
			"error", process.ErrNegativeValue)
		return result
	}

	deltaGasPenalized := int64(finalGasAndFees.GasPenalized) - int64(initialGasAndFees.GasPenalized)
	if deltaGasPenalized < 0 {
		log.Error("gasAndFeesDelta",
			"initial gasPenalized", initialGasAndFees.GasPenalized,
			"final gasPenalized", finalGasAndFees.GasPenalized,
			"error", process.ErrNegativeValue)
		return result
	}

	deltaGasRefunded := int64(finalGasAndFees.GasRefunded) - int64(initialGasAndFees.GasRefunded)
	if deltaGasRefunded < 0 {
		log.Error("gasAndFeesDelta",
			"initial gasRefunded", initialGasAndFees.GasRefunded,
			"final gasRefunded", finalGasAndFees.GasRefunded,
			"error", process.ErrNegativeValue)
		return result
	}

	return scheduled.GasAndFees{
		AccumulatedFees: deltaAccumulatedFees,
		DeveloperFees:   deltaDevFees,
		GasProvided:     uint64(deltaGasProvided),
		GasPenalized:    uint64(deltaGasPenalized),
		GasRefunded:     uint64(deltaGasRefunded),
	}
}

func displayCleanupErrorMessage(message string, shardID uint32, noncesToPrevFinal uint64, err error) {
	// 2 blocks on shard + 2 blocks on meta + 1 block to previous final
	maxNoncesToPrevFinalWithoutWarn := uint64(process.BlockFinality+1)*2 + 1
	level := logger.LogWarning
	if noncesToPrevFinal <= maxNoncesToPrevFinalWithoutWarn {
		level = logger.LogDebug
	}

	log.Log(level, message,
		"shard", shardID,
		"nonces to previous final", noncesToPrevFinal,
		"error", err.Error())
}
