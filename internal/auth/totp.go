package auth

import (
	"time"

	"github.com/Busnes-app/ky-primitives/totp"
	"github.com/Busnes-app/kydrive-server/internal/crypto"
)

// GenerateTOTPSecret returns a fresh base32 RFC 6238 secret.
func GenerateTOTPSecret() (string, error) { return totp.GenerateSecret() }

// GenerateTOTPURL returns the otpauth:// URI an authenticator app enrols from.
func GenerateTOTPURL(issuer, accountName, secret string) string {
	return totp.ProvisioningURI(issuer, accountName, secret)
}

// ValidateTOTP reports whether code is valid for secret now, and the counter it matched.
// The caller must spend the counter with UserStore.SpendTOTPCounter before trusting it.
func ValidateTOTP(secret, code string) (int64, bool) {
	return totp.Validate(secret, code, time.Now())
}

// totpAAD binds a sealed secret to its user row, so a ciphertext copied to another row fails.
func totpAAD(userID string) []byte { return []byte("users.totp_secret_enc:" + userID) }

// SealTOTPSecret encrypts secret for userID under the deployment key's "kydrive:totp" subkey.
func SealTOTPSecret(key []byte, userID, secret string) (string, error) {
	k, err := crypto.DeriveKey(key, "kydrive:totp")
	if err != nil {
		return "", err
	}
	return crypto.EncryptAESGCMAAD([]byte(secret), k, totpAAD(userID))
}

// OpenTOTPSecret reverses SealTOTPSecret. It also accepts the legacy form (raw deployment key,
// no AAD) and reports legacy so the caller can reseal the row.
func OpenTOTPSecret(key []byte, userID, sealed string) (secret string, legacy bool, err error) {
	k, err := crypto.DeriveKey(key, "kydrive:totp")
	if err != nil {
		return "", false, err
	}
	if b, err := crypto.DecryptAESGCMAAD(sealed, k, totpAAD(userID)); err == nil {
		return string(b), false, nil
	}
	b, err := crypto.DecryptAESGCM(sealed, key)
	return string(b), err == nil, err
}
