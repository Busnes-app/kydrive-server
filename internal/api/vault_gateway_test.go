package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/vault"
	"github.com/google/uuid"
)

func stagingCert(t *testing.T, dir, name, ip string) (string, string, string) {
	t.Helper()
	cert, key := filepath.Join(dir, name+".pem"), filepath.Join(dir, name+".key")
	out, err := exec.CommandContext(t.Context(), "openssl", "req", "-x509", "-newkey", "ec",
		"-pkeyopt", "ec_paramgen_curve:P-256", "-nodes", "-days", "1", "-subj", "/CN=ky-vault-staging",
		"-addext", "subjectAltName=IP:"+ip, "-out", cert, "-keyout", key).CombinedOutput()
	if err != nil {
		t.Fatalf("fixture certificate: %v %s", err, out)
	}
	raw, err := os.ReadFile(cert)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatal("missing fixture certificate")
	}
	x, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(x.RawSubjectPublicKeyInfo)
	return cert, key, base64.StdEncoding.EncodeToString(hash[:])
}

type gatewayFixture struct {
	url, pin, root string
	client         *http.Client
	compose        func(context.Context, []byte, ...string) ([]byte, error)
}

func startVaultGateway(t *testing.T, ctx context.Context, origins string, handler http.Handler) gatewayFixture {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	out, err := exec.CommandContext(t.Context(), "docker", "network", "inspect", "bridge",
		"--format", "{{range .IPAM.Config}}{{.Gateway}}{{end}}").Output()
	host := strings.TrimSpace(string(out))
	if err != nil || net.ParseIP(host).To4() == nil {
		t.Fatalf("resolve Docker host bridge: %v", err)
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	gatewayCert, gatewayKey, gatewayPin := stagingCert(t, dir, "gateway", "127.0.0.1")
	authorityCert, authorityKey, _ := stagingCert(t, dir, "authority", host)
	authority := &http.Server{Handler: http.NewServeMux(), ReadHeaderTimeout: 5 * time.Second,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13}}
	authority.Handler.(*http.ServeMux).Handle("POST /api/vault-relay/check", handler)
	t.Cleanup(func() { _ = authority.Close() })
	go authority.ServeTLS(listener, authorityCert, authorityKey)

	project := "vault-stage-" + uuid.NewString()[:8]
	composePath := filepath.Join(root, "deploy/vault-gateway/compose.yml")
	env := append(os.Environ(),
		"KY_VAULT_RELAY_AUTHORITY=https://"+listener.Addr().String(),
		"KY_VAULT_GATEWAY_CERT="+gatewayCert, "KY_VAULT_GATEWAY_KEY="+gatewayKey,
		"KY_VAULT_AUTHORITY_CA="+authorityCert, "KY_VAULT_GATEWAY_PORT=0",
		"KY_VAULT_CLIENT_ORIGINS="+origins,
		"KY_VAULT_RELAY_ORIGIN=https://localhost:9443", "KY_VAULT_SANDBOX_ORIGIN=https://sandbox.localhost:9444")
	compose := func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "docker", append([]string{"compose", "-p", project, "-f", composePath}, args...)...)
		cmd.Dir, cmd.Env, cmd.Stdin = root, env, bytes.NewReader(input)
		return cmd.CombinedOutput()
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if out, err := compose(ctx, nil, "down", "-v"); err != nil {
			t.Errorf("staging cleanup: %v %s", err, out)
		}
	})
	if out, err := compose(ctx, nil, "up", "-d", "--build"); err != nil {
		t.Fatalf("start staging: %v %s", err, out)
	}

	out, err = compose(ctx, nil, "port", "gateway", "9443")
	if err != nil {
		t.Fatalf("staging gateway port: %v %s", err, out)
	}
	gateway := "https://" + strings.TrimSpace(string(out))
	relayID, err := compose(ctx, nil, "ps", "-q", "relay")
	if err != nil || strings.TrimSpace(string(relayID)) == "" {
		t.Fatalf("staging relay container unavailable: %v", err)
	}
	relay := strings.TrimSpace(string(relayID))
	out, err = exec.CommandContext(ctx, "docker", "inspect", "--format", "{{json .HostConfig.PortBindings}}", relay).Output()
	if err != nil || (strings.TrimSpace(string(out)) != "{}" && strings.TrimSpace(string(out)) != "null") {
		t.Fatalf("upstream has exposed ports: %v %s", err, out)
	}
	out, err = exec.CommandContext(ctx, "docker", "inspect", "--format",
		"{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", relay).Output()
	relayIP := strings.TrimSpace(string(out))
	if err != nil || net.ParseIP(relayIP).To4() == nil {
		t.Fatalf("resolve private relay address: %v", err)
	}
	// An edge-network peer cannot reach the private address, even without DNS.
	probe := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", project+"_edge",
		"--entrypoint", "node", project+"-gateway", "-e",
		"fetch(process.argv[1],{signal:AbortSignal.timeout(800)}).then(()=>process.exit(3),()=>{})",
		"http://"+net.JoinHostPort(relayIP, "3000")+"/api/config")
	if out, err := probe.CombinedOutput(); err != nil {
		t.Fatalf("private relay address isolation: %v %s", err, out)
	}

	// Wait for both services before the browser asks for a one-minute lease.
	ready, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	caPEM, err := os.ReadFile(gatewayCert)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}, Timeout: time.Second}
	for {
		r, _ := http.NewRequestWithContext(ready, "GET", gateway+"/api/config", nil)
		if resp, err := client.Do(r); err == nil {
			resp.Body.Close()
			if resp.StatusCode != 404 {
				t.Fatalf("raw config path returned %d", resp.StatusCode)
			}
			break
		}
		if ready.Err() != nil {
			t.Fatal("staging gateway did not become ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
	for {
		if _, err := compose(ready, nil, "exec", "-T", "relay", "node", "-e",
			"fetch('http://127.0.0.1:3000/api/config',{signal:AbortSignal.timeout(800)}).then(r=>process.exit(r.ok?0:1),()=>process.exit(1))"); err == nil {
			break
		}
		if ready.Err() != nil {
			out, _ := compose(ctx, nil, "logs", "--tail=35", "relay")
			t.Fatalf("private relay did not become ready: %s", out)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return gatewayFixture{gateway, gatewayPin, root, client, compose}
}

func runVaultGateway(t *testing.T, f vaultDocFixture, collaborator *signer,
	grants []vault.EditorGrant, rotate func(vaultDocFixture) vaultDocFixture) {
	t.Helper()
	var unavailable atomic.Bool

	var revoked atomic.Bool
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<!doctype html><title>Vault staging fixture</title>")
		case "POST /authority-off":
			unavailable.Store(true)
			w.WriteHeader(204)
		case "POST /authority-on":
			unavailable.Store(false)
			w.WriteHeader(204)
		case "POST /revoke":
			if !revoked.CompareAndSwap(false, true) {
				w.WriteHeader(409)
				return
			}
			current := rotate(f)
			const successorRoom = "02000000000000000000000000000002"
			_, resp, err := current.bind(1, []string{successorRoom}, grants[:1])
			if err != nil || resp.Code != 204 {
				http.Error(w, "could not bind the successor room", 500)
				return
			}
			token, status := current.lease(current.owner, true)
			if status != 200 {
				http.Error(w, "could not authorize the successor room", 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(vaultJSON(map[string]string{"room": successorRoom, "token": token}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	stage := startVaultGateway(t, ctx, control.URL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unavailable.Load() {
			w.WriteHeader(503)
			return
		}
		f.srv.ServeHTTP(w, r)
	}))

	const room = "01000000000000000000000000000001"
	boundBlob, foreignBlob := strings.Repeat("12", 24), strings.Repeat("34", 24)
	blobBytes, err := f.encrypt([]byte("synthetic attachment"))
	if err != nil {
		t.Fatal(err)
	}
	for _, blob := range []string{boundBlob, foreignBlob} {
		out, err := stage.compose(ctx, blobBytes, "exec", "-T", "relay", "sh", "-ec",
			`mkdir -p "/cryptpad/blob/$1"; cat > "/cryptpad/blob/$1/$2"`, "sh", blob[:2], blob)
		if err != nil {
			t.Fatalf("seed fixture blob: %v %s", err, out)
		}
	}

	initial, err := f.encrypt([]byte("synthetic document revision"))
	if err != nil {
		t.Fatal(err)
	}
	f.expectPublish(t, initial, 1, 204)
	_, resp, err := f.bind(1, []string{room}, grants, boundBlob)
	if err != nil || resp.Code != 204 {
		t.Fatalf("stage signed binding: %v %v", err, resp)
	}

	edit, editorStatus := f.lease(collaborator, true)
	view, readerStatus := f.lease(f.owner, false)
	if editorStatus != 200 || readerStatus != 200 {
		t.Fatalf("stage leases unavailable: %d %d", editorStatus, readerStatus)
	}
	for _, raw := range []string{"/api/config", "/block/" + boundBlob, "/datastore/" + room + ".ndjson",
		"/blob/" + boundBlob[:2] + "/" + boundBlob + "?v=1"} {
		req, _ := http.NewRequestWithContext(ctx, "GET", stage.url+raw, nil)
		req.Header.Set("Authorization", "Bearer "+edit)
		resp, err := stage.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatalf("raw upstream route %s returned %d", raw, resp.StatusCode)
		}
	}
	script := exec.CommandContext(ctx, "node", filepath.Join(stage.root, "deploy/vault-gateway/accept.mjs"))
	script.Dir = stage.root
	script.Env = append(os.Environ(),
		"KY_TEST_GATEWAY="+stage.url, "KY_TEST_GATEWAY_PIN="+stage.pin,
		"KY_TEST_GATEWAY_CONTROL="+control.URL, "KY_TEST_GATEWAY_ROOM="+room,
		"KY_TEST_GATEWAY_EDIT="+edit, "KY_TEST_GATEWAY_VIEW="+view,
		"KY_TEST_GATEWAY_BLOB="+boundBlob, "KY_TEST_GATEWAY_FOREIGN_BLOB="+foreignBlob,
		"KY_TEST_GATEWAY_BLOB_DIGEST="+fmt.Sprintf("%x", sha256.Sum256(blobBytes)))
	output, err := script.CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		out, _ := stage.compose(ctx, nil, "logs", "--tail=30")
		t.Fatalf("staging authorization proof: %v\n%s", err, out)
	}
}
