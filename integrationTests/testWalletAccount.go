package integrationTests

import (
	"encoding/hex"
	"fmt"
	"math/big"

	crypto "github.com/multiversx/mx-chain-crypto-go"
	ed25519SingleSig "github.com/multiversx/mx-chain-crypto-go/signing/ed25519/singlesig"
	"github.com/multiversx/mx-chain-go/factory/peerSignatureHandler"
	"github.com/multiversx/mx-chain-go/integrationTests/mock"
	"github.com/multiversx/mx-chain-storage-go/storageUnit"
)

// TestWalletAccount creates and account with balance and crypto necessary to sign transactions
type TestWalletAccount struct {
	SingleSigner      crypto.SingleSigner
	BlockSingleSigner crypto.SingleSigner
	SkTxSign          crypto.PrivateKey
	PkTxSign          crypto.PublicKey
	PkTxSignBytes     []byte
	KeygenTxSign      crypto.KeyGenerator
	KeygenBlockSign   crypto.KeyGenerator
	PeerSigHandler    crypto.PeerSignatureHandler

	Address []byte
	Nonce   uint64
	Balance *big.Int
}

// CreateTestWalletAccount creates an wallet account in a selected shard
func CreateTestWalletAccount() *TestWalletAccount {
	testWalletAccount := &TestWalletAccount{}
	testWalletAccount.initCrypto()
	testWalletAccount.Balance = big.NewInt(0)
	return testWalletAccount
}

// CreateTestWalletAccountWithKeygenAndSingleSigner creates a wallet account in a selected shard
func CreateTestWalletAccountWithKeygenAndSingleSigner(
	blockSingleSigner crypto.SingleSigner,
	keyGenBlockSign crypto.KeyGenerator,
) *TestWalletAccount {
	twa := CreateTestWalletAccount()
	twa.KeygenBlockSign = keyGenBlockSign
	twa.BlockSingleSigner = blockSingleSigner

	return twa
}

// initCrypto initializes the crypto for the account
func (twa *TestWalletAccount) initCrypto() {
	twa.SingleSigner = &ed25519SingleSig.Ed25519Signer{}
	twa.BlockSingleSigner = &mock.SignerMock{
		VerifyStub: func(public crypto.PublicKey, msg []byte, sig []byte) error {
			return nil
		},
	}
	sk, pk, keyGen := GenerateSkAndPkInShard()

	pkBuff, _ := pk.ToByteArray()
	fmt.Printf("Found pk: %s \n", hex.EncodeToString(pkBuff))

	twa.SkTxSign = sk
	twa.PkTxSign = pk
	twa.PkTxSignBytes, _ = pk.ToByteArray()
	twa.KeygenTxSign = keyGen
	twa.KeygenBlockSign = &mock.KeyGenMock{}
	twa.Address = twa.PkTxSignBytes

	peerSigCache, _ := storageUnit.NewCache(storageUnit.CacheConfig{Type: storageUnit.LRUCache, Capacity: 1000})
	twa.PeerSigHandler, _ = peerSignatureHandler.NewPeerSignatureHandler(peerSigCache, twa.SingleSigner, keyGen)
}

// LoadTxSignSkBytes alters the already generated sk/pk pair
func (twa *TestWalletAccount) LoadTxSignSkBytes(skBytes []byte) {
	newSk, _ := twa.KeygenTxSign.PrivateKeyFromByteArray(skBytes)
	newPk := newSk.GeneratePublic()

	twa.SkTxSign = newSk
	twa.PkTxSign = newPk
	twa.PkTxSignBytes, _ = newPk.ToByteArray()
	twa.Address = twa.PkTxSignBytes
}
