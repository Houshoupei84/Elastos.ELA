package utils

import (
	"bytes"
	"errors"

	"github.com/elastos/Elastos.ELA/common"
	"github.com/elastos/Elastos.ELA/core/contract"
	"github.com/elastos/Elastos.ELA/core/contract/program"
	"github.com/elastos/Elastos.ELA/crypto"
)

func PkByteFromPkHex(pkHexStr string) ([]byte, error) {
	return common.HexStringToBytes(pkHexStr)
}
func PkHexFromPkByte(pkByte []byte) string {
	return common.BytesToHexString(pkByte)
}

func PtPubKeyFromPkByte(publicKey []byte) (*crypto.PublicKey, error) {
	return crypto.DecodePoint(publicKey)
}

func PkByteFromPubKeyPt(pubKeyPt *crypto.PublicKey) ([]byte, error) {
	return pubKeyPt.EncodePoint(true)
}

// code = len(data) + PkByte + vm.CHECKSIG
func GetCode(pkBytes []byte) []byte {
	pk, _ := crypto.DecodePoint(pkBytes)
	redeemScript, _ := contract.CreateStandardRedeemScript(pk)
	return redeemScript
}

// code = len(data) + PkByte + vm.CHECKSIG
func GetPublickKey(code []byte) []byte {
	publicKey := code[1 : len(code)-1]
	return publicKey
}

func GetDidPkHash(publicKey []byte) (*common.Uint168, error) {
	pk, _ := crypto.DecodePoint(publicKey)
	redeemScript, err := contract.CreateStandardRedeemScript(pk)
	if err != nil {
		return nil, err
	}
	return GetDidPKHash(redeemScript)
}

func GetDidPKHash(code []byte) (*common.Uint168, error) {
	ct1, error := contract.CreateCRDIDContractByCode(code)
	if error != nil {
		return nil, error
	}
	return ct1.ToProgramHash(), error
}

//返回根据hash 返回地址
func GetDIDAddress(didHash *common.Uint168) (string, error) {
	return didHash.ToAddress()
}

//parameter = len(signature) + signature
func GetParameter(signature []byte) []byte {
	buf := new(bytes.Buffer)
	buf.WriteByte(byte(len(signature)))
	buf.Write(signature)
	return buf.Bytes()
}

func CheckStandardSignature(program program.Program, data []byte) error {
	if len(program.Parameter) != crypto.SignatureScriptLength {
		return errors.New("invalid signature length")
	}

	publicKey, err := crypto.DecodePoint(program.Code[1 : len(program.Code)-1])
	if err != nil {
		return err
	}

	return crypto.Verify(*publicKey, data, program.Parameter[1:])
}

func Sign(private []byte, data []byte) (signature []byte, err error) {
	signature, err = crypto.Sign(private, data)
	if err != nil {
		return signature, err
	}

	buf := new(bytes.Buffer)
	//buf.WriteByte(byte(len(signature)))
	buf.Write(signature)
	return buf.Bytes(), err
}
