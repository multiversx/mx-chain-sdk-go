package validator

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"sync"

	sidechaindata "github.com/ElrondNetwork/chain-go-sdk/data"
	"github.com/ElrondNetwork/elrond-go-core/core"
	"github.com/ElrondNetwork/elrond-go-core/core/check"
	"github.com/ElrondNetwork/elrond-go-core/data"
	"github.com/ElrondNetwork/elrond-go-core/marshal"
	logger "github.com/ElrondNetwork/elrond-go-logger"
	"github.com/ElrondNetwork/elrond-go/common"
	"github.com/ElrondNetwork/elrond-go/dataRetriever"
	"github.com/ElrondNetwork/elrond-go/process"
	"github.com/ElrondNetwork/elrond-go/sharding"
	"github.com/ElrondNetwork/elrond-go/sharding/nodesCoordinator"
	"github.com/ElrondNetwork/elrond-go/state"
)

var log = logger.GetOrCreate("process/validator")

type validatorActionType uint8

const (
	unknownAction             validatorActionType = 0
	leaderSuccess             validatorActionType = 1
	leaderFail                validatorActionType = 2
	validatorSuccess          validatorActionType = 3
	validatorIgnoredSignature validatorActionType = 4
)

// ArgValidatorStatisticsProcessor holds all dependencies for the validatorStatistics
type ArgValidatorStatisticsProcessor struct {
	Marshalizer                          marshal.Marshalizer
	NodesCoordinator                     nodesCoordinator.NodesCoordinator
	ShardCoordinator                     sharding.Coordinator
	DataPool                             DataPool
	StorageService                       dataRetriever.StorageService
	PubkeyConv                           core.PubkeyConverter
	PeerAdapter                          state.AccountsAdapter
	Rater                                sharding.PeerAccountListAndRatingHandler
	RewardsHandler                       process.RewardsHandler
	MaxComputableRounds                  uint64
	MaxConsecutiveRoundsOfRatingDecrease uint64
	NodesSetup                           sharding.GenesisNodesSetupHandler
	GenesisNonce                         uint64
}

type validatorStatistics struct {
	marshalizer                          marshal.Marshalizer
	dataPool                             DataPool
	storageService                       dataRetriever.StorageService
	nodesCoordinator                     nodesCoordinator.NodesCoordinator
	shardCoordinator                     sharding.Coordinator
	pubkeyConv                           core.PubkeyConverter
	peerAdapter                          state.AccountsAdapter
	rater                                sharding.PeerAccountListAndRatingHandler
	rewardsHandler                       process.RewardsHandler
	maxComputableRounds                  uint64
	maxConsecutiveRoundsOfRatingDecrease uint64
	missedBlocksCounters                 validatorRoundCounters
	mutValidatorStatistics               sync.RWMutex
	lastFinalizedRootHash                []byte
	genesisNonce                         uint64
}

// NewValidatorStatisticsProcessor instantiates a new validatorStatistics structure responsible of keeping account of
//  each validator actions in the consensus process
func NewValidatorStatisticsProcessor(arguments ArgValidatorStatisticsProcessor) (*validatorStatistics, error) {
	if check.IfNil(arguments.PeerAdapter) {
		return nil, process.ErrNilPeerAccountsAdapter
	}
	if check.IfNil(arguments.PubkeyConv) {
		return nil, process.ErrNilPubkeyConverter
	}
	if check.IfNil(arguments.DataPool) {
		return nil, process.ErrNilDataPoolHolder
	}
	if check.IfNil(arguments.StorageService) {
		return nil, process.ErrNilStorage
	}
	if check.IfNil(arguments.NodesCoordinator) {
		return nil, process.ErrNilNodesCoordinator
	}
	if check.IfNil(arguments.ShardCoordinator) {
		return nil, process.ErrNilShardCoordinator
	}
	if check.IfNil(arguments.Marshalizer) {
		return nil, process.ErrNilMarshalizer
	}
	if arguments.MaxComputableRounds == 0 {
		return nil, process.ErrZeroMaxComputableRounds
	}
	if arguments.MaxConsecutiveRoundsOfRatingDecrease == 0 {
		return nil, process.ErrZeroMaxConsecutiveRoundsOfRatingDecrease
	}
	if check.IfNil(arguments.Rater) {
		return nil, process.ErrNilRater
	}
	if check.IfNil(arguments.RewardsHandler) {
		return nil, process.ErrNilRewardsHandler
	}
	if check.IfNil(arguments.NodesSetup) {
		return nil, process.ErrNilNodesSetup
	}

	vs := &validatorStatistics{
		peerAdapter:                          arguments.PeerAdapter,
		pubkeyConv:                           arguments.PubkeyConv,
		nodesCoordinator:                     arguments.NodesCoordinator,
		shardCoordinator:                     arguments.ShardCoordinator,
		dataPool:                             arguments.DataPool,
		storageService:                       arguments.StorageService,
		marshalizer:                          arguments.Marshalizer,
		missedBlocksCounters:                 make(validatorRoundCounters),
		rater:                                arguments.Rater,
		rewardsHandler:                       arguments.RewardsHandler,
		maxComputableRounds:                  arguments.MaxComputableRounds,
		maxConsecutiveRoundsOfRatingDecrease: arguments.MaxConsecutiveRoundsOfRatingDecrease,
		genesisNonce:                         arguments.GenesisNonce,
	}

	err := vs.saveInitialState(arguments.NodesSetup)
	if err != nil {
		return nil, err
	}

	return vs, nil
}

// saveInitialState takes an initial validator list, validates it and sets up the initial state for each of the peers
func (vs *validatorStatistics) saveInitialState(nodesConfig sharding.GenesisNodesSetupHandler) error {
	eligibleNodesInfo, _ := nodesConfig.InitialNodesInfo()
	err := vs.saveInitialValueForMap(eligibleNodesInfo[0], common.EligibleList)
	if err != nil {
		return err
	}

	hash, err := vs.peerAdapter.Commit()
	if err != nil {
		return err
	}

	log.Trace("committed validator adapter", "root hash", hex.EncodeToString(hash))

	return nil
}

func (vs *validatorStatistics) saveInitialValueForMap(
	nodesInfo []nodesCoordinator.GenesisNodeInfoHandler,
	peerType common.PeerType,
) error {
	if len(nodesInfo) == 0 {
		return nil
	}

	for index, nodeInfo := range nodesInfo {
		err := vs.initializeNode(nodeInfo, 0, peerType, uint32(index))
		if err != nil {
			return err
		}
	}

	return nil
}

// SaveNodesCoordinatorUpdates -
func (vs *validatorStatistics) SaveNodesCoordinatorUpdates(_ uint32) (bool, error) {
	return false, nil
}

// UpdatePeerState takes a header, updates the validator state for all of the
// consensus members and returns the new root hash
func (vs *validatorStatistics) UpdatePeerState(header sidechaindata.SideChainHeaderHandler, previousHeader sidechaindata.SideChainHeaderHandler) ([]byte, error) {
	if check.IfNil(header) {
		return nil, process.ErrNilHeaderHandler
	}
	if header.GetNonce() == vs.genesisNonce {
		return vs.peerAdapter.RootHash()
	}

	if check.IfNil(previousHeader) {
		return nil, process.ErrNilHeaderHandler
	}

	vs.mutValidatorStatistics.Lock()
	vs.missedBlocksCounters.reset()
	vs.mutValidatorStatistics.Unlock()

	err := vs.checkForMissedBlocks(
		header.GetRound(),
		previousHeader.GetRound(),
		previousHeader.GetRandSeed(),
	)
	if err != nil {
		return nil, err
	}

	err = vs.updateMissedBlocksCounters()
	if err != nil {
		return nil, err
	}

	if header.GetNonce() == vs.genesisNonce+1 {
		return vs.peerAdapter.RootHash()
	}
	log.Trace("Increasing", "round", previousHeader.GetRound(), "prevRandSeed", previousHeader.GetPrevRandSeed())

	consensusGroup, err := vs.nodesCoordinator.ComputeConsensusGroup(
		previousHeader.GetPrevRandSeed(),
		previousHeader.GetRound(),
		0, 0)
	if err != nil {
		return nil, err
	}
	leaderPK := core.GetTrimmedPk(vs.pubkeyConv.Encode(consensusGroup[0].PubKey()))
	log.Trace("Increasing for leader", "leader", leaderPK, "round", previousHeader.GetRound())
	err = vs.updateValidatorInfoOnSuccessfulBlock(
		consensusGroup,
		previousHeader.GetPubKeysBitmap(),
		big.NewInt(0).Sub(previousHeader.GetAccumulatedFees(), previousHeader.GetDeveloperFees()),
	)
	if err != nil {
		return nil, err
	}

	rootHash, err := vs.peerAdapter.RootHash()
	if err != nil {
		return nil, err
	}

	log.Trace("after updating validator stats", "rootHash", rootHash, "round", header.GetRound(), "selfId", vs.shardCoordinator.SelfId())

	return rootHash, nil
}

// DisplayRatings will print the ratings
func (vs *validatorStatistics) DisplayRatings(epoch uint32) {
	validatorPKs, err := vs.nodesCoordinator.GetAllEligibleValidatorsPublicKeys(epoch)
	if err != nil {
		log.Warn("could not get ValidatorPublicKeys", "epoch", epoch)
		return
	}
	log.Trace("started printing tempRatings")
	for shardID, list := range validatorPKs {
		for _, pk := range list {
			log.Trace("tempRating", "PK", pk, "tempRating", vs.getTempRating(string(pk)), "ShardID", shardID)
		}
	}
	log.Trace("finished printing tempRatings")
}

// Commit commits the validator statistics trie and returns the root hash
func (vs *validatorStatistics) Commit() ([]byte, error) {
	return vs.peerAdapter.Commit()
}

// RootHash returns the root hash of the validator statistics trie
func (vs *validatorStatistics) RootHash() ([]byte, error) {
	return vs.peerAdapter.RootHash()
}

func (vs *validatorStatistics) getValidatorDataFromLeaves(
	leavesChannel chan core.KeyValueHolder,
) (map[uint32][]*state.ValidatorInfo, error) {

	validators := make(map[uint32][]*state.ValidatorInfo, vs.shardCoordinator.NumberOfShards()+1)
	for i := uint32(0); i < vs.shardCoordinator.NumberOfShards(); i++ {
		validators[i] = make([]*state.ValidatorInfo, 0)
	}

	for pa := range leavesChannel {
		peerAccount, err := vs.unmarshalPeer(pa.Value())
		if err != nil {
			return nil, err
		}

		currentShardId := peerAccount.GetShardId()
		validatorInfoData := vs.PeerAccountToValidatorInfo(peerAccount)
		validators[currentShardId] = append(validators[currentShardId], validatorInfoData)
	}

	return validators, nil
}

// PeerAccountToValidatorInfo creates a validator info from the given validator account
func (vs *validatorStatistics) PeerAccountToValidatorInfo(peerAccount state.PeerAccountHandler) *state.ValidatorInfo {
	chance := vs.rater.GetChance(peerAccount.GetRating())
	startRatingChance := vs.rater.GetChance(vs.rater.GetStartRating())
	ratingModifier := float32(chance) / float32(startRatingChance)

	return &state.ValidatorInfo{
		PublicKey:                       peerAccount.GetBLSPublicKey(),
		ShardId:                         peerAccount.GetShardId(),
		List:                            peerAccount.GetList(),
		Index:                           peerAccount.GetIndexInList(),
		TempRating:                      peerAccount.GetTempRating(),
		Rating:                          peerAccount.GetRating(),
		RatingModifier:                  ratingModifier,
		RewardAddress:                   peerAccount.GetRewardAddress(),
		LeaderSuccess:                   peerAccount.GetLeaderSuccessRate().NumSuccess,
		LeaderFailure:                   peerAccount.GetLeaderSuccessRate().NumFailure,
		ValidatorSuccess:                peerAccount.GetValidatorSuccessRate().NumSuccess,
		ValidatorFailure:                peerAccount.GetValidatorSuccessRate().NumFailure,
		ValidatorIgnoredSignatures:      peerAccount.GetValidatorIgnoredSignaturesRate(),
		TotalLeaderSuccess:              peerAccount.GetTotalLeaderSuccessRate().NumSuccess,
		TotalLeaderFailure:              peerAccount.GetTotalLeaderSuccessRate().NumFailure,
		TotalValidatorSuccess:           peerAccount.GetTotalValidatorSuccessRate().NumSuccess,
		TotalValidatorFailure:           peerAccount.GetTotalValidatorSuccessRate().NumFailure,
		TotalValidatorIgnoredSignatures: peerAccount.GetTotalValidatorIgnoredSignaturesRate(),
		NumSelectedInSuccessBlocks:      peerAccount.GetNumSelectedInSuccessBlocks(),
		AccumulatedFees:                 big.NewInt(0).Set(peerAccount.GetAccumulatedFees()),
	}
}

// IsLowRating returns true if temp rating is under 0 chance value
func (vs *validatorStatistics) IsLowRating(blsKey []byte) bool {
	acc, err := vs.peerAdapter.GetExistingAccount(blsKey)
	if err != nil {
		return false
	}

	validatorAccount, ok := acc.(state.PeerAccountHandler)
	if !ok {
		return false
	}

	return vs.isValidatorWithLowRating(validatorAccount)
}

func (vs *validatorStatistics) isValidatorWithLowRating(validatorAccount state.PeerAccountHandler) bool {
	minChance := vs.rater.GetChance(0)
	return vs.rater.GetChance(validatorAccount.GetTempRating()) < minChance
}

func (vs *validatorStatistics) jailValidatorIfBadRatingAndInactive(validatorAccount state.PeerAccountHandler) {
	if !vs.isValidatorWithLowRating(validatorAccount) {
		return
	}

	validatorAccount.SetListAndIndex(validatorAccount.GetShardId(), string(common.JailedList), validatorAccount.GetIndexInList())
}

func (vs *validatorStatistics) unmarshalPeer(pa []byte) (state.PeerAccountHandler, error) {
	peerAccount := state.NewEmptyPeerAccount()
	err := vs.marshalizer.Unmarshal(peerAccount, pa)
	if err != nil {
		return nil, err
	}
	return peerAccount, nil
}

// GetValidatorInfoForRootHash returns all the validator accounts from the trie with the given rootHash
func (vs *validatorStatistics) GetValidatorInfoForRootHash(rootHash []byte) (map[uint32][]*state.ValidatorInfo, error) {
	sw := core.NewStopWatch()
	sw.Start("GetValidatorInfoForRootHash")
	defer func() {
		sw.Stop("GetValidatorInfoForRootHash")
		log.Debug("GetValidatorInfoForRootHash", sw.GetMeasurements()...)
	}()

	leavesChannel := make(chan core.KeyValueHolder, common.TrieLeavesChannelDefaultCapacity)
	err := vs.peerAdapter.GetAllLeaves(leavesChannel, context.Background(), rootHash)
	if err != nil {
		return nil, err
	}

	vInfos, err := vs.getValidatorDataFromLeaves(leavesChannel)
	if err != nil {
		return nil, err
	}

	return vInfos, err
}

func (vs *validatorStatistics) checkForMissedBlocks(
	currentHeaderRound,
	previousHeaderRound uint64,
	prevRandSeed []byte,
) error {
	missedRounds := currentHeaderRound - previousHeaderRound
	if missedRounds <= 1 {
		return nil
	}
	if missedRounds > vs.maxConsecutiveRoundsOfRatingDecrease {
		return nil
	}

	tooManyComputations := missedRounds > vs.maxComputableRounds
	if !tooManyComputations {
		return vs.computeDecrease(previousHeaderRound, currentHeaderRound, prevRandSeed)
	}

	return vs.decreaseAll(missedRounds - 1)
}

func (vs *validatorStatistics) computeDecrease(
	previousHeaderRound uint64,
	currentHeaderRound uint64,
	prevRandSeed []byte,
) error {
	sw := core.NewStopWatch()
	sw.Start("checkForMissedBlocks")
	defer func() {
		sw.Stop("checkForMissedBlocks")
		log.Trace("measurements checkForMissedBlocks", sw.GetMeasurements()...)
	}()

	for i := previousHeaderRound + 1; i < currentHeaderRound; i++ {
		swInner := core.NewStopWatch()

		swInner.Start("ComputeValidatorsGroup")
		log.Debug("decreasing", "round", i, "prevRandSeed", prevRandSeed)
		consensusGroup, err := vs.nodesCoordinator.ComputeConsensusGroup(prevRandSeed, i, 0, 0)
		swInner.Stop("ComputeValidatorsGroup")
		if err != nil {
			return err
		}

		swInner.Start("loadPeerAccount")
		leaderPeerAcc, err := vs.loadPeerAccount(consensusGroup[0].PubKey())
		leaderPK := core.GetTrimmedPk(vs.pubkeyConv.Encode(consensusGroup[0].PubKey()))
		swInner.Stop("loadPeerAccount")
		if err != nil {
			return err
		}

		vs.mutValidatorStatistics.Lock()
		vs.missedBlocksCounters.decreaseLeader(consensusGroup[0].PubKey())
		vs.mutValidatorStatistics.Unlock()

		swInner.Start("ComputeDecreaseProposer")
		newRating := vs.rater.ComputeDecreaseProposer(
			0,
			leaderPeerAcc.GetTempRating(),
			leaderPeerAcc.GetConsecutiveProposerMisses())
		swInner.Stop("ComputeDecreaseProposer")

		swInner.Start("SetConsecutiveProposerMisses")
		leaderPeerAcc.SetConsecutiveProposerMisses(leaderPeerAcc.GetConsecutiveProposerMisses() + 1)
		swInner.Stop("SetConsecutiveProposerMisses")

		swInner.Start("SetTempRating")
		leaderPeerAcc.SetTempRating(newRating)
		log.Debug("decreasing for leader",
			"leader", leaderPK,
			"round", i,
			"temp rating", newRating,
			"consecutive misses", leaderPeerAcc.GetConsecutiveProposerMisses())
		vs.jailValidatorIfBadRatingAndInactive(leaderPeerAcc)

		err = vs.peerAdapter.SaveAccount(leaderPeerAcc)
		swInner.Stop("SetTempRating")
		if err != nil {
			return err
		}

		swInner.Start("ComputeDecreaseAllValidators")
		err = vs.decreaseForConsensusValidators(consensusGroup)
		swInner.Stop("ComputeDecreaseAllValidators")
		if err != nil {
			return err
		}
		sw.Add(swInner)
	}
	return nil
}

func (vs *validatorStatistics) decreaseForConsensusValidators(
	consensusGroup []nodesCoordinator.Validator,
) error {
	vs.mutValidatorStatistics.Lock()
	defer vs.mutValidatorStatistics.Unlock()

	for j := 1; j < len(consensusGroup); j++ {
		validatorPeerAccount, verr := vs.loadPeerAccount(consensusGroup[j].PubKey())
		if verr != nil {
			return verr
		}
		vs.missedBlocksCounters.decreaseValidator(consensusGroup[j].PubKey())

		newRating := vs.rater.ComputeDecreaseValidator(0, validatorPeerAccount.GetTempRating())
		validatorPeerAccount.SetTempRating(newRating)
		vs.jailValidatorIfBadRatingAndInactive(validatorPeerAccount)
		err := vs.peerAdapter.SaveAccount(validatorPeerAccount)
		if err != nil {
			return err
		}
	}

	return nil
}

// RevertPeerState takes the current and previous headers and undos the validator state
//  for all of the consensus members
func (vs *validatorStatistics) RevertPeerState(header data.MetaHeaderHandler) error {
	return vs.peerAdapter.RecreateTrie(header.GetValidatorStatsRootHash())
}

func (vs *validatorStatistics) initializeNode(
	node nodesCoordinator.GenesisNodeInfoHandler,
	shardID uint32,
	peerType common.PeerType,
	index uint32,
) error {
	peerAccount, err := vs.loadPeerAccount(node.PubKeyBytes())
	if err != nil {
		return err
	}

	return vs.savePeerAccountData(peerAccount, node, node.GetInitialRating(), shardID, peerType, index)
}

func (vs *validatorStatistics) savePeerAccountData(
	peerAccount state.PeerAccountHandler,
	node nodesCoordinator.GenesisNodeInfoHandler,
	startRating uint32,
	shardID uint32,
	peerType common.PeerType,
	index uint32,
) error {
	log.Trace("validatorStatistics - savePeerAccountData",
		"pubkey", node.PubKeyBytes(),
		"reward address", node.AddressBytes(),
		"initial rating", node.GetInitialRating())
	err := peerAccount.SetRewardAddress(node.AddressBytes())
	if err != nil {
		return err
	}

	err = peerAccount.SetBLSPublicKey(node.PubKeyBytes())
	if err != nil {
		return err
	}

	peerAccount.SetRating(startRating)
	peerAccount.SetTempRating(startRating)
	peerAccount.SetListAndIndex(shardID, string(peerType), index)

	return vs.peerAdapter.SaveAccount(peerAccount)
}

func (vs *validatorStatistics) updateValidatorInfoOnSuccessfulBlock(
	validatorList []nodesCoordinator.Validator,
	signingBitmap []byte,
	accumulatedFees *big.Int,
) error {

	if len(signingBitmap) == 0 {
		return process.ErrNilPubKeysBitmap
	}
	lenValidators := len(validatorList)
	leaderAccumulatedFees := core.GetIntTrimmedPercentageOfValue(accumulatedFees, vs.rewardsHandler.LeaderPercentage())
	// TODO: add accumulated fees percentage to each validator
	for i := 0; i < lenValidators; i++ {
		peerAcc, err := vs.loadPeerAccount(validatorList[i].PubKey())
		if err != nil {
			return err
		}

		peerAcc.IncreaseNumSelectedInSuccessBlocks()

		newRating := peerAcc.GetRating()
		isLeader := i == 0
		validatorSigned := (signingBitmap[i/8] & (1 << (uint16(i) % 8))) != 0
		actionType := vs.computeValidatorActionType(isLeader, validatorSigned)

		switch actionType {
		case leaderSuccess:
			peerAcc.IncreaseLeaderSuccessRate(1)
			peerAcc.SetConsecutiveProposerMisses(0)
			newRating = vs.rater.ComputeIncreaseProposer(0, peerAcc.GetTempRating())
			peerAcc.AddToAccumulatedFees(leaderAccumulatedFees)
		case validatorSuccess:
			peerAcc.IncreaseValidatorSuccessRate(1)
			newRating = vs.rater.ComputeIncreaseValidator(0, peerAcc.GetTempRating())
		case validatorIgnoredSignature:
			peerAcc.IncreaseValidatorIgnoredSignaturesRate(1)
			newRating = vs.rater.ComputeIncreaseValidator(0, peerAcc.GetTempRating())
		}

		peerAcc.SetTempRating(newRating)

		err = vs.peerAdapter.SaveAccount(peerAcc)
		if err != nil {
			return err
		}
	}

	return nil
}

func (vs *validatorStatistics) loadPeerAccount(address []byte) (state.PeerAccountHandler, error) {
	account, err := vs.peerAdapter.LoadAccount(address)
	if err != nil {
		return nil, err
	}

	peerAccount, ok := account.(state.PeerAccountHandler)
	if !ok {
		return nil, process.ErrInvalidPeerAccount
	}

	return peerAccount, nil
}

func (vs *validatorStatistics) updateMissedBlocksCounters() error {
	vs.mutValidatorStatistics.Lock()
	defer func() {
		vs.missedBlocksCounters.reset()
		vs.mutValidatorStatistics.Unlock()
	}()

	for pubKey, roundCounters := range vs.missedBlocksCounters {
		peerAccount, err := vs.loadPeerAccount([]byte(pubKey))
		if err != nil {
			return err
		}

		if roundCounters.leaderDecreaseCount > 0 {
			peerAccount.DecreaseLeaderSuccessRate(roundCounters.leaderDecreaseCount)
		}

		if roundCounters.validatorDecreaseCount > 0 {
			peerAccount.DecreaseValidatorSuccessRate(roundCounters.validatorDecreaseCount)
		}

		err = vs.peerAdapter.SaveAccount(peerAccount)
		if err != nil {
			return err
		}
	}

	return nil
}

func (vs *validatorStatistics) computeValidatorActionType(isLeader, validatorSigned bool) validatorActionType {
	if isLeader && validatorSigned {
		return leaderSuccess
	}
	if isLeader && !validatorSigned {
		return leaderFail
	}
	if !isLeader && validatorSigned {
		return validatorSuccess
	}
	if !isLeader && !validatorSigned {
		return validatorIgnoredSignature
	}

	return unknownAction
}

// IsInterfaceNil returns true if there is no value under the interface
func (vs *validatorStatistics) IsInterfaceNil() bool {
	return vs == nil
}

func (vs *validatorStatistics) getTempRating(s string) uint32 {
	peer, err := vs.loadPeerAccount([]byte(s))

	if err != nil {
		log.Debug("Error getting validator account", "error", err)
		return vs.rater.GetStartRating()
	}

	return peer.GetTempRating()
}

func (vs *validatorStatistics) display(validatorKey string) {
	peerAcc, err := vs.loadPeerAccount([]byte(validatorKey))
	if err != nil {
		log.Trace("display validator acc", "error", err)
		return
	}

	log.Trace("validator statistics",
		"pk", core.GetTrimmedPk(hex.EncodeToString(peerAcc.GetBLSPublicKey())),
		"leader fail", peerAcc.GetLeaderSuccessRate().NumFailure,
		"leader success", peerAcc.GetLeaderSuccessRate().NumSuccess,
		"val success", peerAcc.GetValidatorSuccessRate().NumSuccess,
		"val ignored sigs", peerAcc.GetValidatorIgnoredSignaturesRate(),
		"val fail", peerAcc.GetValidatorSuccessRate().NumFailure,
		"temp rating", peerAcc.GetTempRating(),
		"rating", peerAcc.GetRating(),
	)
}

func (vs *validatorStatistics) decreaseAll(
	missedRounds uint64,
) error {
	consensusGroupSize := vs.nodesCoordinator.ConsensusGroupSize(0)
	validators, err := vs.nodesCoordinator.GetAllEligibleValidatorsPublicKeys(0)
	if err != nil {
		return err
	}

	shardValidators := validators[0]
	validatorsCount := len(shardValidators)
	percentageRoundMissedFromTotalValidators := float64(missedRounds) / float64(validatorsCount)
	leaderAppearances := uint32(percentageRoundMissedFromTotalValidators + 1 - math.SmallestNonzeroFloat64)
	consensusGroupAppearances := uint32(float64(consensusGroupSize)*percentageRoundMissedFromTotalValidators +
		1 - math.SmallestNonzeroFloat64)
	ratingDifference := uint32(0)

	for i, validator := range shardValidators {
		validatorPeerAccount, errLoad := vs.loadPeerAccount(validator)
		if errLoad != nil {
			return errLoad
		}
		validatorPeerAccount.DecreaseLeaderSuccessRate(leaderAppearances)
		validatorPeerAccount.DecreaseValidatorSuccessRate(consensusGroupAppearances)

		currentTempRating := validatorPeerAccount.GetTempRating()
		for ct := uint32(0); ct < leaderAppearances; ct++ {
			currentTempRating = vs.rater.ComputeDecreaseProposer(0, currentTempRating, 0)
		}

		for ct := uint32(0); ct < consensusGroupAppearances; ct++ {
			currentTempRating = vs.rater.ComputeDecreaseValidator(0, currentTempRating)
		}

		if i == 0 {
			ratingDifference = validatorPeerAccount.GetTempRating() - currentTempRating
		}

		validatorPeerAccount.SetTempRating(currentTempRating)
		vs.jailValidatorIfBadRatingAndInactive(validatorPeerAccount)
		err = vs.peerAdapter.SaveAccount(validatorPeerAccount)
		if err != nil {
			return err
		}

		vs.display(string(validator))
	}

	log.Trace(fmt.Sprintf("Decrease leader: %v, decrease validator: %v, ratingDifference: %v", leaderAppearances, consensusGroupAppearances, ratingDifference))

	return nil
}

// SetLastFinalizedRootHash - sets the last finalized root hash needed for correct validatorStatistics computations
func (vs *validatorStatistics) SetLastFinalizedRootHash(lastFinalizedRootHash []byte) {
	if len(lastFinalizedRootHash) == 0 {
		return
	}

	vs.mutValidatorStatistics.Lock()
	vs.lastFinalizedRootHash = lastFinalizedRootHash
	vs.mutValidatorStatistics.Unlock()
}

// LastFinalizedRootHash returns the root hash of the validator statistics trie that was last finalized
func (vs *validatorStatistics) LastFinalizedRootHash() []byte {
	vs.mutValidatorStatistics.RLock()
	defer vs.mutValidatorStatistics.RUnlock()
	return vs.lastFinalizedRootHash
}
