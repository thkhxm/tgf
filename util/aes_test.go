package util_test

import (
	"encoding/hex"
	"github.com/thkhxm/tgf/util"
	"testing"
)

var strKey = "hduiorlkjuysbxgthduiorlkjuysbxgt"

func TestEncrypt(t *testing.T) {
	key := util.StringToSliceByte(strKey)
	t.Logf("key: %v", hex.EncodeToString(key))
	a, _ := util.NewAes(key)
	b := util.StringToSliceByte("hello world")
	tt, _ := a.EncryptAES(b)
	t.Logf("encrypt: %v", hex.EncodeToString(tt))
}

func TestDecrypt(t *testing.T) {
	key := util.StringToSliceByte(strKey)
	t.Logf("key: %v, length %d", key, len(key))
	a, _ := util.NewAes(key)
	d, _ := hex.DecodeString("4ee7b0e6119f0cd6d6b8e8eca02a85c5ccf86a9a48082868f27977c9f4ba41ae")
	tt, _ := a.DecryptAES(d)
	t.Logf("encrypt: %s", tt)
}
