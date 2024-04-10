package util

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
)

type Aes struct {
	block cipher.Block
	mode  cipher.BlockMode
}

// GenerateKey 生成一个 AES 密钥。
func GenerateKey() ([]byte, error) {
	key := make([]byte, 32) // AES-256 需要 32 字节长的密钥
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

// pad 对数据进行填充，使其长度为块大小的倍数。
func pad(src []byte) []byte {
	padding := aes.BlockSize - len(src)%aes.BlockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(src, padtext...)
}

// unpad 移除填充数据。
func unpad(src []byte) []byte {
	length := len(src)
	unpadding := int(src[length-1])
	return src[:(length - unpadding)]
}

func (a *Aes) EncryptAES(text []byte) ([]byte, error) {
	msg := pad(text)
	ciphertext := make([]byte, aes.BlockSize+len(msg))
	iv := ciphertext[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}

	mode := cipher.NewCBCEncrypter(a.block, iv)
	mode.CryptBlocks(ciphertext[aes.BlockSize:], msg)

	return ciphertext, nil
}

func (a *Aes) DecryptAES(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < aes.BlockSize {
		return nil, errors.New("ciphertext too short")
	}
	iv := ciphertext[:aes.BlockSize]
	ciphertext = ciphertext[aes.BlockSize:]

	mode := cipher.NewCBCDecrypter(a.block, iv)
	mode.CryptBlocks(ciphertext, ciphertext)
	return unpad(ciphertext), nil
}

func NewAes(key []byte) (*Aes, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return &Aes{block: block}, nil
}
