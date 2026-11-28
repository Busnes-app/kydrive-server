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
	"github.com/Busnes-app/ky-primitives/hybridsig"
	"github.com/Busnes-app/kydrive-server/internal/vault"
	"github.com/google/uuid"
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

type memberKey struct {
	Recipient string `json:"recipient"`
	Signing   []byte `json:"signing"`
}

type browser struct {
	account account
	device  *ecdsa.PrivateKey
	member  *age.HybridIdentity
	signing *hybridsig.PrivateKey
	active  *openVault
}

func (b *browser) publicKey() memberKey {
	return memberKey{b.member.Recipient().String(), b.signing.Public().Bytes()}
}

func (b *browser) invoke(action string, input json.RawMessage) (any, error) {
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
		if b.member == nil {
			return nil, errors.New("Unlock your vault key first")
		}
		return b.publicKey(), nil
	case "createVault":
		return b.createVault()
	case "identity":
		if b.member == nil {
			return nil, errors.New("Unlock your vault key first")
		}
		return memberPin{b.account.User, b.publicKey().fingerprint()}, nil
	case "verifyHistory", "openVault":
		var in historyInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		if action == "verifyHistory" {
			checked, err := b.verifyHistory(in)
			return checked.head, err
		}
		return b.openHistory(in)
	case "changeMembership":
		var in membershipChange
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return b.changeMembership(in)
	case "sealRevision":
		var in revisionInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return b.sealRevision(in)
	case "revisionPlaintextLimit":
		return revisionPlaintextLimit, nil
	case "readRevision":
		var in revisionRead
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return b.readRevision(in)
	case "bindEditor":
		var in editorInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return b.bindEditor(in)
	case "openEditor":
		var in editorOpen
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return b.openEditor(in)
	case "refreshCheckpoint":
		var checkpoint vault.Checkpoint
		if err := json.Unmarshal(input, &checkpoint); err != nil {
			return nil, err
		}
		if b.active == nil || vault.VerifyCheckpoint(b.active.witness, checkpoint, b.active.head, time.Now()) != nil {
			return nil, errors.New("The vault witness checkpoint did not verify")
		}
		b.active.checkpoint = checkpoint
		return nil, nil
	case "closeVault":
		b.active = nil
		return nil, nil
	default:
		return nil, errors.New("Invalid vault browser operation")
	}
}

func validAccount(a account) bool {
	return strings.HasPrefix(a.Token, "kyd_") && vault.ValidUserID(a.User) && vault.ID(a.Org)
}

func (b *browser) requestHeaders(origin, method, path string, body []byte) (map[string]string, error) {
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
	message := strings.Join([]string{"kydrive-request-v1", origin, method, path, date, encodedNonce, vault.Digest(nil), digest}, "|")
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
	if b.device == nil || !validAccount(b.account) || b.member != nil || len(passphrase) < 12 {
		return nil, errors.New("Link your browser and choose a passphrase of at least 12 characters")
	}
	member, err := age.GenerateHybridIdentity()
	if err != nil {
		return nil, err
	}
	signing, err := hybridsig.Generate()
	if err != nil {
		return nil, err
	}
	device, err := x509.MarshalPKCS8PrivateKey(b.device)
	if err != nil {
		return nil, err
	}
	ml, ed := signing.Seeds()
	bundle, err := json.Marshal(browserIdentity{b.account, device, member.String(), ml, ed})
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
	b.member, b.signing = member, signing
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
	member, err := age.ParseHybridIdentity(secret.RecipientSecret)
	if err != nil {
		return "", err
	}
	signing, err := hybridsig.FromSeeds(secret.MLDSASeed, secret.EdSeed)
	if err != nil {
		return "", err
	}
	parsed, err := x509.ParsePKCS8PrivateKey(secret.Device)
	device, ok := parsed.(*ecdsa.PrivateKey)
	if err != nil || !ok || device.Curve != elliptic.P256() {
		return "", errors.New("Invalid browser device key")
	}
	*b = browser{account: secret.Account, device: device, member: member, signing: signing}
	return b.account.User, nil
}

func (b *browser) createVault() (vault.Record, error) {
	if b.member == nil {
		return vault.Record{}, errors.New("Unlock the vault key first")
	}
	key, err := age.GenerateHybridIdentity()
	if err != nil {
		return vault.Record{}, err
	}
	wrapped, err := encrypt([]byte(key.String()), b.member.Recipient())
	if err != nil {
		return vault.Record{}, err
	}
	indexKey := make([]byte, 32)
	if _, err := rand.Read(indexKey); err != nil {
		return vault.Record{}, err
	}
	index, err := encrypt(indexKey, key.Recipient())
	if err != nil {
		return vault.Record{}, err
	}
	record := vault.Record{
		Org: b.account.Org, Vault: uuid.NewString(), Sequence: 1, Signer: b.account.User, Change: "create",
		State: vault.State{Generation: 1, Recipient: key.Recipient().String(), NameIndex: index,
			Members: []vault.Member{{User: b.account.User, Fingerprint: b.publicKey().fingerprint(), Role: vault.Manager, Wrap: wrapped}}},
	}
	return record.Sign(b.signing)
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
