package auth_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/auth"
)

func TestProofOfWorkChallenge(t *testing.T) {
	const secret = "test-signing-secret"
	challenge, err := auth.GeneratePoWChallenge(1000, secret)
	if err != nil {
		t.Fatalf("failed to generate PoW challenge: %v", err)
	}

	// Solve challenge client-side
	var winningNumber int = -1
	for i := 1; i <= challenge.MaxNumber; i++ {
		sol := auth.PoWSolution{
			Algorithm: challenge.Algorithm,
			Salt:      challenge.Salt,
			Challenge: challenge.Challenge,
			Number:    i,
			MaxNumber: challenge.MaxNumber,
			ExpiresAt: challenge.ExpiresAt,
			Signature: challenge.Signature,
		}
		solBytes, _ := json.Marshal(sol)
		solB64 := base64.StdEncoding.EncodeToString(solBytes)
		if auth.VerifyPoWSolution(solB64, secret) {
			winningNumber = i
			break
		}
	}

	if winningNumber == -1 {
		t.Errorf("failed to solve PoW within max iterations")
	}
	forged := auth.PoWSolution{Algorithm: "SHA-256", Salt: "mine", Challenge: "fake", Number: 1, MaxNumber: 1, ExpiresAt: challenge.ExpiresAt, Signature: challenge.Signature}
	forgedJSON, _ := json.Marshal(forged)
	if auth.VerifyPoWSolution(base64.StdEncoding.EncodeToString(forgedJSON), secret) {
		t.Fatal("accepted forged challenge metadata")
	}
}

func TestPolicyValidation(t *testing.T) {
	if err := auth.ValidatePassword("short"); err != auth.ErrPasswordTooShort {
		t.Errorf("expected ErrPasswordTooShort, got %v", err)
	}
	if err := auth.ValidatePassword("ValidLongPassword123!"); err != nil {
		t.Errorf("expected valid password, got %v", err)
	}

	if err := auth.ValidateUsername("a"); err != auth.ErrInvalidUsername {
		t.Errorf("expected ErrInvalidUsername for short username, got %v", err)
	}
	if err := auth.ValidateUsername("alice.ops_01"); err != nil {
		t.Errorf("expected valid username, got %v", err)
	}

	if err := auth.ValidateEmail("not-an-email"); err != auth.ErrInvalidEmail {
		t.Errorf("expected ErrInvalidEmail, got %v", err)
	}
	if err := auth.ValidateEmail("alice@busnes.app"); err != nil {
		t.Errorf("expected valid email, got %v", err)
	}
}

func TestPoWSpentRefusesReplay(t *testing.T) {
	const secret = "test-signing-secret"
	challenge, err := auth.GeneratePoWChallenge(200, secret)
	if err != nil {
		t.Fatal(err)
	}
	var token string
	for i := 1; i <= challenge.MaxNumber && token == ""; i++ {
		raw, _ := json.Marshal(auth.PoWSolution{Algorithm: challenge.Algorithm, Salt: challenge.Salt, Challenge: challenge.Challenge, Number: i, MaxNumber: challenge.MaxNumber, ExpiresAt: challenge.ExpiresAt, Signature: challenge.Signature})
		if candidate := base64.StdEncoding.EncodeToString(raw); auth.VerifyPoWSolution(candidate, secret) {
			token = candidate
		}
	}
	var spent auth.PoWSpent
	if spent.Verify("garbage", secret) {
		t.Fatal("accepted garbage")
	}
	if !spent.Verify(token, secret) {
		t.Fatal("rejected a fresh solution")
	}
	if spent.Verify(token, secret) {
		t.Fatal("accepted a replayed solution")
	}
}
