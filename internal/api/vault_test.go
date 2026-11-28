package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"filippo.io/age"
	"github.com/Busnes-app/ky-primitives/hybridsig"
	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
	"github.com/Busnes-app/kydrive-server/internal/vault"
	"github.com/google/uuid"
)

func TestVaultWitnessAndRevocation(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	hybrid := func() *hybridsig.PrivateKey {
		t.Helper()
		key, err := hybridsig.Generate()
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	identity := func() *age.HybridIdentity {
		t.Helper()
		id, err := age.GenerateHybridIdentity()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	inactiveRoutes := serve(srv, httptest.NewRequest(http.MethodPut, "/api/vault-keys", nil))
	if inactiveRoutes.Code != 404 {
		t.Fatalf("unconfigured vault routes returned %d", inactiveRoutes.Code)
	}
	publisher, witnessKey := hybrid(), hybrid()
	witnessPath := filepath.Join(t.TempDir(), "witness.db")
	witness, err := vault.InitializeWitness(t.Context(), witnessPath, witnessKey, publisher.Public())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { witness.Close() }()
	cfg.Vault = config.VaultConfig{Publisher: publisher, Witness: witnessKey.Public()}
	srv = api.NewServer(cfg, st)
	origin := api.DeviceOriginForTest(srv)
	manager := linkSigned(t, srv, loginAs(t, srv, st, "vmgr", "user"), origin)
	reader := linkSigned(t, srv, loginAs(t, srv, st, "vreader", "user"), origin)
	outsider := linkSigned(t, srv, loginAs(t, srv, st, "vadmin", "admin"), origin)
	request := func(device *signer, method, path string, payload any, want int) *httptest.ResponseRecorder {
		t.Helper()
		var data []byte
		if payload != nil {
			data, err = json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
		}
		w := serve(srv, device.request(method, path, data))
		if w.Code != want {
			t.Fatalf("%s %s: status %d, want %d (%s)", method, path, w.Code, want, w.Body)
		}
		return w
	}
	decode := func(w *httptest.ResponseRecorder, v any) {
		t.Helper()
		if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
			t.Fatal(err)
		}
	}
	accountPath := "/api/vault/account"
	if unsigned := serve(srv, httptest.NewRequest(http.MethodGet, accountPath, nil)); unsigned.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned account discovery returned %d", unsigned.Code)
	}
	var scope struct {
		User string `json:"user_id"`
		Org  string `json:"org_id"`
	}
	decode(request(manager, http.MethodGet, accountPath, nil, http.StatusOK), &scope)
	if scope.User != "usr_vmgr" || scope.Org != drive.DefaultOrg {
		t.Fatalf("unexpected signed vault scope: %+v", scope)
	}
	wrap := func(to age.Recipient, data string) []byte {
		t.Helper()
		var out bytes.Buffer
		writer, err := age.Encrypt(&out, to)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = writer.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	memberKey, readerKey := hybrid(), hybrid()
	managerIdentity, readerIdentity, first := identity(), identity(), identity()
	managerKey := drive.VaultKey{Recipient: managerIdentity.Recipient().String(), Signing: memberKey.Public().Bytes()}
	viewerKey := drive.VaultKey{Recipient: readerIdentity.Recipient().String(), Signing: readerKey.Public().Bytes()}
	request(manager, "PUT", "/api/vault-keys", managerKey, 204)
	request(manager, "PUT", "/api/vault-keys", managerKey, 204)
	request(manager, "PUT", "/api/vault-keys", viewerKey, 409)
	request(reader, "PUT", "/api/vault-keys", viewerKey, 204)

	create := vault.Record{
		Org: drive.DefaultOrg, Vault: uuid.NewString(), Sequence: 1, Change: "create", Signer: "usr_vmgr",
		State: vault.State{
			Generation: 1, Recipient: first.Recipient().String(),
			NameIndex: wrap(first.Recipient(), "synthetic name index"),
			Members: []vault.Member{{User: "usr_vmgr", Fingerprint: managerKey.Fingerprint(), Role: vault.Manager,
				Wrap: wrap(managerIdentity.Recipient(), first.String())}},
		},
	}
	path := "/api/vaults/" + create.Vault
	sign := func(record vault.Record, key *hybridsig.PrivateKey) vault.Record {
		t.Helper()
		signed, err := record.Sign(key)
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}
	create = sign(create, memberKey)
	bad := create
	bad.Org = uuid.NewString()
	request(manager, "POST", path+"/records", sign(bad, memberKey), 403)
	commit := func(record vault.Record) vault.Receipt {
		t.Helper()
		var receipt vault.Receipt
		decode(request(manager, "POST", path+"/records", record, 200), &receipt)
		return receipt
	}
	advance := func(receipts ...vault.Receipt) vault.Checkpoint {
		t.Helper()
		body, err := json.Marshal(receipts)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/v1/logs/"+receipts[0].Log, bytes.NewReader(body))
		w := httptest.NewRecorder()
		witness.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("advance: %d %s", w.Code, w.Body)
		}
		var cp vault.Checkpoint
		decode(w, &cp)
		return cp
	}
	check := func(device *signer, cp vault.Checkpoint, edit bool, want int) {
		t.Helper()
		request(device, "POST", path+"/check", map[string]any{"checkpoint": cp, "edit": edit}, want)
	}
	genesis := commit(create)
	if retry := commit(create); retry.Link != genesis.Link || retry.Log != genesis.Log {
		t.Fatal("retry returned a different head")
	}
	check(manager, vault.Checkpoint{}, true, 409)
	add := create
	add.Sequence, add.Previous, add.Change, add.Target = 2, create.Link().Hash(), "add", "usr_vreader"
	proofDir := os.Getenv("KYDRIVE_DOCX_FIXTURE")
	gatewayFixture := os.Getenv("KYDRIVE_VAULT_GATEWAY_FIXTURE") == "1"
	role := vault.Reader
	if proofDir != "" || gatewayFixture {
		role = vault.Editor
	}
	add.State.Members = append(slices.Clone(create.State.Members), vault.Member{
		User: add.Target, Fingerprint: viewerKey.Fingerprint(), Role: role,
		Wrap: wrap(readerIdentity.Recipient(), first.String()),
	})
	add = sign(add, memberKey)
	old := advance(genesis, commit(add))
	if proofDir != "" || gatewayFixture {
		firstDoc := vaultDocFixture{srv: srv, owner: manager, key: memberKey, identity: first, cp: old,
			doc: vault.Document{Org: create.Org, Vault: create.Vault, File: uuid.NewString(), Generation: 1, Head: old.Head, Signer: create.Signer}}
		grants := func(link string, rotated bool) []vault.EditorGrant {
			result := []vault.EditorGrant{{User: create.Signer, Role: vault.Manager, Wrapped: wrap(managerIdentity.Recipient(), link)}}
			if !rotated {
				result = append(result, vault.EditorGrant{User: add.Target, Role: vault.Editor, Wrapped: wrap(readerIdentity.Recipient(), link)})
			}
			return result
		}
		rotate := func(current vaultDocFixture) vaultDocFixture {
			next := identity()
			remove := add
			remove.Sequence, remove.Previous, remove.Change = 3, add.Link().Hash(), "remove"
			remove.State = vault.State{Generation: 2, Recipient: next.Recipient().String(),
				Previous: wrap(next.Recipient(), first.String()), NameIndex: wrap(next.Recipient(), "fixture index"),
				Members: []vault.Member{{User: create.Signer, Fingerprint: managerKey.Fingerprint(), Role: vault.Manager,
					Wrap: wrap(managerIdentity.Recipient(), next.String())}}}
			current.cp = advance(commit(sign(remove, memberKey)))
			current.doc.Generation, current.doc.Head, current.identity, current.binding = 2, current.cp.Head, next, ""
			return current
		}
		if gatewayFixture {
			runVaultGateway(t, firstDoc, reader, grants("synthetic encrypted capability", false), rotate)
		} else {
			runVaultBrowser(t, proofDir, firstDoc, reader, grants, rotate)
		}
		return
	}
	check(manager, old, true, 204)
	check(reader, old, false, 204)
	check(reader, old, true, 403)
	check(outsider, old, false, 404)

	doc := vaultDocFixture{srv: srv, owner: manager, key: memberKey, identity: first, cp: old,
		doc: vault.Document{Org: create.Org, Vault: create.Vault, File: uuid.NewString(), Generation: 1, Head: old.Head, Signer: create.Signer}}
	ciphertext := wrap(first.Recipient(), "synthetic Office source")
	doc.expectPublish(t, ciphertext, 1, 204)
	// Exact retries are acknowledged, including when the first response was lost.
	doc.expectPublish(t, ciphertext, 1, 204)
	room := "0a000000000000000000000000000000"
	blob := strings.Repeat("ab", 24)
	binding, bindStatus, err := doc.bind(1, []string{room}, []vault.EditorGrant{
		{User: create.Signer, Role: vault.Manager, Wrapped: wrap(managerIdentity.Recipient(), "synthetic edit capability")},
		{User: add.Target, Role: vault.Reader, Wrapped: wrap(readerIdentity.Recipient(), "synthetic view capability")},
	}, blob)
	if err != nil || bindStatus.Code != 204 {
		t.Fatalf("bind status %v: %v", bindStatus, err)
	}
	saved := wrap(first.Recipient(), "synthetic Office export")
	doc.expectPublish(t, saved, 2, 409)
	doc.binding = binding.ID()
	doc.expectPublish(t, saved, 2, 204)
	doc.expectPublish(t, wrap(first.Recipient(), "another stale export"), 2, 409)
	editLease, code := doc.lease(manager, true)
	if code != 200 || vaultRelayFixtureCheck(srv, editLease, room, true) != 204 {
		t.Fatalf("editor lease status %d", code)
	}
	checkBlob := func(token, id string) int {
		r := httptest.NewRequest("POST", "/api/vault-relay/check", bytes.NewReader(vaultJSON(map[string]string{"blob": id})))
		r.Header.Set("Authorization", "Bearer "+token)
		return serve(srv, r).Code
	}
	if checkBlob(editLease, blob) != 204 || checkBlob(editLease, strings.Repeat("cd", 24)) != 403 ||
		checkBlob(editLease, room) != 400 {
		t.Fatal("blob authorization was wider than the signed binding")
	}
	viewLease, code := doc.lease(reader, false)
	if code != 200 || vaultRelayFixtureCheck(srv, viewLease, room, false) != 204 ||
		vaultRelayFixtureCheck(srv, viewLease, room, true) != 403 {
		t.Fatalf("reader lease status %d", code)
	}
	if got, err := doc.fetch(reader, "latest", 200); err != nil || string(got) != "synthetic Office export" {
		t.Fatalf("download changed exported bytes: %v", err)
	}
	if _, err := doc.fetch(outsider, "latest", 404); err != nil {
		t.Fatal(err)
	}

	// An active account in another org cannot fetch even the public key or vault log.
	db := testdb.Open(t, cfg.Database)
	otherOrg := uuid.NewString()
	for _, fixture := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO drive_orgs(id,status) VALUES(?,'active')`, []any{otherOrg}},
		{`INSERT INTO server_settings(key,value,updated_at) VALUES(?,?,CURRENT_TIMESTAMP)`,
			[]any{drive.OrgsFreshUntil, "9999999999"}},
		{`INSERT INTO users(id,username,status,created_at,updated_at,org_id)
			VALUES('usr_other','other','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,?)`, []any{otherOrg}},
	} {
		if _, err := db.ExecContext(t.Context(), fixture.query, fixture.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Drive().VaultUserKey(t.Context(), "usr_other", create.Signer); err != vault.ErrMissing {
		t.Fatalf("cross-org key returned %v", err)
	}
	if _, err := st.Drive().VaultRecords(t.Context(), "usr_other", create.Vault, 0); err != vault.ErrMissing {
		t.Fatalf("cross-org log returned %v", err)
	}
	if got := send(t, srv, "GET", path+"/records", "", nil, manager.token); got.Code != 401 {
		t.Fatalf("unsigned device token: %d", got.Code)
	}

	second := identity()
	remove := add
	remove.Sequence, remove.Previous, remove.Change = 3, add.Link().Hash(), "remove"
	remove.State = vault.State{
		Generation: 2, Recipient: second.Recipient().String(),
		Previous: wrap(second.Recipient(), first.String()), NameIndex: wrap(second.Recipient(), "synthetic name index"),
		Members: []vault.Member{{User: create.Signer, Fingerprint: managerKey.Fingerprint(), Role: vault.Manager,
			Wrap: wrap(managerIdentity.Recipient(), second.String())}},
	}
	request(manager, "POST", path+"/records", sign(remove, readerKey), 400)
	remove = sign(remove, memberKey)
	commit(remove) // simulate loss of the first response before checkpointing
	if checkBlob(editLease, blob) != 403 || vaultRelayFixtureCheck(srv, editLease, room, true) != 403 ||
		vaultRelayFixtureCheck(srv, viewLease, room, false) != 403 {
		t.Fatal("superseded room accepted an existing lease")
	}
	if _, err := doc.fetch(reader, "latest", 404); err != nil {
		t.Fatal(err)
	}
	doc.expectPublish(t, wrap(first.Recipient(), "old room export"), 3, 409)
	check(manager, old, true, 409)
	check(reader, old, false, 404)
	request(reader, "GET", path+"/records?after=2", nil, 404)
	var recovery struct {
		drive.VaultHistory
		Receipts []vault.Receipt `json:"receipts"`
	}
	decode(request(manager, "GET", path+"/records?after=2", nil, 200), &recovery)
	if len(recovery.Records) != 1 || len(recovery.Receipts) != 1 || recovery.Records[0].Link() != remove.Link() {
		t.Fatalf("retry did not recover the committed record")
	}
	current := advance(recovery.Receipts...)
	if current.Head != recovery.Head {
		t.Fatal("witness and catalogue differ")
	}
	if err := witness.Close(); err != nil {
		t.Fatal(err)
	}
	witness, err = vault.OpenWitness(t.Context(), witnessPath, witnessKey, publisher.Public())
	if err != nil {
		t.Fatal(err)
	}
	current, err = witness.Current(t.Context(), recovery.Head.Log)
	if err != nil {
		t.Fatal(err)
	}
	check(manager, current, true, 204)
	check(reader, current, false, 404)

	reuse := remove
	reuse.Change, reuse.Target, reuse.Sequence, reuse.Previous = "rotate", "", 4, remove.Link().Hash()
	reuse.State.Generation, reuse.State.Recipient = 3, first.Recipient().String()
	reuse.State.Previous = wrap(first.Recipient(), second.String())
	reuse.State.NameIndex = wrap(first.Recipient(), "synthetic name index")
	reuse.State.Members = slices.Clone(remove.State.Members)
	reuse.State.Members[0].Wrap = wrap(managerIdentity.Recipient(), first.String())
	request(manager, "POST", path+"/records", sign(reuse, memberKey), 409)
	check(manager, current, true, 204)

	var race sync.WaitGroup
	status := make(chan int, 2)
	for range 2 {
		nextKey := identity()
		next := reuse
		next.State.Recipient = nextKey.Recipient().String()
		next.State.Previous = wrap(nextKey.Recipient(), second.String())
		next.State.NameIndex = wrap(nextKey.Recipient(), "synthetic name index")
		next.State.Members = slices.Clone(reuse.State.Members)
		next.State.Members[0].Wrap = wrap(managerIdentity.Recipient(), nextKey.String())
		data, err := json.Marshal(sign(next, memberKey))
		if err != nil {
			t.Fatal(err)
		}
		req := manager.request("POST", path+"/records", data)
		race.Go(func() { status <- serve(srv, req).Code })
	}
	race.Wait()
	got := []int{<-status, <-status}
	slices.Sort(got)
	if !slices.Equal(got, []int{200, 409}) {
		t.Fatalf("racing commits returned %v", got)
	}
	check(manager, current, true, 409)
}
