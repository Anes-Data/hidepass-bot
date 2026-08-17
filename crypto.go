package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"

	"golang.org/x/crypto/argon2"
)

const (
	saltSize = 16
	keySize  = 32

	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 4

	minPassphraseLength = 8

	standardNonceSize = 12
)

func deriveKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, keySize)
}

func encryptText(plainText string, passphrase string) ([]byte, error) {
	if len(passphrase) < minPassphraseLength {
		return nil, errors.New("passphrase is too short, must be at least 8 characters")
	}

	salt := make([]byte, saltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}

	key := deriveKey(passphrase, salt)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	cipherText := gcm.Seal(nil, nonce, []byte(plainText), nil)

	finalPayload := make([]byte, 0, len(salt)+len(nonce)+len(cipherText))
	finalPayload = append(finalPayload, salt...)
	finalPayload = append(finalPayload, nonce...)
	finalPayload = append(finalPayload, cipherText...)

	return finalPayload, nil
}

func decryptText(cipherData []byte, passphrase string) (string, error) {
	if len(passphrase) < minPassphraseLength {
		return "", errors.New("passphrase is too short, must be at least 8 characters")
	}

	const minRequiredLen = saltSize + standardNonceSize + 16
	if len(cipherData) < minRequiredLen {
		return "", errors.New("invalid or truncated ciphertext payload")
	}

	salt := cipherData[:saltSize]
	nonce := cipherData[saltSize : saltSize+standardNonceSize]
	cipherText := cipherData[saltSize+standardNonceSize:]

	key := deriveKey(passphrase, salt)

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	plainText, err := gcm.Open(nil, nonce, cipherText, nil)
	if err != nil {
		return "", errors.New("invalid passphrase or corrupted data")
	}

	return string(plainText), nil
}