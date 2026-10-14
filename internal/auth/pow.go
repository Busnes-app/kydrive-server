package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"sync"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/crypto"
)

// PoWChallenge represents a client-side SHA256 puzzle matching the KySecurity standard.
type PoWChallenge struct {
	Algorithm string `json:"algorithm"`
	Salt      string `json:"salt"`
	Challenge string `json:"challenge"`
	MaxNumber int    `json:"maxnumber"`
	ExpiresAt int64  `json:"expires_at"`
	Signature string `json:"signature"`
}

// PoWSolution represents the client's submitted answer.
type PoWSolution struct {
	Algorithm string `json:"algorithm"`
	Salt      string `json:"salt"`
	Challenge string `json:"challenge"`
	Number    int    `json:"number"`
	MaxNumber int    `json:"maxnumber"`
	ExpiresAt int64  `json:"expires_at"`
	Signature string `json:"signature"`
}

// GeneratePoWChallenge creates a puzzle whose answer is uniform in [1, difficulty]; the client
// searches linearly, so it costs difficulty/2 SHA-256 operations on average.
func GeneratePoWChallenge(difficulty int, signingSecret string) (*PoWChallenge, error) {
	if difficulty <= 0 {
		difficulty = 20000
	}

	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		return nil, err
	}
	salt := hex.EncodeToString(saltBytes)

	// Pick a random winning number in range [1, difficulty]
	nBig, err := rand.Int(rand.Reader, big.NewInt(int64(difficulty)))
	if err != nil {
		return nil, err
	}
	targetNumber := int(nBig.Int64()) + 1

	targetStr := fmt.Sprintf("%s%d", salt, targetNumber)
	h := sha256.Sum256([]byte(targetStr))
	challenge := hex.EncodeToString(h[:])

	challengeExpires := time.Now().Add(5 * time.Minute).Unix()
	signaturePayload := fmt.Sprintf("%s:%s:%d:%d", salt, challenge, difficulty, challengeExpires)
	return &PoWChallenge{
		Algorithm: "SHA-256",
		Salt:      salt,
		Challenge: challenge,
		MaxNumber: difficulty,
		ExpiresAt: challengeExpires,
		Signature: crypto.ComputeHMACSHA256([]byte(signaturePayload), signingSecret),
	}, nil
}

// VerifyPoWSolution decodes and validates the client's base64-encoded JSON solution. It is
// stateless; login uses PoWSpent.Verify, which also refuses a replayed solution.
func VerifyPoWSolution(solutionBase64, signingSecret string) bool {
	_, ok := verifyPoW(solutionBase64, signingSecret)
	return ok
}

func verifyPoW(solutionBase64, signingSecret string) (*PoWSolution, bool) {
	data, err := base64.StdEncoding.DecodeString(solutionBase64)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(solutionBase64)
		if err != nil {
			return nil, false
		}
	}

	var sol PoWSolution
	if err := json.Unmarshal(data, &sol); err != nil {
		return nil, false
	}

	if sol.Algorithm != "SHA-256" || sol.Salt == "" || sol.Challenge == "" || sol.Signature == "" ||
		sol.Number < 1 || sol.MaxNumber < 1 || sol.Number > sol.MaxNumber || sol.ExpiresAt < time.Now().Unix() {
		return nil, false
	}
	signaturePayload := sol.Salt + ":" + sol.Challenge + ":" + strconv.Itoa(sol.MaxNumber) + ":" + strconv.FormatInt(sol.ExpiresAt, 10)
	if !crypto.VerifyHMACSHA256([]byte(signaturePayload), signingSecret, sol.Signature) {
		return nil, false
	}

	targetStr := fmt.Sprintf("%s%d", sol.Salt, sol.Number)
	h := sha256.Sum256([]byte(targetStr))
	return &sol, hex.EncodeToString(h[:]) == sol.Challenge
}

// PoWSpent remembers verified salts until their challenge expires, so each solution buys one
// attempt. In memory, like the login limiter: valid for the single-instance server only.
type PoWSpent struct {
	mu    sync.Mutex
	spent map[string]int64 // salt -> challenge expiry
	prune int64
}

// Verify accepts a valid solution once.
func (p *PoWSpent) Verify(solutionBase64, signingSecret string) bool {
	sol, ok := verifyPoW(solutionBase64, signingSecret)
	if !ok {
		return false
	}
	now := time.Now().Unix()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.spent == nil {
		p.spent = map[string]int64{}
	}
	if now >= p.prune {
		for salt, exp := range p.spent {
			if exp < now {
				delete(p.spent, salt)
			}
		}
		p.prune = now + 60
	}
	if _, used := p.spent[sol.Salt]; used {
		return false
	}
	p.spent[sol.Salt] = sol.ExpiresAt
	return true
}
