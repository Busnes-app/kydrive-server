package vaultclient

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/Busnes-app/ky-primitives/hybridsig"
	"github.com/Busnes-app/kydrive-server/internal/vault"
)

// The wire representation is the same as drive.VaultKey, which is server-only.
func (k MemberKey) Fingerprint() string {
	body, _ := json.Marshal(k)
	return vault.Digest(body)
}

type MemberPin struct {
	User        string `json:"user"`
	Fingerprint string `json:"fingerprint"`
}

type Invitation struct {
	Vault string    `json:"vault"`
	Owner MemberPin `json:"owner"`
}

type HistoryInput struct {
	Invite     Invitation
	Records    []vault.Record
	Keys       map[string]MemberKey
	Checkpoint vault.Checkpoint
	WitnessKey string
	// Recovery holds members' recovery chains (GET /api/vault-recovery/{user}/records). Older
	// records may pin a chain's earlier keys, and a rekey verifies only when its target's chain
	// replaces the pinned key with the new one.
	Recovery map[string][]RecoveryRecord
}

// key returns the user's key, current or from their recovery chain, with this fingerprint.
func (in HistoryInput) key(user, fingerprint string) (MemberKey, bool) {
	keys := []MemberKey{in.Keys[user]}
	for _, r := range in.Recovery[user] {
		keys = append(keys, MemberKey(r.Key))
	}
	for _, k := range keys {
		if k.Fingerprint() == fingerprint {
			return k, true
		}
	}
	return MemberKey{}, false
}

// replaced reports whether the user's chain moves from one key to a later, different one.
func (in HistoryInput) replaced(user, from, to string) bool {
	seen := false
	for _, r := range in.Recovery[user] {
		fp := r.Key.Fingerprint()
		seen = seen || fp == from
		if seen && fp == to && to != from {
			return true
		}
	}
	return false
}

type verifiedHistory struct {
	head    vault.Head
	records []vault.Record
	keys    map[string]MemberKey
}

type openVault struct {
	verifiedHistory
	identity   *age.HybridIdentity
	index      []byte
	checkpoint vault.Checkpoint
	witness    *hybridsig.PublicKey
}

// Check the genesis against a Fingerprint the Session already trusts. All later key pins
// come from the signed manager transitions and are compared to the directory before use.
func (b *Session) verifyHistory(in HistoryInput) (verifiedHistory, error) {
	refusal := errors.New("The vault membership records did not verify")
	if b.member == nil || len(in.Records) == 0 || !vault.ID(in.Invite.Vault) {
		return verifiedHistory{}, refusal
	}
	genesis := in.Records[0]
	if _, ok := in.key(genesis.Signer, in.Invite.Owner.Fingerprint); genesis.Signer != in.Invite.Owner.User || !ok {
		return verifiedHistory{}, errors.New("The vault owner fingerprint does not match the invitation")
	}
	for user, chain := range in.Recovery {
		var prev *RecoveryRecord
		for i := range chain {
			if chain[i].User != user || chain[i].Org != b.org || vault.VerifyRecovery(prev, chain[i]) != nil {
				return verifiedHistory{}, refusal
			}
			prev = &chain[i]
		}
	}
	var before *vault.Record
	recipients := make(map[string]bool)
	for i := range in.Records {
		record := &in.Records[i]
		// The signer's key is the one the record pins for them (their new key for a self-rekey).
		pin, found := record.State.Member(record.Signer)
		if !found && before != nil {
			pin, _ = before.State.Member(record.Signer)
		}
		signing, _ := in.key(record.Signer, pin.Fingerprint)
		signer, err := hybridsig.ParsePublicKey(signing.Signing)
		if err != nil || record.Org != b.org || record.Vault != in.Invite.Vault || vault.VerifyRecord(before, *record, signer) != nil {
			return verifiedHistory{}, refusal
		}
		if record.Change == "rekey" {
			// Only the target's recovery key can have moved their pin, whoever signed.
			old, _ := before.State.Member(record.Target)
			next, _ := record.State.Member(record.Target)
			if !in.replaced(record.Target, old.Fingerprint, next.Fingerprint) {
				return verifiedHistory{}, refusal
			}
		}
		if before == nil || before.State.Generation != record.State.Generation {
			if recipients[record.State.Recipient] {
				return verifiedHistory{}, refusal
			}
			recipients[record.State.Recipient] = true
		}
		for _, m := range record.State.Members {
			key, found := in.key(m.User, m.Fingerprint)
			if !found || !vault.ValidRecipient(key.Recipient) {
				return verifiedHistory{}, errors.New("A vault member fingerprint does not match the signed record")
			}
		}
		before = record
	}
	current := in.Records[len(in.Records)-1]
	return verifiedHistory{vault.Head{Log: genesis.Link().Hash(), Sequence: current.Sequence, Hash: current.Link().Hash()}, in.Records, in.Keys}, nil
}

func (b *Session) OpenHistory(in HistoryInput) (vault.State, error) {
	b.active = nil
	verified, err := b.verifyHistory(in)
	if err != nil {
		return vault.State{}, err
	}
	witness, err := vault.ParsePublicKey(in.WitnessKey)
	if err != nil || vault.VerifyCheckpoint(witness, in.Checkpoint, verified.head, time.Now()) != nil {
		return vault.State{}, errors.New("The vault witness checkpoint did not verify")
	}
	state := verified.records[len(verified.records)-1].State
	member, ok := state.Member(b.user)
	if !ok {
		return vault.State{}, errors.New("This account is no longer a vault member")
	}
	if member.Fingerprint != b.publicKey().Fingerprint() {
		return vault.State{}, errors.New("The vault key fingerprint changed")
	}
	key, err := unwrapIdentity(member.Wrap, b.member, state.Recipient)
	if err != nil {
		return vault.State{}, err
	}
	index, err := decrypt(state.NameIndex, key, 32)
	if err != nil || len(index) != 32 {
		return vault.State{}, errors.New("The vault name-index key did not verify")
	}
	b.active = &openVault{verified, key, index, in.Checkpoint, witness}
	return state, nil
}

func unwrapIdentity(wrap []byte, recipient *age.HybridIdentity, expected string) (*age.HybridIdentity, error) {
	secret, err := decrypt(wrap, recipient, 4096)
	if err != nil {
		return nil, err
	}
	key, err := age.ParseHybridIdentity(string(secret))
	if err != nil || key.Recipient().String() != expected {
		return nil, errors.New("The vault key did not verify")
	}
	return key, nil
}

type MembershipChange struct {
	Action string
	Target MemberPin
	Key    MemberKey
}

func (b *Session) ChangeMembership(in MembershipChange) (vault.Record, error) {
	if b.active == nil || !vault.ValidUserID(in.Target.User) || in.Target.User == b.user {
		return vault.Record{}, errors.New("Open a vault and choose another member")
	}
	before := b.active.records[len(b.active.records)-1]
	actor, found := before.State.Member(b.user)
	if !found || actor.Role != vault.Manager {
		return vault.Record{}, errors.New("A vault manager must change membership")
	}
	record := before
	record.Sequence, record.Previous, record.Signer, record.Change, record.Target = before.Sequence+1, b.active.head.Hash, b.user, in.Action, in.Target.User
	record.State.Members = slices.Clone(before.State.Members)
	_, present := before.State.Member(in.Target.User)
	switch in.Action {
	case "add":
		if present || in.Target.Fingerprint != in.Key.Fingerprint() {
			return vault.Record{}, errors.New("The recipient fingerprint does not match the independently verified key")
		}
		if _, err := hybridsig.ParsePublicKey(in.Key.Signing); err != nil {
			return vault.Record{}, errors.New("Invalid member signing key")
		}
		recipient, err := age.ParseHybridRecipient(in.Key.Recipient)
		if err != nil {
			return vault.Record{}, errors.New("Invalid member recipient key")
		}
		wrap, err := encrypt([]byte(b.active.identity.String()), recipient)
		if err != nil {
			return vault.Record{}, err
		}
		record.State.Members = append(record.State.Members, vault.Member{User: in.Target.User, Fingerprint: in.Target.Fingerprint, Role: vault.Editor, Wrap: wrap})
		slices.SortFunc(record.State.Members, func(a, b vault.Member) int { return strings.Compare(a.User, b.User) })
	case "remove":
		if !present {
			return vault.Record{}, errors.New("Choose a current vault member")
		}
		record.State.Members = slices.DeleteFunc(record.State.Members, func(m vault.Member) bool { return m.User == in.Target.User })
		if err := b.rotate(&record.State); err != nil {
			return vault.Record{}, err
		}
	default:
		return vault.Record{}, errors.New("Invalid vault membership operation")
	}
	signed, err := record.Sign(b.signing)
	if err != nil {
		return vault.Record{}, err
	}
	if err := vault.VerifyRecord(&before, signed, b.signing.Public()); err != nil {
		return vault.Record{}, errors.New("The proposed membership change did not verify")
	}
	return signed, nil
}

func (b *Session) rotate(state *vault.State) error {
	next, err := age.GenerateHybridIdentity()
	if err != nil {
		return err
	}
	previous, err := encrypt([]byte(b.active.identity.String()), next.Recipient())
	if err != nil {
		return err
	}
	index, err := encrypt(b.active.index, next.Recipient())
	if err != nil {
		return err
	}
	for i, m := range state.Members {
		recipient, err := age.ParseHybridRecipient(b.active.keys[m.User].Recipient)
		if err != nil {
			return err
		}
		state.Members[i].Wrap, err = encrypt([]byte(next.String()), recipient)
		if err != nil {
			return err
		}
	}
	state.Generation++
	state.Recipient, state.Previous, state.NameIndex = next.Recipient().String(), previous, index
	return nil
}
