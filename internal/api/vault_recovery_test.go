package api_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/Busnes-app/ky-primitives/hybridsig"
	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
	"github.com/Busnes-app/kydrive-server/internal/vault"
	"github.com/Busnes-app/kydrive-server/pkg/vaultclient"
	"github.com/google/uuid"
)

type recoveryFixture struct {
	t       *testing.T
	srv     *api.Server
	witness *vault.Witness
	wkey    string
	// The checkpoint and state of the last open.
	cp    vault.Checkpoint
	state vault.State
}

func (f recoveryFixture) do(device *signer, method, path string, payload any, want int) *httptest.ResponseRecorder {
	f.t.Helper()
	var data []byte
	if payload != nil {
		var err error
		if data, err = json.Marshal(payload); err != nil {
			f.t.Fatal(err)
		}
	}
	w := serve(f.srv, device.request(method, path, data))
	if w.Code != want {
		f.t.Fatalf("%s %s: status %d, want %d (%s)", method, path, w.Code, want, w.Body)
	}
	return w
}

func (f recoveryFixture) decode(w *httptest.ResponseRecorder, v any) {
	f.t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		f.t.Fatal(err)
	}
}

func (f recoveryFixture) head(device *signer, user string) vaultclient.RecoveryRecord {
	f.t.Helper()
	var chain struct{ Records []vaultclient.RecoveryRecord }
	f.decode(f.do(device, "GET", "/api/vault-recovery/"+user+"/records", nil, 200), &chain)
	return chain.Records[len(chain.Records)-1]
}

// open verifies the vault's whole log as a client does and opens it on sess.
func (f *recoveryFixture) open(sess *vaultclient.Session, device *signer, id string, owner vaultclient.MemberPin, keys map[string]vaultclient.MemberKey, chains map[string][]vaultclient.RecoveryRecord) error {
	f.t.Helper()
	var history struct {
		Records  []vault.Record  `json:"records"`
		Receipts []vault.Receipt `json:"receipts"`
	}
	f.decode(f.do(device, "GET", "/api/vaults/"+id+"/records", nil, 200), &history)
	body, _ := json.Marshal(history.Receipts)
	w := httptest.NewRecorder()
	f.witness.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/v1/logs/"+history.Receipts[0].Log, bytes.NewReader(body)))
	f.decode(w, &f.cp)
	var err error
	f.state, err = sess.OpenHistory(vaultclient.HistoryInput{
		Invite: vaultclient.Invitation{Vault: id, Owner: owner}, Records: history.Records, Keys: keys,
		Checkpoint: f.cp, WitnessKey: f.wkey, Recovery: chains,
	})
	return err
}

func signingKey(t *testing.T, id *vaultclient.Identity) *hybridsig.PrivateKey {
	t.Helper()
	_, ml, ed := id.Secrets()
	key, err := hybridsig.FromSeeds(ml, ed)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// must panics on a setup error, which fails the test with its stack.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func secretOf(id *vaultclient.Identity) string {
	member, _, _ := id.Secrets()
	return member
}

func pin(user string, key vaultclient.MemberKey) vaultclient.MemberPin {
	return vaultclient.MemberPin{User: user, Fingerprint: key.Fingerprint()}
}

func TestVaultRecoveryKeyReplacement(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	publisher := must(hybridsig.Generate())
	witnessKey := must(hybridsig.Generate())
	witness := must(vault.InitializeWitness(t.Context(), filepath.Join(t.TempDir(), "witness.db"), witnessKey, publisher.Public()))
	t.Cleanup(func() { witness.Close() })
	cfg.Vault = config.VaultConfig{Publisher: publisher, Witness: witnessKey.Public()}
	srv = api.NewServer(cfg, st)
	f := &recoveryFixture{t: t, srv: srv, witness: witness, wkey: base64.StdEncoding.EncodeToString(witnessKey.Public().Bytes())}
	origin := api.DeviceOriginForTest(srv)
	org := drive.DefaultOrg
	const aliceID, bobID, carolID = "usr_vralice", "usr_vrbob", "usr_vrcarol"
	alice := linkSigned(t, srv, loginAs(t, srv, st, "vralice", "user"), origin)
	bob := linkSigned(t, srv, loginAs(t, srv, st, "vrbob", "user"), origin)
	carol := linkSigned(t, srv, loginAs(t, srv, st, "vrcarol", "user"), origin)

	aliceKey, bobKey, carolKey := must(vaultclient.GenerateIdentity()), must(vaultclient.GenerateIdentity()), must(vaultclient.GenerateIdentity())
	aliceRecovery, bobRecovery, carolRecovery := must(vaultclient.GenerateRecoveryKey()), must(vaultclient.GenerateRecoveryKey()), must(vaultclient.GenerateRecoveryKey())
	for _, enrol := range []struct {
		device   *signer
		user     string
		identity *vaultclient.Identity
		recovery *vaultclient.RecoveryKey
	}{{alice, aliceID, aliceKey, aliceRecovery}, {bob, bobID, bobKey, bobRecovery}, {carol, carolID, carolKey, carolRecovery}} {
		f.do(enrol.device, "PUT", "/api/vault-keys", enrol.identity.PublicKey(), 204)
		upload := must(vaultclient.RegisterRecovery(org, enrol.user, enrol.identity, enrol.recovery))
		f.do(enrol.device, "POST", "/api/vault-recovery", upload, 204)
		f.do(enrol.device, "POST", "/api/vault-recovery", upload, 204) // an exact retry
	}
	// Registration cannot be repeated with another recovery key.
	f.do(carol, "POST", "/api/vault-recovery", must(vaultclient.RegisterRecovery(org, carolID, carolKey, aliceRecovery)), 409)

	// The copy the server returns is ciphertext that only the recovery key opens.
	var fetched vaultclient.RecoveryUpload
	f.decode(f.do(bob, "GET", "/api/vault-recovery", nil, 200), &fetched)
	if strings.Contains(string(fetched.Copy), "AGE-SECRET-KEY") || bytes.Contains(fetched.Copy, func() []byte { _, ml, _ := bobKey.Secrets(); return ml }()) {
		t.Fatal("the recovery copy holds plaintext key material")
	}
	if got, err := vaultclient.OpenRecovery(fetched, bobRecovery); err != nil || got.PublicKey().Fingerprint() != bobKey.PublicKey().Fingerprint() {
		t.Fatalf("recovery copy did not open to the enrolled key: %v", err)
	}
	if _, err := vaultclient.OpenRecovery(fetched, carolRecovery); err == nil {
		t.Fatal("another account's recovery key opened the copy")
	}
	// Other accounts see only the public chain, never a copy.
	chain := f.do(alice, "GET", "/api/vault-recovery/"+bobID+"/records", nil, 200).Body.String()
	if strings.Contains(chain, base64.StdEncoding.EncodeToString(fetched.Copy[:48])) {
		t.Fatal("the public chain returned the recovery copy")
	}
	f.do(alice, "GET", "/api/vault-recovery/usr_nobody/records", nil, 404)

	// Alice creates a shared vault and adds Bob.
	aliceSession := must(vaultclient.NewSession(aliceID, org, aliceKey))
	create := must(aliceSession.CreateVault())
	id := create.Vault
	owner := pin(aliceID, aliceKey.PublicKey())
	f.do(alice, "POST", "/api/vaults/"+id+"/records", create, 200)
	keys := map[string]vaultclient.MemberKey{aliceID: aliceKey.PublicKey(), bobID: bobKey.PublicKey()}
	if err := f.open(aliceSession, alice, id, owner, keys, nil); err != nil {
		t.Fatal(err)
	}
	add := must(aliceSession.ChangeMembership(vaultclient.MembershipChange{Action: "add", Target: pin(bobID, bobKey.PublicKey()), Key: bobKey.PublicKey()}))
	f.do(alice, "POST", "/api/vaults/"+id+"/records", add, 200)
	keys[carolID] = carolKey.PublicKey()
	if err := f.open(aliceSession, alice, id, owner, keys, nil); err != nil {
		t.Fatal(err)
	}
	add = must(aliceSession.ChangeMembership(vaultclient.MembershipChange{Action: "add", Target: pin(carolID, carolKey.PublicKey()), Key: carolKey.PublicKey()}))
	f.do(alice, "POST", "/api/vaults/"+id+"/records", add, 200)

	// Bob loses his devices. A replacement not signed by his recovery key is refused.
	bobNew := must(vaultclient.GenerateIdentity())
	bobHead := f.head(bob, bobID)
	f.do(bob, "PUT", "/api/vault-keys", bobNew.PublicKey(), 409) // enrolment stays immutable
	f.do(bob, "POST", "/api/vault-recovery", must(vaultclient.ReplaceIdentity(bobHead, must(vaultclient.GenerateRecoveryKey()), bobNew)), 403)
	f.do(bob, "POST", "/api/vault-recovery", must(vaultclient.ReplaceIdentity(bobHead, carolRecovery, bobNew)), 403)
	// Signed by his old account key alone (any recovery key it names is its own choice).
	forged := must(vaultclient.ReplaceIdentity(bobHead, carolRecovery, bobNew))
	chosen := must(hybridsig.Generate())
	forged.Record.Recovery = vault.Key{Recipient: carolRecovery.PublicKey().Recipient, Signing: chosen.Public().Bytes()}
	forged.Record = must(forged.Record.Sign(signingKey(t, bobNew), chosen, signingKey(t, bobKey)))
	f.do(bob, "POST", "/api/vault-recovery", forged, 403)
	// Carol cannot replace Bob's key with Bob's own valid replacement.
	replace := must(vaultclient.ReplaceIdentity(bobHead, bobRecovery, bobNew))
	f.do(carol, "POST", "/api/vault-recovery", replace, 403)
	f.do(bob, "POST", "/api/vault-recovery", replace, 204)
	f.do(bob, "POST", "/api/vault-recovery", replace, 204)
	var bobKeyNow drive.VaultKey
	f.decode(f.do(alice, "GET", "/api/vault-keys/"+bobID, nil, 200), &bobKeyNow)
	if bobKeyNow.Fingerprint() != bobNew.PublicKey().Fingerprint() {
		t.Fatal("replacement did not change the enrolled key")
	}

	// Alice's client verifies Bob's chain from the fingerprint the log pins, then re-adds him.
	var bobChain struct{ Records []vaultclient.RecoveryRecord }
	f.decode(f.do(alice, "GET", "/api/vault-recovery/"+bobID+"/records", nil, 200), &bobChain)
	verified, err := vaultclient.VerifyRecoveryChain(bobChain.Records, pin(bobID, bobKey.PublicKey()))
	if err != nil || verified.Fingerprint() != bobNew.PublicKey().Fingerprint() {
		t.Fatalf("chain did not verify to the replacement: %v", err)
	}
	if _, err := vaultclient.VerifyRecoveryChain(bobChain.Records, pin(bobID, carolKey.PublicKey())); err == nil {
		t.Fatal("chain verified from a key it never held")
	}
	if err := f.open(aliceSession, alice, id, owner, keys, nil); err != nil {
		t.Fatal(err)
	}
	// The server re-adds only the enrolled replacement.
	stray := must(aliceSession.Rekey(pin(bobID, carolKey.PublicKey()), carolKey.PublicKey(), nil))
	f.do(alice, "POST", "/api/vaults/"+id+"/records", stray, 409)
	rekey := must(aliceSession.Rekey(pin(bobID, verified), verified, nil))
	// A rekey moves only its target: changing another member's pin is refused.
	wider := rekey
	wider.State.Members = append([]vault.Member(nil), rekey.State.Members...)
	wider.State.Members[2].Fingerprint, wider.Signature = bobNew.PublicKey().Fingerprint(), nil
	f.do(alice, "POST", "/api/vaults/"+id+"/records", must(wider.Sign(signingKey(t, aliceKey))), 409)
	f.do(alice, "POST", "/api/vaults/"+id+"/records", rekey, 200)
	keys[bobID] = bobNew.PublicKey()
	chains := func() map[string][]vaultclient.RecoveryRecord {
		out := map[string][]vaultclient.RecoveryRecord{}
		for _, user := range []string{aliceID, bobID} {
			var chain struct{ Records []vaultclient.RecoveryRecord }
			f.decode(f.do(carol, "GET", "/api/vault-recovery/"+user+"/records", nil, 200), &chain)
			out[user] = chain.Records
		}
		return out
	}
	bobSession := must(vaultclient.NewSession(bobID, org, bobNew))
	if err := f.open(bobSession, bob, id, owner, keys, chains()); err != nil {
		t.Fatalf("re-added member could not open the vault: %v", err)
	}
	if err := f.open(bobSession, bob, id, owner, keys, nil); err == nil {
		t.Fatal("history verified without the replaced key")
	}

	// Rotation needs the old recovery key; a replayed replacement is refused afterwards.
	bobRotated := must(vaultclient.GenerateRecoveryKey())
	bobHead = f.head(bob, bobID)
	f.do(bob, "POST", "/api/vault-recovery", must(vaultclient.RotateRecovery(bobHead, bobNew, carolRecovery, bobRotated)), 403)
	f.do(bob, "POST", "/api/vault-recovery", must(vaultclient.RotateRecovery(bobHead, bobNew, bobRecovery, bobRotated)), 204)
	f.do(bob, "POST", "/api/vault-recovery", replace, 409)
	f.decode(f.do(bob, "GET", "/api/vault-recovery", nil, 200), &fetched)
	if _, err := vaultclient.OpenRecovery(fetched, bobRecovery); err == nil {
		t.Fatal("the rotated-out recovery key still opens the copy")
	}
	if _, err := vaultclient.OpenRecovery(fetched, bobRotated); err != nil {
		t.Fatal(err)
	}

	// Alice replaces her key. Her old key signs nothing, and her new key signs only her own
	// rekey until the log pins it.
	aliceNew := must(vaultclient.GenerateIdentity())
	if err := f.open(aliceSession, alice, id, owner, keys, chains()); err != nil {
		t.Fatal(err)
	}
	f.do(alice, "POST", "/api/vault-recovery", must(vaultclient.ReplaceIdentity(f.head(alice, aliceID), aliceRecovery, aliceNew)), 204)
	remove := must(aliceSession.ChangeMembership(vaultclient.MembershipChange{Action: "remove", Target: pin(bobID, bobNew.PublicKey())}))
	f.do(alice, "POST", "/api/vaults/"+id+"/records", remove, 400)
	remove.Signature = nil
	f.do(alice, "POST", "/api/vaults/"+id+"/records", must(remove.Sign(signingKey(t, aliceNew))), 409)
	// Revisions too: the old key's signature fails, the unpinned new key is refused.
	vaultKey := must(age.GenerateHybridIdentity())
	doc := vaultDocFixture{srv: srv, owner: alice, key: signingKey(t, aliceKey), identity: vaultKey, cp: f.cp,
		doc: vault.Document{Org: org, Vault: id, File: uuid.NewString(), Generation: f.state.Generation, Head: f.cp.Head, Signer: aliceID}}
	payload := must(doc.encrypt([]byte("synthetic revision")))
	if w := must(doc.publish(payload, 1)); w.Code == 204 {
		t.Fatal("the replaced key published a revision")
	}
	doc.key = signingKey(t, aliceNew)
	if w := must(doc.publish(payload, 1)); w.Code != 409 {
		t.Fatalf("the unpinned replacement published a revision: %d", w.Code)
	}
	self := must(aliceSession.Rekey(pin(aliceID, aliceNew.PublicKey()), aliceNew.PublicKey(), aliceNew))
	f.do(alice, "POST", "/api/vaults/"+id+"/records", self, 200)
	keys[aliceID] = aliceNew.PublicKey()
	aliceSession = must(vaultclient.NewSession(aliceID, org, aliceNew))
	if err := f.open(aliceSession, alice, id, owner, keys, chains()); err != nil {
		t.Fatalf("self-rekeyed manager could not open the vault: %v", err)
	}
	// A server that enrols its own key for Alice cannot forge her self-rekey: her chain never
	// moves to that key.
	planted := must(vaultclient.GenerateIdentity())
	forgedRekey := must(aliceSession.Rekey(pin(aliceID, planted.PublicKey()), planted.PublicKey(), planted))
	var history struct {
		Records []vault.Record `json:"records"`
	}
	f.decode(f.do(bob, "GET", "/api/vaults/"+id+"/records", nil, 200), &history)
	input := vaultclient.HistoryInput{Invite: vaultclient.Invitation{Vault: id, Owner: owner}, Records: history.Records, Keys: keys, Recovery: chains()}
	if _, err := bobSession.VerifyHistory(input); err != nil {
		t.Fatalf("genuine history: %v", err)
	}
	input.Records = append(input.Records, forgedRekey)
	input.Keys = map[string]vaultclient.MemberKey{aliceID: planted.PublicKey(), bobID: keys[bobID], carolID: keys[carolID]}
	if _, err := bobSession.VerifyHistory(input); err == nil {
		t.Fatal("a self-rekey to a key outside the recovery chain verified")
	}
	doc.cp, doc.doc.Generation, doc.doc.Head = f.cp, f.state.Generation, f.cp.Head
	if w := must(doc.publish(payload, 1)); w.Code != 204 {
		t.Fatalf("the re-keyed account could not publish: %d %s", w.Code, w.Body)
	}

	// Another org's active account sees neither the chain nor a copy.
	db := testdb.Open(t, cfg.Database)
	otherOrg := uuid.NewString()
	for _, q := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO drive_orgs(id,status) VALUES(?,'active')`, []any{otherOrg}},
		{`INSERT INTO server_settings(key,value,updated_at) VALUES(?,?,CURRENT_TIMESTAMP)`, []any{drive.OrgsFreshUntil, "9999999999"}},
		{`INSERT INTO users(id,username,status,created_at,updated_at,org_id) VALUES('usr_vrother','vrother','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,?)`, []any{otherOrg}},
	} {
		if _, err := db.ExecContext(t.Context(), q.query, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Drive().VaultRecoveryHistory(t.Context(), "usr_vrother", bobID); err != vault.ErrMissing {
		t.Fatalf("cross-org chain returned %v", err)
	}
	if _, err := st.Drive().VaultRecoveryCopy(t.Context(), "usr_vrother"); err != vault.ErrMissing {
		t.Fatalf("cross-org copy returned %v", err)
	}
	if err := st.Drive().CommitVaultRecovery(t.Context(), "usr_vrother", drive.VaultRecovery{Record: replace.Record, Copy: replace.Copy}); err != vault.ErrDenied {
		t.Fatalf("cross-org commit returned %v", err)
	}

	// The database holds no private key material.
	rows, err := db.QueryContext(t.Context(), `SELECT body||COALESCE(recovery_copy,'') FROM drive_vault_recovery UNION ALL SELECT body FROM drive_vault_user_keys`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{secretOf(aliceKey), secretOf(bobKey), secretOf(bobNew), bobRecovery.String(), bobRotated.String()} {
			if strings.Contains(row, secret) {
				t.Fatal("a stored row holds a private key")
			}
		}
	}
}

// A recovery copy that is not age ciphertext to a hybrid recipient is refused even when signed.
func TestVaultRecoveryRefusesPlaintextCopy(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	cfg.Vault = config.VaultConfig{Publisher: must(hybridsig.Generate()), Witness: must(hybridsig.Generate()).Public()}
	srv = api.NewServer(cfg, st)
	f := recoveryFixture{t: t, srv: srv}
	device := linkSigned(t, srv, loginAs(t, srv, st, "vrplain", "user"), api.DeviceOriginForTest(srv))
	identity := must(vaultclient.GenerateIdentity())
	f.do(device, "PUT", "/api/vault-keys", identity.PublicKey(), 204)
	recoveryID := must(age.GenerateHybridIdentity())
	recoverySigner := must(hybridsig.Generate())
	member, _, _ := identity.Secrets()
	for _, plain := range [][]byte{[]byte(member), []byte("age-encryption.org/v1\n-> X25519 abc\n--- x\n" + member)} {
		record := vault.RecoveryRecord{Org: drive.DefaultOrg, User: "usr_vrplain", Sequence: 1, Change: "register",
			Key: vault.Key(identity.PublicKey()), Recovery: vault.Key{Recipient: recoveryID.Recipient().String(), Signing: recoverySigner.Public().Bytes()},
			Copy: vault.Digest(plain)}
		record = must(record.Sign(signingKey(t, identity), recoverySigner, nil))
		f.do(device, "POST", "/api/vault-recovery", vaultclient.RecoveryUpload{Record: record, Copy: plain}, 400)
	}
	f.do(device, "GET", "/api/vault-recovery", nil, 404)
}

func TestVaultRecoveryLimitsAndOrigin(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	cfg.Vault = config.VaultConfig{Publisher: must(hybridsig.Generate()), Witness: must(hybridsig.Generate()).Public()}
	srv = api.NewServer(cfg, st)
	f := recoveryFixture{t: t, srv: srv}
	device := linkSigned(t, srv, loginAs(t, srv, st, "vrlimit", "user"), api.DeviceOriginForTest(srv))
	for _, site := range []string{"https://evil.example", cfg.Server.AppURL} {
		if w := serve(srv, device.request("GET", "/api/vault-recovery", nil, "Origin", site)); w.Code != http.StatusUnauthorized {
			t.Fatalf("origin %s reached recovery: %d", site, w.Code)
		}
	}
	if w := serve(srv, httptest.NewRequest("GET", "/api/vault-recovery", nil)); w.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned recovery fetch: %d", w.Code)
	}
	for range 10 {
		f.do(device, "POST", "/api/vault-recovery", map[string]any{}, 400)
		f.do(device, "GET", "/api/vault-recovery", nil, 404)
	}
	f.do(device, "POST", "/api/vault-recovery", map[string]any{}, 429)
	f.do(device, "GET", "/api/vault-recovery", nil, 429)
	audits, _, err := st.Audit().ListAuditRecords(t.Context(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	counted := map[string]int{}
	for _, a := range audits {
		if a.UserID == "usr_vrlimit" {
			counted[a.Action+" "+a.Details]++
		}
	}
	if counted["vault.recovery_commit result=refused"] != 11 || counted["vault.recovery_fetch result=refused"] != 11 {
		t.Fatalf("recovery attempts were not all audited: %v", counted)
	}
}
