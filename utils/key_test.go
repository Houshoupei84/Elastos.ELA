package utils

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"testing"

	"github.com/elastos/Elastos.ELA/common"
	"github.com/elastos/Elastos.ELA/core/contract/program"
)

func TestIDChainStore_CreateDIDTx(t *testing.T) {

	pkHexStr := "03667d8514e94a1f93a5592ae783050dac4efa82ecf9711de2dc28da80c6e5d5e5"
	privateKeyHexStr := "d28a4420de297db6102f969e89e1e56a9951ca775b346cc3375f1741e963f588"
	privateKeyByte, _ := common.HexStringToBytes(privateKeyHexStr)

	//pkbyte <-> pkHex
	pkByte, error := PkByteFromPkHex(pkHexStr)
	if error != nil {
		return
	}
	pkHexStr2 := PkHexFromPkByte(pkByte)
	assert.Equal(t, pkHexStr, pkHexStr2)
	fmt.Printf("pkByte %v \n", pkByte)

	//pointPubKey <-> pkByte
	pkPt, _ := PtPubKeyFromPkByte(pkByte)
	fmt.Println("pkPt", pkPt)
	pkByteNew, _ := PkByteFromPubKeyPt(pkPt)
	assert.Equal(t, pkByteNew, pkByte)

	//pkByte <-> code
	code := GetCode(pkByte)
	pkByteNew = GetPublickKey(code)
	assert.Equal(t, pkByte, pkByteNew)

	//pkbyte	 03667d8514e94a1f93a5592ae783050dac4efa82ecf9711de2dc28da80c6e5d5e5didPkHash
	//didpkhash  67447c9305066460e4d32d23c15a410c1269855979
	//didaddress iZ4FboZVW9jaZxWQNCyLBGNarAwTyY3Ydr
	//pkbyte---->didpkhash-------------------------------->didaddress注意是i开头地址(
	didPkHash, _ := GetDidPkHash(pkByte)
	didAddress, _ := GetDIDAddress(didPkHash)
	fmt.Printf("pkbyte %s \n", common.BytesToHexString(pkByte))
	fmt.Println("didPkHash", didPkHash)
	fmt.Println("didAddress", didAddress)

	//signature ---->parameter
	var prog program.Program
	prog.Code = code
	data := []byte{1, 2, 3}
	signature, err := Sign(privateKeyByte, data)
	prog.Parameter = GetParameter(signature)
	err = CheckStandardSignature(prog, data)
	assert.NoError(t, err)
}
