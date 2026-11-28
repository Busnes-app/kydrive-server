// The trusted browser site uses the existing Go vault wire code and crypto.
package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"syscall/js"
	"time"

	"filippo.io/age"
	"github.com/Busnes-app/kydrive-server/internal/vault"
	"github.com/Busnes-app/kydrive-server/pkg/vaultclient"
)

type account struct {
	Token string `json:"token"`
	User  string `json:"user_id"`
	Org   string `json:"org_id"`
}

type browserIdentity struct {
	Account         account
	Device          []byte
	RecipientSecret string
	MLDSASeed       []byte
	EdSeed          []byte
}

type browser struct {
	account  account
	device   *ecdsa.PrivateKey
	identity *vaultclient.Identity
	vault    *vaultclient.Session
}

func (b *browser) invoke(action string, input json.RawMessage) (any, error) {
	switch action {
	case "createVault", "verifyHistory", "openVault", "changeMembership", "sealRevision", "readRevision", "bindEditor", "openEditor", "refreshCheckpoint":
		if b.vault == nil {
			return nil, errors.New("Unlock your vault key first")
		}
	}
	switch action {
	case "device":
		device, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		spki, err := x509.MarshalPKIXPublicKey(&device.PublicKey)
		if err != nil {
			return nil, err
		}
		b.device = device
		return spki, nil
	case "link":
		var a account
		if err := json.Unmarshal(input, &a); err != nil || !validAccount(a) || b.device == nil {
			return nil, errors.New("Invalid browser approval")
		}
		b.account = a
		return a.User, nil
	case "sign":
		var request struct {
			Origin, Method, Path string
			Body                 []byte
		}
		if err := json.Unmarshal(input, &request); err != nil {
			return nil, err
		}
		return b.requestHeaders(request.Origin, request.Method, request.Path, request.Body)
	case "createKey":
		var passphrase string
		if err := json.Unmarshal(input, &passphrase); err != nil {
			return nil, err
		}
		return b.createKey(passphrase)
	case "unlock":
		var in struct {
			Passphrase string
			Sealed     []byte
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return b.unlock(in.Passphrase, in.Sealed)
	case "publicKey":
		if b.identity == nil {
			return nil, errors.New("Unlock your vault key first")
		}
		return b.identity.PublicKey(), nil
	case "createVault":
		return b.vault.CreateVault()
	case "identity":
		if b.identity == nil {
			return nil, errors.New("Unlock your vault key first")
		}
		return vaultclient.MemberPin{User: b.account.User, Fingerprint: b.identity.PublicKey().Fingerprint()}, nil
	case "verifyHistory", "openVault":
		var in vaultclient.HistoryInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		if action == "verifyHistory" {
			return b.vault.VerifyHistory(in)
		}
		return b.vault.OpenHistory(in)
	case "changeMembership":
		var in vaultclient.MembershipChange
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return b.vault.ChangeMembership(in)
	case "sealRevision":
		var in vaultclient.RevisionInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return b.vault.SealRevision(in)
	case "revisionPlaintextLimit":
		return vaultclient.RevisionPlaintextLimit, nil
	case "readRevision":
		var in vaultclient.RevisionRead
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return b.vault.ReadRevision(in)
	case "bindEditor":
		var in vaultclient.EditorInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return b.vault.BindEditor(in)
	case "openEditor":
		var in vaultclient.EditorOpen
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return b.vault.OpenEditor(in)
	case "refreshCheckpoint":
		var checkpoint vault.Checkpoint
		if err := json.Unmarshal(input, &checkpoint); err != nil {
			return nil, err
		}
		return nil, b.vault.RefreshCheckpoint(checkpoint)
	case "closeVault":
		if b.vault != nil {
			b.vault.Close()
		}
		return nil, nil
	default:
		return nil, errors.New("Invalid vault browser operation")
	}
}

func validAccount(a account) bool {
	return strings.HasPrefix(a.Token, "kyd_") && vault.ValidUserID(a.User) && vault.ID(a.Org)
}

func (b *browser) requestHeaders(origin, method, requestURI string, body []byte) (map[string]string, error) {
	if b.device == nil || !validAccount(b.account) {
		return nil, errors.New("Link this browser first")
	}
	date := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	encodedNonce := base64.RawURLEncoding.EncodeToString(nonce)
	digest := vault.Digest(body)
	// The wire format signs the entire request URI, including any query, as sent.
	// Vault requests do not use any headers from the device signing list.
	headerDigest := vault.Digest(nil)
	message := strings.Join([]string{"kydrive-request-v1", origin, method, requestURI, date, encodedNonce, headerDigest, digest}, "|")
	signed := sha256.Sum256([]byte(message))
	signature, err := ecdsa.SignASN1(rand.Reader, b.device, signed[:])
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"Authorization":          "Bearer " + b.account.Token,
		"KyDrive-Date":           date,
		"KyDrive-Nonce":          encodedNonce,
		"KyDrive-Content-SHA256": digest,
		"KyDrive-Signature":      base64.StdEncoding.EncodeToString(signature),
	}, nil
}

func (b *browser) createKey(passphrase string) ([]byte, error) {
	if b.device == nil || !validAccount(b.account) || b.identity != nil || len(passphrase) < 12 {
		return nil, errors.New("Link your browser and choose a passphrase of at least 12 characters")
	}
	identity, err := vaultclient.GenerateIdentity()
	if err != nil {
		return nil, err
	}
	session, err := vaultclient.NewSession(b.account.User, b.account.Org, identity)
	if err != nil {
		return nil, err
	}
	device, err := x509.MarshalPKCS8PrivateKey(b.device)
	if err != nil {
		return nil, err
	}
	secret, ml, ed := identity.Secrets()
	bundle, err := json.Marshal(browserIdentity{b.account, device, secret, ml, ed})
	if err != nil {
		return nil, err
	}
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, err
	}
	recipient.SetWorkFactor(17)
	sealed, err := encrypt(bundle, recipient)
	if err != nil {
		return nil, err
	}
	b.identity, b.vault = identity, session
	return sealed, nil
}

func (b *browser) unlock(passphrase string, sealed []byte) (string, error) {
	identity, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return "", err
	}
	identity.SetMaxWorkFactor(17)
	opened, err := decrypt(sealed, identity, 16<<10)
	if err != nil {
		return "", errors.New("Could not unlock the browser vault key")
	}
	var secret browserIdentity
	if err := json.Unmarshal(opened, &secret); err != nil || !validAccount(secret.Account) {
		return "", errors.New("Invalid browser vault key")
	}
	key, err := vaultclient.RestoreIdentity(secret.RecipientSecret, secret.MLDSASeed, secret.EdSeed)
	if err != nil {
		return "", err
	}
	session, err := vaultclient.NewSession(secret.Account.User, secret.Account.Org, key)
	if err != nil {
		return "", err
	}
	parsed, err := x509.ParsePKCS8PrivateKey(secret.Device)
	device, ok := parsed.(*ecdsa.PrivateKey)
	if err != nil || !ok || device.Curve != elliptic.P256() {
		return "", errors.New("Invalid browser device key")
	}
	*b = browser{account: secret.Account, device: device, identity: key, vault: session}
	return b.account.User, nil
}

func encrypt(data []byte, recipient age.Recipient) ([]byte, error) {
	var buffer bytes.Buffer
	w, err := age.Encrypt(&buffer, recipient)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func decrypt(data []byte, identity age.Identity, limit int64) ([]byte, error) {
	r, err := age.Decrypt(bytes.NewReader(data), identity)
	if err != nil {
		return nil, err
	}
	opened, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil || int64(len(opened)) > limit {
		return nil, errors.New("Invalid encrypted vault data")
	}
	return opened, nil
}

func main() {
	b := &browser{}
	js.Global().Set("kyDriveVault", js.FuncOf(func(_ js.Value, args []js.Value) any {
		value, err := b.invoke(args[0].String(), json.RawMessage(args[1].String()))
		result := struct {
			Value any    `json:"value"`
			Error string `json:"error,omitempty"`
		}{Value: value}
		if err != nil {
			result.Error = err.Error()
		}
		body, _ := json.Marshal(result)
		return string(body)
	}))
	select {}
}
