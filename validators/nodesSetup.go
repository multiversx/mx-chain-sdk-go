package validators

import (
	"fmt"
	"github.com/ElrondNetwork/elrond-go/sharding"

	"github.com/ElrondNetwork/elrond-go-core/core"
	"github.com/ElrondNetwork/elrond-go-core/core/check"
	"github.com/ElrondNetwork/elrond-go/sharding/nodesCoordinator"
)

const defaultInitialRating = uint32(5000001)

// InitialNode holds data from json
type InitialNode struct {
	PubKey        string `json:"pubkey"`
	Address       string `json:"address"`
	InitialRating uint32 `json:"initialRating"`
	nodeInfo
}

// nodeInfo holds node info
type nodeInfo struct {
	pubKey        []byte
	address       []byte
	initialRating uint32
}

// AssignedShard gets the node assigned shard
func (ni *nodeInfo) AssignedShard() uint32 {
	return 0
}

// AddressBytes gets the node address as bytes
func (ni *nodeInfo) AddressBytes() []byte {
	return ni.address
}

// PubKeyBytes gets the node public key as bytes
func (ni *nodeInfo) PubKeyBytes() []byte {
	return ni.pubKey
}

// GetInitialRating gets the initial rating for a node
func (ni *nodeInfo) GetInitialRating() uint32 {
	return ni.initialRating
}

// IsInterfaceNil returns true if underlying object is nil
func (ni *nodeInfo) IsInterfaceNil() bool {
	return ni == nil
}

// NodesSetup hold data for decoded data from json file
type NodesSetup struct {
	StartTime          int64          `json:"startTime"`
	RoundDuration      uint64         `json:"roundDuration"`
	ConsensusGroupSize uint32         `json:"consensusGroupSize"`
	InitialNodes       []*InitialNode `json:"initialNodes"`

	eligible                 []nodesCoordinator.GenesisNodeInfoHandler
	validatorPubkeyConverter core.PubkeyConverter
	addressPubkeyConverter   core.PubkeyConverter
}

// NewNodesSetup creates a new decoded nodes structure from json config file
func NewNodesSetup(
	nodesFilePath string,
	addressPubkeyConverter core.PubkeyConverter,
	validatorPubkeyConverter core.PubkeyConverter,
) (*NodesSetup, error) {

	if check.IfNil(addressPubkeyConverter) {
		return nil, fmt.Errorf("%w for addressPubkeyConverter", sharding.ErrNilPubkeyConverter)
	}
	if check.IfNil(validatorPubkeyConverter) {
		return nil, fmt.Errorf("%w for validatorPubkeyConverter", sharding.ErrNilPubkeyConverter)
	}

	nodes := &NodesSetup{
		addressPubkeyConverter:   addressPubkeyConverter,
		validatorPubkeyConverter: validatorPubkeyConverter,
	}

	err := core.LoadJsonFile(nodes, nodesFilePath)
	if err != nil {
		return nil, err
	}

	err = nodes.processConfig()
	if err != nil {
		return nil, err
	}

	nodes.createInitialNodesInfo()

	return nodes, nil
}

func (ns *NodesSetup) processConfig() error {
	var err error

	for i := 0; i < len(ns.InitialNodes); i++ {
		pubKey := ns.InitialNodes[i].PubKey
		ns.InitialNodes[i].pubKey, err = ns.validatorPubkeyConverter.Decode(pubKey)
		if err != nil {
			return fmt.Errorf("%w, %s for string %s", sharding.ErrCouldNotParsePubKey, err.Error(), pubKey)
		}

		address := ns.InitialNodes[i].Address
		ns.InitialNodes[i].address, err = ns.addressPubkeyConverter.Decode(address)
		if err != nil {
			return fmt.Errorf("%w, %s for string %s", sharding.ErrCouldNotParseAddress, err.Error(), address)
		}

		// decoder treats empty string as correct, it is not allowed to have empty string as public key
		if ns.InitialNodes[i].PubKey == "" {
			ns.InitialNodes[i].pubKey = nil
			return sharding.ErrCouldNotParsePubKey
		}

		// decoder treats empty string as correct, it is not allowed to have empty string as address
		if ns.InitialNodes[i].Address == "" {
			ns.InitialNodes[i].address = nil
			return sharding.ErrCouldNotParseAddress
		}

		initialRating := ns.InitialNodes[i].InitialRating
		if initialRating == uint32(0) {
			initialRating = defaultInitialRating
		}
		ns.InitialNodes[i].initialRating = initialRating
	}

	if ns.ConsensusGroupSize < 1 {
		return sharding.ErrNegativeOrZeroConsensusGroupSize
	}

	return nil
}

func (ns *NodesSetup) createInitialNodesInfo() {
	ns.eligible = make([]nodesCoordinator.GenesisNodeInfoHandler, len(ns.InitialNodes))
	for i, in := range ns.InitialNodes {
		if in.pubKey != nil && in.address != nil {
			ni := &nodeInfo{
				pubKey:        in.pubKey,
				address:       in.address,
				initialRating: in.initialRating,
			}
			ns.eligible[i] = ni
		}
	}
}

// InitialNodesPubKeys - gets initial nodes public keys
func (ns *NodesSetup) InitialNodesPubKeys() []string {
	pubKeys := make([]string, len(ns.eligible))
	for i, nodesInfo := range ns.eligible {
		pubKeys[i] = string(nodesInfo.PubKeyBytes())
	}

	return pubKeys
}

// InitialNodesInfo - gets initial nodes info
func (ns *NodesSetup) InitialNodesInfo() []nodesCoordinator.GenesisNodeInfoHandler {
	return ns.eligible
}

// AllInitialNodes returns all initial nodes loaded
func (ns *NodesSetup) AllInitialNodes() []nodesCoordinator.GenesisNodeInfoHandler {
	list := make([]nodesCoordinator.GenesisNodeInfoHandler, len(ns.InitialNodes))
	for idx, initialNode := range ns.InitialNodes {
		list[idx] = initialNode
	}

	return list
}

// MinNumberOfNodes returns the minimum number of nodes
func (ns *NodesSetup) MinNumberOfNodes() uint32 {
	return ns.ConsensusGroupSize
}

// GetStartTime returns the start time
func (ns *NodesSetup) GetStartTime() int64 {
	return ns.StartTime
}

// GetRoundDuration returns the round duration
func (ns *NodesSetup) GetRoundDuration() uint64 {
	return ns.RoundDuration
}

// GetConsensusGroupSize returns the shard consensus group size
func (ns *NodesSetup) GetConsensusGroupSize() uint32 {
	return ns.ConsensusGroupSize
}

// IsInterfaceNil returns true if underlying object is nil
func (ns *NodesSetup) IsInterfaceNil() bool {
	return ns == nil
}
