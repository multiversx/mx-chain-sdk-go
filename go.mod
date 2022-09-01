module github.com/ElrondNetwork/chain-go-sdk

go 1.15

require (
	github.com/ElrondNetwork/arwen-wasm-vm/v1_4 v1.4.58
	github.com/ElrondNetwork/elrond-go v1.3.37-0.20220824134635-ec5c6e2e0c5c
	github.com/ElrondNetwork/elrond-go-core v1.1.19
	github.com/ElrondNetwork/elrond-go-crypto v1.0.1
	github.com/ElrondNetwork/elrond-go-logger v1.0.7
	github.com/ElrondNetwork/elrond-vm-common v1.3.14
	github.com/pkg/errors v0.9.1
	github.com/stretchr/testify v1.7.1
)

replace github.com/gogo/protobuf => github.com/ElrondNetwork/protobuf v1.3.2

replace github.com/ElrondNetwork/arwen-wasm-vm/v1_2 v1.2.41 => github.com/ElrondNetwork/arwen-wasm-vm v1.2.41

replace github.com/ElrondNetwork/arwen-wasm-vm/v1_3 v1.3.41 => github.com/ElrondNetwork/arwen-wasm-vm v1.3.41

replace github.com/ElrondNetwork/arwen-wasm-vm/v1_4 v1.4.58 => github.com/ElrondNetwork/arwen-wasm-vm v1.4.58
