package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

const (
	authNonceSize = 32
	authMACSize   = sha256.Size
)

func hmacSHA256(key []byte, parts ...[]byte) []byte {
	mac := hmac.New(sha256.New, key)
	for _, part := range parts {
		_, _ = mac.Write(part)
	}
	return mac.Sum(nil)
}

func deriveWorkerSessionKey(secret, nonce []byte, workerID string) []byte {
	return hmacSHA256(secret, []byte("NODREN-SESSION-KEY-v1\x00"), nonce, []byte(workerID))
}

func authenticateWorker(conn net.Conn, secrets map[string]string) (string, []byte, error) {
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetDeadline(time.Time{})

	nonce := make([]byte, authNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", nil, fmt.Errorf("create Worker challenge: %w", err)
	}
	if err := writeFrame(conn, MsgAuthChallenge, 0, nonce); err != nil {
		return "", nil, err
	}
	response, err := readFrame(conn)
	if err != nil {
		return "", nil, err
	}
	if response.Type != MsgAuthResponse || response.RequestID != 0 {
		return "", nil, errors.New("invalid Worker authentication response")
	}
	cursor := 0
	workerID, err := readString(response.Payload, &cursor)
	if err != nil || !safeWorkerID(workerID) || len(response.Payload)-cursor != authMACSize {
		return "", nil, errors.New("invalid Worker authentication identity or proof")
	}
	secret, authorized := secrets[workerID]
	if !authorized {
		return "", nil, errors.New("Worker identity is not authorized")
	}
	proof := hmacSHA256([]byte(secret), []byte("NODREN-WORKER-LOGIN-v1\x00"), nonce, []byte(workerID))
	if !hmac.Equal(proof, response.Payload[cursor:]) {
		return "", nil, errors.New("Worker authentication failed")
	}

	key := deriveWorkerSessionKey([]byte(secret), nonce, workerID)
	serverProof := hmacSHA256(key, []byte("NODREN-CONTROLLER-LOGIN-v1\x00"), nonce, []byte(workerID))
	if err := writeFrame(conn, MsgAuthResult, 0, serverProof); err != nil {
		return "", nil, err
	}
	return workerID, key, nil
}

func safeWorkerID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for index, value := range id {
		if value >= 'a' && value <= 'z' ||
			value >= 'A' && value <= 'Z' ||
			value >= '0' && value <= '9' ||
			index > 0 && (value == '-' || value == '_' || value == '.') {
			continue
		}
		return false
	}
	return true
}

func validateWorkerTokens(tokens map[string]string) error {
	if len(tokens) == 0 {
		return errors.New("secure mode requires NODREN_WORKER_TOKENS with per-worker credentials")
	}
	seen := make(map[string]struct{}, len(tokens))
	for id, token := range tokens {
		if !safeWorkerID(id) || len(token) < 32 {
			return fmt.Errorf("NODREN_WORKER_TOKENS has an invalid identity or short credential")
		}
		if _, duplicate := seen[token]; duplicate {
			return errors.New("each Worker must have a unique NODREN_WORKER_TOKENS credential")
		}
		seen[token] = struct{}{}
	}
	return nil
}

func protectFramePayload(key []byte, direction byte, messageType MessageType, requestID, sequence uint64, payload []byte) []byte {
	var typeBytes [2]byte
	var requestBytes [8]byte
	var sequenceBytes [8]byte
	binary.LittleEndian.PutUint16(typeBytes[:], uint16(messageType))
	binary.LittleEndian.PutUint64(requestBytes[:], requestID)
	binary.LittleEndian.PutUint64(sequenceBytes[:], sequence)
	tag := hmacSHA256(key, []byte("NODREN-FRAME-v1\x00"), []byte{direction}, typeBytes[:], requestBytes[:], sequenceBytes[:], payload)
	protected := make([]byte, 0, 8+len(payload)+len(tag))
	protected = append(protected, sequenceBytes[:]...)
	protected = append(protected, payload...)
	protected = append(protected, tag...)
	return protected
}

func verifyFramePayload(key []byte, direction byte, messageType MessageType, requestID, expectedSequence uint64, protected []byte) ([]byte, error) {
	if len(protected) < 8+authMACSize {
		return nil, errors.New("authenticated frame is truncated")
	}
	sequence := binary.LittleEndian.Uint64(protected[:8])
	if sequence != expectedSequence {
		return nil, errors.New("authenticated frame sequence mismatch")
	}
	payloadEnd := len(protected) - authMACSize
	payload := protected[8:payloadEnd]
	expected := protectFramePayload(key, direction, messageType, requestID, sequence, payload)
	if !hmac.Equal(expected[len(expected)-authMACSize:], protected[payloadEnd:]) {
		return nil, errors.New("authenticated frame MAC mismatch")
	}
	return payload, nil
}
