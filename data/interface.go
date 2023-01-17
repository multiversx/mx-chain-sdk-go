package data

import (
	"github.com/multiversx/mx-chain-core-go/data"
	"math/big"
)

// CommonHeaderHandler defines getters and setters for header data holder
type CommonHeaderHandler interface {
	GetNonce() uint64
	GetEpoch() uint32
	GetRound() uint64
	GetRootHash() []byte
	GetPrevHash() []byte
	GetPrevRandSeed() []byte
	GetRandSeed() []byte
	GetPubKeysBitmap() []byte
	GetSignature() []byte
	GetLeaderSignature() []byte
	GetChainID() []byte
	GetSoftwareVersion() []byte
	GetTimeStamp() uint64
	GetTxCount() uint32
	GetReceiptsHash() []byte
	GetReserved() []byte
	GetAccumulatedFees() *big.Int
	GetDeveloperFees() *big.Int
	GetMiniBlockHeadersHashes() [][]byte
	GetMiniBlockHeaderHandlers() []data.MiniBlockHeaderHandler

	SetNonce(n uint64) error
	SetEpoch(e uint32) error
	SetRound(r uint64) error
	SetTimeStamp(ts uint64) error
	SetRootHash(rHash []byte) error
	SetPrevHash(pvHash []byte) error
	SetPrevRandSeed(pvRandSeed []byte) error
	SetRandSeed(randSeed []byte) error
	SetPubKeysBitmap(pkbm []byte) error
	SetSignature(sg []byte) error
	SetLeaderSignature(sg []byte) error
	SetChainID(chainID []byte) error
	SetSoftwareVersion(version []byte) error
	SetTxCount(txCount uint32) error
	SetDeveloperFees(value *big.Int) error
	SetAccumulatedFees(value *big.Int) error
	SetMiniBlockHeaderHandlers(mbHeaderHandlers []data.MiniBlockHeaderHandler) error
	SetReceiptsHash(hash []byte) error
	ValidateHeaderVersion() error
	ShallowClone() CommonHeaderHandler
	IsInterfaceNil() bool
}

// SideChainHeaderHandler simple header handler with validator statistics
type SideChainHeaderHandler interface {
	CommonHeaderHandler
	SetValidatorStatsRootHash(rHash []byte) error
	GetValidatorStatsRootHash() []byte
}
