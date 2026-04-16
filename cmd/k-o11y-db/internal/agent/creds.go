package agent

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"fmt"
)

// DecryptAESGCM은 AES-256-GCM으로 암호화된 문자열을 복호화합니다.
// SigNoz pkg/crypto/aes.go의 Decrypt()와 동일한 로직입니다.
//
// ciphertextHex: hex 인코딩된 암호문 (nonce 12바이트 prepended)
// keyHex: 64자 hex 문자열 (32바이트 AES-256 키)
func DecryptAESGCM(ciphertextHex, keyHex string) (string, error) {
	if ciphertextHex == "" {
		return "", nil
	}

	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return "", fmt.Errorf("invalid key hex: %w", err)
	}
	if len(key) != 32 {
		return "", fmt.Errorf("key must be 32 bytes (64 hex chars), got %d bytes", len(key))
	}

	ciphertext, err := hex.DecodeString(ciphertextHex)
	if err != nil {
		return "", fmt.Errorf("invalid ciphertext hex: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed: %w", err)
	}

	return string(plaintext), nil
}
